package summary

import (
	"testing"

	"smachnogo/pkg/models"
)

func meal(date string, state models.MealState, kcal int, protein float64, ns, dq int) models.Meal {
	return models.Meal{
		Date: date, State: state,
		Nutrients: models.Nutrients{CaloriesKcal: kcal, ProteinG: protein},
		Scores:    models.Scores{NutritionScore: ns, DietQualityScore: dq},
	}
}

func TestFoldDayCalorieWeightedAndPlannedExcluded(t *testing.T) {
	meals := []models.Meal{
		meal("2026-06-10", models.MealStateLogged, 300, 20, 90, 80),
		meal("2026-06-10", models.MealStateLogged, 100, 2, 30, 20),
		meal("2026-06-10", models.MealStatePlanned, 900, 50, 10, 10), // must not count
		meal("2026-06-11", models.MealStateLogged, 500, 30, 60, 70),
	}
	r := Fold(meals, GranularityDay)
	if len(r.Buckets) != 2 {
		t.Fatalf("buckets: %d", len(r.Buckets))
	}
	d0 := r.Buckets[0]
	if d0.Key != "2026-06-10" || d0.CaloriesKcal != 400 || d0.ProteinG != 22 || d0.MealCount != 2 {
		t.Fatalf("day0: %+v", d0)
	}
	// (90*300 + 30*100)/400 = 75 ; (80*300 + 20*100)/400 = 65
	if d0.NutritionScore != 75 || d0.DietQualityScore != 65 {
		t.Fatalf("weighted scores: %+v", d0.Scores)
	}
	if r.Totals.CaloriesKcal != 900 || r.Totals.MealCount != 3 || r.Totals.DaysLogged != 2 {
		t.Fatalf("totals: %+v", r.Totals)
	}
}

func TestFoldWeekISOMondayBuckets(t *testing.T) {
	// 2026-06-08 is a Monday; 2026-06-14 the Sunday of that week;
	// 2026-06-15 the next Monday. Sunday must NOT start a new week.
	meals := []models.Meal{
		meal("2026-06-08", models.MealStateLogged, 100, 0, 50, 50),
		meal("2026-06-14", models.MealStateLogged, 200, 0, 50, 50),
		meal("2026-06-15", models.MealStateLogged, 400, 0, 50, 50),
	}
	r := Fold(meals, GranularityWeek)
	if len(r.Buckets) != 2 {
		t.Fatalf("buckets: %+v", r.Buckets)
	}
	if r.Buckets[0].Key != "2026-06-08" || r.Buckets[0].CaloriesKcal != 300 {
		t.Fatalf("week1: %+v", r.Buckets[0])
	}
	if r.Buckets[1].Key != "2026-06-15" || r.Buckets[1].CaloriesKcal != 400 {
		t.Fatalf("week2: %+v", r.Buckets[1])
	}
	if r.Buckets[0].DaysLogged != 2 {
		t.Fatalf("week1 days: %d", r.Buckets[0].DaysLogged)
	}
}

func TestFoldWeekYearBoundary(t *testing.T) {
	// 2026-01-01 is a Thursday → its ISO week starts Monday 2025-12-29.
	meals := []models.Meal{
		meal("2025-12-29", models.MealStateLogged, 100, 0, 50, 50),
		meal("2026-01-01", models.MealStateLogged, 200, 0, 50, 50),
	}
	r := Fold(meals, GranularityWeek)
	if len(r.Buckets) != 1 || r.Buckets[0].Key != "2025-12-29" || r.Buckets[0].CaloriesKcal != 300 {
		t.Fatalf("year-boundary week: %+v", r.Buckets)
	}
}

func TestFoldMonthBoundary(t *testing.T) {
	meals := []models.Meal{
		meal("2026-05-31", models.MealStateLogged, 100, 0, 50, 50),
		meal("2026-06-01", models.MealStateLogged, 200, 0, 50, 50),
	}
	r := Fold(meals, GranularityMonth)
	if len(r.Buckets) != 2 || r.Buckets[0].Key != "2026-05" || r.Buckets[1].Key != "2026-06" {
		t.Fatalf("month buckets: %+v", r.Buckets)
	}
}

func TestFoldZeroKcalFallsBackToPlainMean(t *testing.T) {
	meals := []models.Meal{
		meal("2026-06-10", models.MealStateLogged, 0, 0, 80, 60),
		meal("2026-06-10", models.MealStateLogged, 0, 0, 40, 20),
	}
	r := Fold(meals, GranularityDay)
	if r.Buckets[0].NutritionScore != 60 || r.Buckets[0].DietQualityScore != 40 {
		t.Fatalf("plain-mean fallback: %+v", r.Buckets[0].Scores)
	}
}
