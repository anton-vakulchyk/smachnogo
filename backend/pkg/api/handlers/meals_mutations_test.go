package handlers

import (
	"testing"

	"smachnogo/pkg/models"
)

// baseNutrients must derive a fork meal's base from the CHOSEN variant, even
// while the scan still exists (its top-level dish is the Regular default).
// This is the C3 correctness fix: a portion edit on a Diet meal must not
// rescale from Regular, and switching the variant must re-base cleanly.
func TestBaseNutrientsPrefersChosenVariant(t *testing.T) {
	h := &Meals{} // Store + *http.Request are unused by the variant branch
	di := 0
	vi := 1 // Diet
	meal := &models.Meal{
		Nutrients:    models.Nutrients{CaloriesKcal: 1}, // stored = chosen Diet at ×1.0
		ScanID:       "11111111-1111-1111-1111-111111111111",
		DishIndex:    &di, // scan present — the variant must still win
		VariantIndex: &vi,
		Variants: []models.DishVariant{
			{Label: "Regular", Nutrients: models.Nutrients{CaloriesKcal: 140}},
			{Label: "Diet", Nutrients: models.Nutrients{CaloriesKcal: 1}},
		},
	}

	base, _, ok := h.baseNutrients(nil, "u", meal)
	if !ok || base.CaloriesKcal != 1 {
		t.Fatalf("base must be the chosen Diet variant (1 kcal), got ok=%v kcal=%d", ok, base.CaloriesKcal)
	}

	// Switching the index re-bases to Regular (140), with no scan read.
	*meal.VariantIndex = 0
	base, _, ok = h.baseNutrients(nil, "u", meal)
	if !ok || base.CaloriesKcal != 140 {
		t.Fatalf("base must follow VariantIndex to Regular (140), got ok=%v kcal=%d", ok, base.CaloriesKcal)
	}
}
