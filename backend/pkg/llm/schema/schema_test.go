package schema

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"smachnogo/pkg/models"
)

// jsonFields returns the JSON keys a struct (with embedded structs
// flattened) marshals to.
func jsonFields(t *testing.T, typ reflect.Type) []string {
	t.Helper()
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Anonymous {
			out = append(out, jsonFields(t, f.Type)...)
			continue
		}
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		out = append(out, tag)
	}
	return out
}

// TestDishSchemaMatchesStruct is the sync guard: every schema property must
// exist on models.Dish and vice versa. A failure here means the canonical
// contract drifted — fix BOTH sides deliberately.
func TestDishSchemaMatchesStruct(t *testing.T) {
	props := Dish()["properties"].(map[string]any)
	var schemaKeys []string
	for k := range props {
		schemaKeys = append(schemaKeys, k)
	}
	structKeys := jsonFields(t, reflect.TypeOf(models.Dish{}))

	sort.Strings(schemaKeys)
	sort.Strings(structKeys)
	if !reflect.DeepEqual(schemaKeys, structKeys) {
		t.Fatalf("schema/struct drift:\nschema: %v\nstruct: %v", schemaKeys, structKeys)
	}

	required := Dish()["required"].([]string)
	sort.Strings(required)
	if !reflect.DeepEqual(required, schemaKeys) {
		t.Fatalf("every property must be required (portable subset):\nrequired: %v\nprops:    %v", required, schemaKeys)
	}
}

// TestVariantSchemaMatchesStruct guards the DishVariant contract the same way
// TestDishSchemaMatchesStruct guards Dish — the variant item schema and
// models.DishVariant must not drift.
func TestVariantSchemaMatchesStruct(t *testing.T) {
	props := variantProperties()
	var schemaKeys []string
	for k := range props {
		schemaKeys = append(schemaKeys, k)
	}
	structKeys := jsonFields(t, reflect.TypeOf(models.DishVariant{}))

	sort.Strings(schemaKeys)
	sort.Strings(structKeys)
	if !reflect.DeepEqual(schemaKeys, structKeys) {
		t.Fatalf("variant schema/struct drift:\nschema: %v\nstruct: %v", schemaKeys, structKeys)
	}

	req := append([]string{}, variantRequired...)
	sort.Strings(req)
	if !reflect.DeepEqual(req, schemaKeys) {
		t.Fatalf("every variant property must be required:\nrequired: %v\nprops:    %v", req, schemaKeys)
	}
}

func TestSchemasMarshalMinified(t *testing.T) {
	for name, s := range map[string]map[string]any{
		"photo": PhotoAnalysis(), "dish": Dish(), "text": TextEstimate(),
	} {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(string(b), "\n") || strings.Contains(string(b), "  ") {
			t.Fatalf("%s schema not minified", name)
		}
	}
}

// frozenV1 is the frozen result_version=1 fixture: future struct versions
// must still decode it without silently zero-filling load-bearing fields.
const frozenV1 = `{
  "is_food": true,
  "image_quality": "good",
  "dishes": [{
    "label": "Borscht",
    "description": "Beet soup with sour cream, assumed beef stock.",
    "portion_desc": "1 bowl (~350 g)",
    "portion_g": 350,
    "calories_kcal": 220,
    "protein_g": 9.5,
    "fat_g": 8.2,
    "carbs_g": 26.4,
    "fiber_g": 4.8,
    "sugar_g": 9.1,
    "sodium_mg": 740,
    "saturated_fat_g": 2.9,
    "iron_mg": 2.4,
    "calcium_mg": 95,
    "omega3_g": 0.1,
    "nutrition_score": 78,
    "diet_quality_score": 81,
    "confidence": 0.86,
    "needs_clarification": false,
    "clarification_question": "",
    "clarification_options": []
  }]
}`

func TestFrozenV1RoundTrip(t *testing.T) {
	var a models.PhotoAnalysis
	if err := json.Unmarshal([]byte(frozenV1), &a); err != nil {
		t.Fatal(err)
	}
	if !a.IsFood || len(a.Dishes) != 1 {
		t.Fatalf("decode shape: %+v", a)
	}
	d := a.Dishes[0]
	if d.CaloriesKcal != 220 || d.SodiumMg != 740 || d.NutritionScore != 78 || d.Confidence != 0.86 {
		t.Fatalf("v1 fields zero-filled: %+v", d)
	}

	// Round trip must preserve every key (no omitempty on numerics).
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var orig, rt map[string]any
	_ = json.Unmarshal([]byte(frozenV1), &orig)
	_ = json.Unmarshal(b, &rt)
	origDish := orig["dishes"].([]any)[0].(map[string]any)
	rtDish := rt["dishes"].([]any)[0].(map[string]any)
	for k := range origDish {
		if _, ok := rtDish[k]; !ok {
			t.Fatalf("key %q lost in round trip", k)
		}
	}
}
