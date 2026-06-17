//go:build eval

// Phase 0 detection-recall probe for the regular-vs-diet beverage fork (the
// "cola problem"): a glass of dark soda is visually identical whether it is
// Regular (~140 kcal) or Zero/Diet (~0 kcal). This is a BUILD-NOTHING test —
// it runs ambiguous beverages + unambiguous controls through the PRODUCTION
// vision model on the CURRENT prompt and measures whether needs_clarification
// fires where it should. It gates the decision to build the precomputed-
// variants pipeline: high recall + low false-flag => the model detects the
// fork, so building the pickable-variants UX is worth it; low recall => the
// model needs an explicit prompt rule (and maybe a deterministic label-based
// trigger) BEFORE any schema/UI work.
//
//	GEMINI_API_KEY=... go test -tags eval ./eval/ -run TestBeverageClarifyRecall -v -timeout 20m
//
// Photos: drop phone shots into backend/tests/fixtures/ with the names below.
// IMPORTANT: keep the can/bottle/label OUT of frame — pour into a glass. A
// visible brand label lets the model identify the exact product and is NOT
// the ambiguous case under test. Missing files are skipped, so a partial set
// still yields numbers (the two cola-glass shots are the essential pair).
package eval

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"smachnogo/pkg/llm/gemini"
)

type bevCase struct {
	file      string
	ambiguous bool // true = SHOULD flag (regular/diet fork); false = control, should NOT flag
	note      string
}

// The labeled set. Filenames are the spec for what to photograph.
var beverages = []bevCase{
	{"bev_cola_regular.jpg", true, "regular cola in a glass, no label/can in frame"},
	{"bev_cola_zero.jpg", true, "zero/diet cola in a glass, no label/can in frame"},
	{"bev_dark_soda.jpg", true, "any other dark soda (pepsi/dr pepper) in a glass — optional"},
	{"bev_iced_tea.jpg", true, "iced tea in a glass (sweetened vs unsweetened ambiguous) — optional"},
	{"bev_water.jpg", false, "plain water in a glass — unambiguous ~0 kcal"},
	{"bev_black_coffee.jpg", false, "black coffee in a cup — unambiguous ~0 kcal"},
	{"bev_orange_juice.jpg", false, "orange juice in a glass — caloric but unambiguous"},
}

func TestBeverageClarifyRecall(t *testing.T) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Skip("GEMINI_API_KEY not set")
	}
	// One production model by default (quota-friendly); first value if a list.
	model := os.Getenv("EVAL_MODELS")
	if model == "" {
		model = "gemini-2.5-flash"
	}
	if i := strings.IndexByte(model, ','); i >= 0 {
		model = model[:i]
	}
	model = strings.TrimSpace(model)
	c := gemini.New(key, model, "gemini-3.1-flash-lite")

	images := map[string][]byte{}
	var present []bevCase
	for _, b := range beverages {
		data, err := os.ReadFile("../fixtures/" + b.file)
		if err != nil {
			t.Logf("skip %-22s (no image) — %s", b.file, b.note)
			continue
		}
		images[b.file] = data
		present = append(present, b)
	}
	if len(present) == 0 {
		t.Skip("no beverage fixtures present — add photos to backend/tests/fixtures/ (see file header)")
	}

	var ambN, ambFlag, ctrlN, ctrlFlag int
	forkVariants := map[string]int{} // fixture → max variant-count across its dishes
	for _, b := range present {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
		a, usage, err := c.AnalyzePhoto(ctx, images[b.file])
		cancel()
		if err != nil {
			t.Logf("%s: ERROR %v", b.file, err)
			continue
		}
		flagged := false
		maxV := 0
		var detail []string
		for _, d := range a.Dishes {
			if d.NeedsClarification {
				flagged = true
			}
			if len(d.Variants) > maxV {
				maxV = len(d.Variants)
			}
			vs := ""
			for _, v := range d.Variants {
				vs += fmt.Sprintf(" {%s:%dkcal}", v.Label, v.CaloriesKcal)
			}
			detail = append(detail, fmt.Sprintf("%q %dkcal conf=%.2f clarify=%v variants=[%s ] q=%q",
				d.Label, d.CaloriesKcal, d.Confidence, d.NeedsClarification, strings.TrimSpace(vs), d.ClarificationQuestion))
		}
		forkVariants[b.file] = maxV
		want := "ctrl"
		if b.ambiguous {
			want = "AMBIG"
			ambN++
			if flagged {
				ambFlag++
			}
		} else {
			ctrlN++
			if flagged {
				ctrlFlag++
			}
		}
		got := "no-flag"
		if flagged {
			got = "FLAG"
		}
		t.Logf("[%-22s want=%-5s got=%-7s] in=%d out=%d :: %s",
			b.file, want, got, usage.InputTokens, usage.OutputTokens, strings.Join(detail, " | "))
	}

	t.Logf("=== Phase 0 result (model=%s) ===", model)
	if ambN > 0 {
		t.Logf("flag RECALL (ambiguous flagged): %d/%d = %.0f%%", ambFlag, ambN, 100*float64(ambFlag)/float64(ambN))
	}
	if ctrlN > 0 {
		t.Logf("FALSE-flag (controls flagged):   %d/%d = %.0f%%", ctrlFlag, ctrlN, 100*float64(ctrlFlag)/float64(ctrlN))
	}
	t.Logf("read: high recall + low false-flag => build the variants pipeline; low recall => add an explicit beverage rule to the prompt + a deterministic label trigger, then re-run.")

	// Regression guard (Phase 5): the cola fixtures are the feature's core
	// case — they MUST fork into >=2 variants. Only asserts for fixtures that
	// actually ran (absent or errored ones are skipped above).
	for _, f := range []string{"bev_cola_regular.jpg", "bev_cola_zero.jpg"} {
		if n, ran := forkVariants[f]; ran && n < 2 {
			t.Errorf("regression: %s must emit >=2 variants (regular/diet fork), got %d", f, n)
		}
	}
}
