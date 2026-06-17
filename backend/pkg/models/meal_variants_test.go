package models

import (
	"encoding/json"
	"strings"
	"testing"
)

// A confirmed fork-meal must carry its variants + selected index over the wire
// (Phase 4 iOS reads them for the post-save picker), and must round-trip.
func TestMealVariantsRoundTrip(t *testing.T) {
	vi := 1
	m := Meal{
		MealID:       "abc-0",
		Nutrients:    Nutrients{CaloriesKcal: 1}, // headline = the chosen (Diet) variant
		VariantIndex: &vi,
		Variants: []DishVariant{
			{Label: "Regular", Nutrients: Nutrients{CaloriesKcal: 140}},
			{Label: "Diet", Nutrients: Nutrients{CaloriesKcal: 1}},
		},
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"variant_index":1`) || !strings.Contains(s, `"label":"Diet"`) {
		t.Fatalf("meal variants not serialized: %s", s)
	}

	var back Meal
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.VariantIndex == nil || *back.VariantIndex != 1 || len(back.Variants) != 2 {
		t.Fatalf("meal variants not decoded: %+v", back)
	}
}

// A non-fork meal must omit both keys entirely — no empty arrays or null
// indexes leaking onto every ordinary diary entry.
func TestMealWithoutVariantsStaysClean(t *testing.T) {
	b, _ := json.Marshal(Meal{MealID: "x"})
	if strings.Contains(string(b), "variant") {
		t.Fatalf("non-fork meal leaked variant keys: %s", b)
	}
}
