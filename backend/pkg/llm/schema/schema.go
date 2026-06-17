// Package schema holds the canonical JSON Schemas for every LLM call.
// Constraints (the portable subset shared by Anthropic structured outputs,
// Gemini responseSchema and OpenAI structured outputs):
//   - additionalProperties:false on every object
//   - every property listed in required (optionality = empty value)
//   - no numeric min/max, no anyOf/null unions — bounds live in descriptions
//     and are clamped server-side
//
// Serialization is always minified (json.Marshal, never MarshalIndent) —
// schema bytes are input tokens on every call.
package schema

// dishProperties is shared by the photo schema (array items) and the refine
// schema (single dish). It must stay field-for-field in sync with
// models.Dish — the golden round-trip test enforces that.
func dishProperties() map[string]any {
	return map[string]any{
		"label":           map[string]any{"type": "string", "description": "short dish name, <=5 words"},
		"description":     map[string]any{"type": "string", "description": "ONE short sentence: visible components, preparation, and any assumption made"},
		"portion_desc":    map[string]any{"type": "string", "description": "human-readable portion, e.g. '1 bowl (~350 g)' or 'whole pot, ~4 servings'"},
		"portion_g":       map[string]any{"type": "integer", "description": "estimated edible weight in grams as currently visible"},
		"calories_kcal":   map[string]any{"type": "integer", "description": "kcal for THIS visible portion"},
		"protein_g":       map[string]any{"type": "number"},
		"fat_g":           map[string]any{"type": "number"},
		"carbs_g":         map[string]any{"type": "number"},
		"fiber_g":         map[string]any{"type": "number"},
		"sugar_g":         map[string]any{"type": "number", "description": "total sugars in grams"},
		"sodium_mg":       map[string]any{"type": "number"},
		"saturated_fat_g": map[string]any{"type": "number"},
		"iron_mg":         map[string]any{"type": "number"},
		"calcium_mg":      map[string]any{"type": "number"},
		"omega3_g":        map[string]any{"type": "number", "description": "EPA+DHA+ALA combined, rough"},
		"nutrition_score": map[string]any{"type": "integer", "description": "0-100 nutrient density of the dish"},
		"diet_quality_score": map[string]any{"type": "integer",
			"description": "0-100 fit with a healthy diet pattern (processing level, added sugar, refined carbs, sodium); must be consistent with the reported nutrient fields"},
		"confidence": map[string]any{"type": "number", "description": "0.0-1.0 confidence in identification AND portion"},
		"needs_clarification": map[string]any{"type": "boolean",
			"description": "true ONLY if plausible contents differ by more than ~25% calories (opaque drinks, hidden fillings, unreadable packaging); never for obvious foods"},
		"clarification_question": map[string]any{"type": "string", "description": "one short question; empty string when not needed"},
		"clarification_options": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
			"description": "3-4 short tappable answers; empty array when not needed"},
		"variants": map[string]any{
			"type":        "array",
			"description": "closed look-alike forks the photo can't resolve (regular vs diet/zero soda, sweetened vs unsweetened tea, whole vs skim milk): 2-3 {label, all nutrient fields, both scores} objects MOST-CALORIC FIRST with variants[0] equal to this dish; empty array otherwise; never for portion size or open-ended hidden contents",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             variantRequired,
				"properties":           variantProperties(),
			},
		},
	}
}

// variantProperties is the schema for one DishVariant: a label plus the same
// nutrient + score fields a dish carries. Kept field-for-field in sync with
// models.DishVariant by TestVariantSchemaMatchesStruct.
func variantProperties() map[string]any {
	return map[string]any{
		"label":              map[string]any{"type": "string", "description": "name of this form, e.g. 'Regular' or 'Diet / Zero'"},
		"calories_kcal":      map[string]any{"type": "integer"},
		"protein_g":          map[string]any{"type": "number"},
		"fat_g":              map[string]any{"type": "number"},
		"carbs_g":            map[string]any{"type": "number"},
		"fiber_g":            map[string]any{"type": "number"},
		"sugar_g":            map[string]any{"type": "number"},
		"sodium_mg":          map[string]any{"type": "number"},
		"saturated_fat_g":    map[string]any{"type": "number"},
		"iron_mg":            map[string]any{"type": "number"},
		"calcium_mg":         map[string]any{"type": "number"},
		"omega3_g":           map[string]any{"type": "number"},
		"nutrition_score":    map[string]any{"type": "integer", "description": "0-100"},
		"diet_quality_score": map[string]any{"type": "integer", "description": "0-100"},
	}
}

