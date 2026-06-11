//go:build live

// Live adapter tests — run with the real Gemini API:
//
//	GEMINI_API_KEY=... go test -tags live ./integration/ -run TestGeminiLive -v
//
// Verifies the canonical schemas pass through responseJsonSchema on BOTH
// the text model (estimate path) and the vision model (photo path).
package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"smachnogo/pkg/llm/gemini"
)

func TestGeminiLiveTextEstimate(t *testing.T) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Skip("GEMINI_API_KEY not set")
	}
	c := gemini.New(key, "gemini-3-flash-preview", "gemini-3.1-flash-lite")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	est, usage, err := c.EstimateText(ctx, "2 eggs and toast with butter")
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	t.Logf("usage in=%d out=%d latency=%dms", usage.InputTokens, usage.OutputTokens, usage.LatencyMS)
	if !est.IsFood || len(est.Items) == 0 {
		t.Fatalf("expected food items, got %+v", est)
	}
	tot := est.Totals()
	t.Logf("label=%q items=%d totals: %d kcal P%.0f F%.0f C%.0f ns=%d dq=%d",
		est.Label, len(est.Items), tot.CaloriesKcal, tot.ProteinG, tot.FatG, tot.CarbsG,
		tot.NutritionScore, tot.DietQualityScore)
	if tot.CaloriesKcal < 150 || tot.CaloriesKcal > 700 {
		t.Errorf("implausible kcal for eggs+toast: %d", tot.CaloriesKcal)
	}
}

func TestGeminiLiveVision(t *testing.T) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Skip("GEMINI_API_KEY not set")
	}
	jpeg, err := os.ReadFile("../fixtures/single_plate.jpg")
	if err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	visionModel := os.Getenv("GEMINI_VISION_MODEL")
	if visionModel == "" {
		visionModel = "gemini-2.5-flash"
	}
	c := gemini.New(key, visionModel, "gemini-3.1-flash-lite")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	a, usage, err := c.AnalyzePhoto(ctx, jpeg)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	t.Logf("usage in=%d out=%d latency=%dms", usage.InputTokens, usage.OutputTokens, usage.LatencyMS)
	if !a.IsFood || len(a.Dishes) == 0 {
		t.Fatalf("expected dishes, got %+v", a)
	}
	for i, d := range a.Dishes {
		t.Logf("dish[%d] %q kcal=%d portion=%q conf=%.2f clarify=%v",
			i, d.Label, d.CaloriesKcal, d.PortionDesc, d.Confidence, d.NeedsClarification)
	}
}
