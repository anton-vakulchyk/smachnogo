//go:build eval

// Vision-model bake-off harness (plan M7 step 0). Runs the golden fixtures
// through the real Analyzer for each candidate model and scores structural
// expectations: food detection, dish granularity, calorie plausibility,
// clarification firing where (and only where) it should. Informs the
// quality-per-dollar production-model decision; rerun on every prompt change.
//
//	GEMINI_API_KEY=... go test -tags eval ./eval/ -run TestBakeoff -v -timeout 30m
//
// Env knobs:
//	EVAL_MODELS  comma-separated vision models (default: current default + candidates)
//	EVAL_RATES   per-model $/1M token rates, e.g. "gemini-2.5-flash=0.30/2.50,..."
//	             (omitted models report tokens only — never guess prices in code)
package eval

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"smachnogo/pkg/llm"
	"smachnogo/pkg/llm/gemini"
	"smachnogo/pkg/models"
)

type expectation struct {
	fixture     string
	isFood      bool
	minDishes   int
	maxDishes   int
	kcalMin     int // band over the SUM of dishes; generous — plausibility, not accuracy
	kcalMax     int
	wantClarify *bool // nil = don't score; shake must clarify, plain plate must not
}

func boolPtr(b bool) *bool { return &b }

var golden = []expectation{
	// single_plate: borscht with sour cream, one bowl.
	{fixture: "single_plate.jpg", isFood: true, minDishes: 1, maxDishes: 2, kcalMin: 150, kcalMax: 1500, wantClarify: boolPtr(false)},
	// two_plates is mislabeled: it's ONE dolsot bibimbap in a single stone pot
	// (plus a corner sliver). One dish is the correct granularity.
	{fixture: "two_plates.jpg", isFood: true, minDishes: 1, maxDishes: 2, kcalMin: 300, kcalMax: 1500},
	{fixture: "not_food.jpg", isFood: false},
	// shake: TWO milkshake glasses; opaque contents must trigger clarification.
	{fixture: "shake.jpg", isFood: true, minDishes: 1, maxDishes: 2, kcalMin: 60, kcalMax: 900, wantClarify: boolPtr(true)},
	// two_meals: composite (borscht bowl + milkshake glasses side by side) —
	// the true multi-dish granularity probe: must NOT merge into one entry.
	{fixture: "two_meals.jpg", isFood: true, minDishes: 2, maxDishes: 4, kcalMin: 200, kcalMax: 2000},
}

type runResult struct {
	model    string
	fixture  string
	err      error
	analysis *models.PhotoAnalysis
	usage    llm.Usage
	failures []string
}

func (r *runResult) check(exp expectation) {
	if r.err != nil {
		r.failures = append(r.failures, fmt.Sprintf("error: %v", r.err))
		return
	}
	a := r.analysis
	if a.IsFood != exp.isFood {
		r.failures = append(r.failures, fmt.Sprintf("is_food=%v want %v", a.IsFood, exp.isFood))
		return
	}
	if !exp.isFood {
		return
	}
	if n := len(a.Dishes); n < exp.minDishes || n > exp.maxDishes {
		r.failures = append(r.failures, fmt.Sprintf("dishes=%d want [%d,%d]", n, exp.minDishes, exp.maxDishes))
	}
	total := 0
	for _, d := range a.Dishes {
		total += d.CaloriesKcal
	}
	if total < exp.kcalMin || total > exp.kcalMax {
		r.failures = append(r.failures, fmt.Sprintf("total kcal=%d want [%d,%d]", total, exp.kcalMin, exp.kcalMax))
	}
	if exp.wantClarify != nil {
		fired := false
		for _, d := range a.Dishes {
			if d.NeedsClarification {
				fired = true
			}
		}
		if fired != *exp.wantClarify {
			r.failures = append(r.failures, fmt.Sprintf("clarify=%v want %v", fired, *exp.wantClarify))
		}
	}
}