var variantRequired = []string{
	"label", "calories_kcal", "protein_g", "fat_g", "carbs_g",
	"fiber_g", "sugar_g", "sodium_mg", "saturated_fat_g", "iron_mg", "calcium_mg", "omega3_g",
	"nutrition_score", "diet_quality_score",
}

var dishRequired = []string{
	"label", "description", "portion_desc", "portion_g",
	"calories_kcal", "protein_g", "fat_g", "carbs_g",
	"fiber_g", "sugar_g", "sodium_mg", "saturated_fat_g", "iron_mg", "calcium_mg", "omega3_g",
	"nutrition_score", "diet_quality_score", "confidence",
	"needs_clarification", "clarification_question", "clarification_options", "variants",
}

// PhotoAnalysis returns the vision-call schema.
func PhotoAnalysis() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"is_food", "image_quality", "dishes"},
		"properties": map[string]any{
			"is_food":       map[string]any{"type": "boolean", "description": "true if the image contains edible food or drink"},
			"image_quality": map[string]any{"type": "string", "enum": []string{"good", "blurry", "dark", "partial"}, "description": "partial = dishes visibly cut off at frame edge"},
			"dishes": map[string]any{
				"type":        "array",
				"description": "one entry per physically distinct plate/bowl/glass of food or drink; empty when is_food is false",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             dishRequired,
					"properties":           dishProperties(),
				},
			},
		},
	}
}

// Dish returns the single-dish schema used by RefineDish.
func Dish() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             dishRequired,
		"properties":           dishProperties(),
	}
}

// TextEstimate returns the free-text estimate schema.
func TextEstimate() map[string]any {
	itemProps := map[string]any{
		"name":            map[string]any{"type": "string"},
		"quantity_desc":   map[string]any{"type": "string", "description": "e.g. '2 large eggs'"},
		"calories_kcal":   map[string]any{"type": "integer"},
		"protein_g":       map[string]any{"type": "number"},
		"fat_g":           map[string]any{"type": "number"},
		"carbs_g":         map[string]any{"type": "number"},
		"fiber_g":         map[string]any{"type": "number"},
		"sugar_g":         map[string]any{"type": "number"},
		"sodium_mg":       map[string]any{"type": "number"},
		"saturated_fat_g": map[string]any{"type": "number"},
		"iron_mg":         map[string]any{"type": "number"},
		"calcium_mg":      map[string]any{"type": "number"},
		"omega3_g":        map[string]any{"type": "number"},
		"nutrition_score": map[string]any{"type": "integer", "description": "0-100"},
		"diet_quality_score": map[string]any{"type": "integer", "description": "0-100"},
		"confidence":      map[string]any{"type": "number", "description": "0.0-1.0"},
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"is_food", "label", "assumptions", "items"},
		"properties": map[string]any{
			"is_food":     map[string]any{"type": "boolean", "description": "false if the text does not describe food or drink"},
			"label":       map[string]any{"type": "string", "description": "short title for the whole entry, e.g. 'Eggs & toast'"},
			"assumptions": map[string]any{"type": "string", "description": "one sentence on assumed portion sizes / preparation; shown to the user"},
			"items": map[string]any{
				"type":        "array",
				"description": "one entry per food item mentioned; empty when is_food is false",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required": []string{"name", "quantity_desc", "calories_kcal", "protein_g", "fat_g", "carbs_g",
						"fiber_g", "sugar_g", "sodium_mg", "saturated_fat_g", "iron_mg", "calcium_mg", "omega3_g",
						"nutrition_score", "diet_quality_score", "confidence"},
					"properties": itemProps,
				},
			},
		},
	}
}
