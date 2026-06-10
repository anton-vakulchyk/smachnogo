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
