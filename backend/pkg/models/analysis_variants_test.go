package models

import "testing"

// Clamp must normalize each variant block (scores into [0,100], negatives to
// 0) and guarantee a non-nil Variants slice, exactly like the headline fields.
func TestClampNormalizesVariants(t *testing.T) {
	d := Dish{
		Nutrients: Nutrients{CaloriesKcal: 140},
		Variants: []DishVariant{
			{Label: "Regular", Nutrients: Nutrients{CaloriesKcal: 140, SugarG: 35}, Scores: Scores{NutritionScore: 20, DietQualityScore: 10}},
			{Label: "Zero", Nutrients: Nutrients{CaloriesKcal: 0, SugarG: -2}, Scores: Scores{NutritionScore: 150, DietQualityScore: -5}},
		},
	}
	d.Clamp()
	if d.Variants[1].SugarG != 0 {
		t.Fatalf("variant negative not clamped: got %v", d.Variants[1].SugarG)
	}
	if d.Variants[1].NutritionScore != 100 || d.Variants[1].DietQualityScore != 0 {
		t.Fatalf("variant scores not clamped: ns=%d dq=%d", d.Variants[1].NutritionScore, d.Variants[1].DietQualityScore)
	}
}

func TestClampForcesNonNilVariants(t *testing.T) {
	d := Dish{Nutrients: Nutrients{CaloriesKcal: 100}}
	d.Clamp()
	if d.Variants == nil {
		t.Fatal("Clamp must force Variants to a non-nil empty slice")
	}
}

// DefaultToFirstVariant copies variants[0] (the most-caloric default) into the
// headline so the shown estimate and the default pick never disagree.
func TestDefaultToFirstVariant(t *testing.T) {
	d := Dish{
		Nutrients: Nutrients{CaloriesKcal: 999}, // stale headline the model may have left
		Scores:    Scores{NutritionScore: 1},
		Variants: []DishVariant{
			{Label: "Regular", Nutrients: Nutrients{CaloriesKcal: 140, SugarG: 35}, Scores: Scores{NutritionScore: 22, DietQualityScore: 11}},
			{Label: "Zero", Nutrients: Nutrients{CaloriesKcal: 1}},
		},
	}
	d.DefaultToFirstVariant()
	if d.CaloriesKcal != 140 || d.SugarG != 35 || d.NutritionScore != 22 {
		t.Fatalf("headline not synced to variants[0]: kcal=%d sugar=%v ns=%d", d.CaloriesKcal, d.SugarG, d.NutritionScore)
	}
}

func TestDefaultToFirstVariantNoOpWhenEmpty(t *testing.T) {
	d := Dish{Nutrients: Nutrients{CaloriesKcal: 300}}
	d.DefaultToFirstVariant()
	if d.CaloriesKcal != 300 {
		t.Fatalf("no-variant dish must be untouched, got %d", d.CaloriesKcal)
	}
}
