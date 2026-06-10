package models

import "testing"

func sampleDish() Dish {
	return Dish{
		Label: "Pizza", PortionG: 300,
		Nutrients: Nutrients{CaloriesKcal: 800, ProteinG: 30, FatG: 35, CarbsG: 88,
			FiberG: 5, SugarG: 8, SodiumMg: 1600, SaturatedFatG: 14, IronMg: 4, CalciumMg: 450, Omega3G: 0.3},
		Scores:     Scores{NutritionScore: 45, DietQualityScore: 38},
		Confidence: 0.9,
	}
}

func TestScaleLinearAndScoresUntouched(t *testing.T) {
	half := sampleDish().Scale(0.5)
	if half.CaloriesKcal != 400 || half.PortionG != 150 {
		t.Fatalf("kcal/portion: %d %d", half.CaloriesKcal, half.PortionG)
	}
	if half.ProteinG != 15 || half.SodiumMg != 800 {
		t.Fatalf("macros/micros: %v %v", half.ProteinG, half.SodiumMg)
	}
	if half.NutritionScore != 45 || half.DietQualityScore != 38 {
		t.Fatalf("scores must not scale: %+v", half.Scores)
	}
	// Scale is always from base — no compounding.
	threeQ := sampleDish().Scale(0.75)
	if threeQ.CaloriesKcal != 600 {
		t.Fatalf("3/4 from base: %d", threeQ.CaloriesKcal)
	}
}

func TestClamp(t *testing.T) {
	d := sampleDish()
	d.NutritionScore = 140
	d.DietQualityScore = -3
	d.ProteinG = -5
	d.Confidence = 1.7
	d.Clamp()
	if d.NutritionScore != 100 || d.DietQualityScore != 0 || d.ProteinG != 0 || d.Confidence != 1 {
		t.Fatalf("clamp: %+v", d)
	}
	if d.ClarificationOptions == nil {
		t.Fatal("options must never be nil (wire contract: empty array)")
	}
}

func TestEstimateTotalsCalorieWeighted(t *testing.T) {
	te := TextEstimate{IsFood: true, Items: []EstimateItem{
		{Nutrients: Nutrients{CaloriesKcal: 300, ProteinG: 20}, Scores: Scores{NutritionScore: 90, DietQualityScore: 80}},
		{Nutrients: Nutrients{CaloriesKcal: 100, ProteinG: 2}, Scores: Scores{NutritionScore: 30, DietQualityScore: 20}},
	}}
	tot := te.Totals()
	if tot.CaloriesKcal != 400 || tot.ProteinG != 22 {
		t.Fatalf("sums: %+v", tot.Nutrients)
	}
	// (90*300 + 30*100)/400 = 75 ; (80*300 + 20*100)/400 = 65
	if tot.NutritionScore != 75 || tot.DietQualityScore != 65 {
		t.Fatalf("weighted scores: %+v", tot.Scores)
	}
}
