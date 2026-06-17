package scanproc

import (
	"testing"

	"smachnogo/pkg/models"
)

func dish(kcal int, p, f, c float64) models.Dish {
	return models.Dish{
		PortionG: 300,
		Nutrients: models.Nutrients{CaloriesKcal: kcal, ProteinG: p, FatG: f, CarbsG: c},
	}
}

// variant builds a DishVariant with the given macro block.
func variant(label string, kcal int, p, f, c float64) models.DishVariant {
	return models.DishVariant{
		Label:     label,
		Nutrients: models.Nutrients{CaloriesKcal: kcal, ProteinG: p, FatG: f, CarbsG: c},
	}
}

func TestPlausibilityGate(t *testing.T) {
	cases := []struct {
		name string
		a    models.PhotoAnalysis
		ok   bool
	}{
		{"not food passes", models.PhotoAnalysis{IsFood: false}, true},
		{"normal meal passes", models.PhotoAnalysis{IsFood: true, Dishes: []models.Dish{dish(500, 30, 20, 50)}}, true},
		{"food with no dishes fails", models.PhotoAnalysis{IsFood: true}, false},
		{"zero kcal pizza fails", models.PhotoAnalysis{IsFood: true, Dishes: []models.Dish{dish(0, 30, 20, 50)}}, false},
		{"macro-energy mismatch fails", models.PhotoAnalysis{IsFood: true, Dishes: []models.Dish{dish(1000, 2, 1, 3)}}, false},
		{"low-cal drink skips ratio check", models.PhotoAnalysis{IsFood: true, Dishes: []models.Dish{dish(40, 0.5, 0.1, 9)}}, true},
		// Bug #1: ethanol-bearing drinks fall below the lower bound but are
		// physically valid — the unexplained calories are ethanol.
		{"beer passes", models.PhotoAnalysis{IsFood: true, Dishes: []models.Dish{dish(150, 0, 0, 13)}}, true},
		{"wine passes", models.PhotoAnalysis{IsFood: true, Dishes: []models.Dish{dish(125, 0, 0, 4)}}, true},
		{"beer among real food passes", models.PhotoAnalysis{IsFood: true, Dishes: []models.Dish{dish(500, 30, 20, 50), dish(150, 0, 0, 13)}}, true},
		// A 200 kcal / 0-macro block is NOT a drink (no carbs) — still garbage.
		{"zero-macro non-drink fails", models.PhotoAnalysis{IsFood: true, Dishes: []models.Dish{dish(200, 0, 0, 0)}}, false},
		// A "drink" claiming far more unexplained energy than a serving of
		// ethanol can supply is garbage, not alcohol.
		{"huge unexplained remainder fails", models.PhotoAnalysis{IsFood: true, Dishes: []models.Dish{dish(1000, 0, 0, 3)}}, false},
		// Upper bound is always enforced, drink signature or not.
		{"upper-bound garbage fails", models.PhotoAnalysis{IsFood: true, Dishes: []models.Dish{dish(100, 40, 40, 40)}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issue := plausibilityIssue(&tc.a)
			if tc.ok && issue != "" {
				t.Fatalf("expected pass, got %q", issue)
			}
			if !tc.ok && issue == "" {
				t.Fatal("expected gate to fire")
			}
		})
	}
}

func TestPlausibilityNonPositivePortion(t *testing.T) {
	d := dish(500, 30, 20, 50)
	d.PortionG = 0
	a := models.PhotoAnalysis{IsFood: true, Dishes: []models.Dish{d}}
	if plausibilityIssue(&a) == "" {
		t.Fatal("zero portion must fail the gate")
	}
}

// Bug #2: non-default variant blocks must face the same macro-energy sanity as
// the headline. variants[0] is the headline (DefaultToFirstVariant copies it
// onto the dish), so the implausible block goes at index 1+.
func TestPlausibilityVariantSanity(t *testing.T) {
	// Impossible Diet variant: 200 kcal with zero macros (not a drink — no
	// carbs) must fail even though the headline regular form is fine.
	d := dish(200, 0, 0, 0)
	d.Variants = []models.DishVariant{
		variant("Regular", 200, 0, 0, 30), // headline: 200 kcal, 120 macro kcal, ratio 0.6 — ok
		variant("Diet", 200, 0, 0, 0),     // impossible: 200 kcal, 0 macros, 0 carbs
	}
	a := models.PhotoAnalysis{IsFood: true, Dishes: []models.Dish{d}}
	if plausibilityIssue(&a) == "" {
		t.Fatal("impossible Diet variant block must fail the gate")
	}

	// A normal 0-kcal Diet variant (alongside a regular headline) is naturally
	// exempt and must pass.
	d2 := dish(150, 0, 0, 39)
	d2.Variants = []models.DishVariant{
		variant("Regular", 150, 0, 0, 39), // headline ratio 1.04 — ok
		variant("Diet", 0, 0, 0, 0),       // 0 kcal — exempt
	}
	a2 := models.PhotoAnalysis{IsFood: true, Dishes: []models.Dish{d2}}
	if issue := plausibilityIssue(&a2); issue != "" {
		t.Fatalf("0-kcal Diet variant must pass, got %q", issue)
	}
}