// rates returns per-model {in, out} $/1M-token prices parsed from EVAL_RATES.
func rates() map[string][2]float64 {
	out := map[string][2]float64{}
	for _, kv := range strings.Split(os.Getenv("EVAL_RATES"), ",") {
		name, prices, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if !ok {
			continue
		}
		in, outp, ok := strings.Cut(prices, "/")
		if !ok {
			continue
		}
		i, err1 := strconv.ParseFloat(in, 64)
		o, err2 := strconv.ParseFloat(outp, 64)
		if err1 == nil && err2 == nil {
			out[name] = [2]float64{i, o}
		}
	}
	return out
}

func TestBakeoff(t *testing.T) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Skip("GEMINI_API_KEY not set")
	}
	modelList := os.Getenv("EVAL_MODELS")
	if modelList == "" {
		modelList = "gemini-2.5-flash,gemini-3-flash-preview,gemini-3.1-flash-lite"
	}
	priceTable := rates()

	images := map[string][]byte{}
	for _, exp := range golden {
		b, err := os.ReadFile("../fixtures/" + exp.fixture)
		if err != nil {
			t.Fatalf("fixture %s: %v", exp.fixture, err)
		}
		images[exp.fixture] = b
	}

	var report []runResult
	for _, model := range strings.Split(modelList, ",") {
		model = strings.TrimSpace(model)
		c := gemini.New(key, model, "gemini-3.1-flash-lite")

		// Fixtures run in parallel per model (independent HTTP calls);
		// models run sequentially to keep 429s attributable.
		results := make([]runResult, len(golden))
		var wg sync.WaitGroup
		for i, exp := range golden {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
				defer cancel()
				start := time.Now()
				a, usage, err := c.AnalyzePhoto(ctx, images[exp.fixture])
				r := runResult{model: model, fixture: exp.fixture, err: err, analysis: a, usage: usage}
				if r.usage.LatencyMS == 0 {
					r.usage.LatencyMS = time.Since(start).Milliseconds()
				}
				r.check(exp)
				results[i] = r
			}()
		}
		wg.Wait()
		report = append(report, results...)
	}

	// Per-model summary table.
	t.Log("model | pass | avg_latency | avg_in_tok | avg_out_tok | $/scan")
	for _, model := range strings.Split(modelList, ",") {
		model = strings.TrimSpace(model)
		var pass, n int
		var lat, tin, tout int64
		for _, r := range report {
			if r.model != model {
				continue
			}
			n++
			if len(r.failures) == 0 {
				pass++
			}
			lat += r.usage.LatencyMS
			tin += int64(r.usage.InputTokens)
			tout += int64(r.usage.OutputTokens)
		}
		if n == 0 {
			continue
		}
		cost := "n/a"
		if p, ok := priceTable[model]; ok {
			perScan := (float64(tin)/float64(n)*p[0] + float64(tout)/float64(n)*p[1]) / 1e6
			cost = fmt.Sprintf("$%.5f", perScan)
		}
		t.Logf("%s | %d/%d | %dms | %d | %d | %s",
			model, pass, n, lat/int64(n), tin/int64(n), tout/int64(n), cost)
	}

	// Detail lines + failures (informational: bake-off output is data, not a gate).
	for _, r := range report {
		if r.err != nil {
			t.Logf("FAIL %s/%s: %v", r.model, r.fixture, r.err)
			continue
		}
		var dishes []string
		for _, d := range r.analysis.Dishes {
			dishes = append(dishes, fmt.Sprintf("%s %dkcal conf=%.2f clarify=%v", d.Label, d.CaloriesKcal, d.Confidence, d.NeedsClarification))
		}
		status := "ok"
		if len(r.failures) > 0 {
			status = "FAIL " + strings.Join(r.failures, "; ")
		}
		t.Logf("%s/%s [%s] is_food=%v lat=%dms in=%d out=%d :: %s",
			r.model, r.fixture, status, r.analysis.IsFood, r.usage.LatencyMS,
			r.usage.InputTokens, r.usage.OutputTokens, strings.Join(dishes, " | "))
	}
}
