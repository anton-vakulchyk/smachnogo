package models

// Nutrients is the shared macro+micro block. Embedded in dishes, estimate
// items, meals and summary buckets so the wire shape stays identical
// everywhere. No omitempty on numerics — Swift Codable models are
// non-optional and the backend always emits zeros.
type Nutrients struct {
	CaloriesKcal  int     `json:"calories_kcal" dynamodbav:"calories_kcal"`
	ProteinG      float64 `json:"protein_g" dynamodbav:"protein_g"`
	FatG          float64 `json:"fat_g" dynamodbav:"fat_g"`
	CarbsG        float64 `json:"carbs_g" dynamodbav:"carbs_g"`
	FiberG        float64 `json:"fiber_g" dynamodbav:"fiber_g"`
	SugarG        float64 `json:"sugar_g" dynamodbav:"sugar_g"`
	SodiumMg      float64 `json:"sodium_mg" dynamodbav:"sodium_mg"`
	SaturatedFatG float64 `json:"saturated_fat_g" dynamodbav:"saturated_fat_g"`
	IronMg        float64 `json:"iron_mg" dynamodbav:"iron_mg"`
	CalciumMg     float64 `json:"calcium_mg" dynamodbav:"calcium_mg"`
	Omega3G       float64 `json:"omega3_g" dynamodbav:"omega3_g"`
}

// Scores are 0–100 dish-quality measures. They are NOT scaled by portion
// factor (they measure what the food is, not how much of it was eaten).
type Scores struct {
	NutritionScore   int `json:"nutrition_score" dynamodbav:"nutrition_score"`
	DietQualityScore int `json:"diet_quality_score" dynamodbav:"diet_quality_score"`
}

// Dish is one physically distinct plate/bowl/glass in a photo analysis.
// This struct IS the canonical contract — pkg/llm/schema generates the
// provider JSON schema to match it, and a golden round-trip test keeps
// the two in sync.
type Dish struct {
	Label       string `json:"label" dynamodbav:"label"`
	Description string `json:"description" dynamodbav:"description"`
	PortionDesc string `json:"portion_desc" dynamodbav:"portion_desc"`
	PortionG    int    `json:"portion_g" dynamodbav:"portion_g"`
	Nutrients
	Scores
	Confidence float64 `json:"confidence" dynamodbav:"confidence"`

	// Clarification trio: always present (portable structured-output subset
	// forbids optional members), empty when not needed.
	NeedsClarification    bool     `json:"needs_clarification" dynamodbav:"needs_clarification"`
	ClarificationQuestion string   `json:"clarification_question" dynamodbav:"clarification_question"`
	ClarificationOptions  []string `json:"clarification_options" dynamodbav:"clarification_options"`
}

type ImageQuality string

const (
	ImageQualityGood    ImageQuality = "good"
	ImageQualityBlurry  ImageQuality = "blurry"
	ImageQualityDark    ImageQuality = "dark"
	ImageQualityPartial ImageQuality = "partial"
)

// PhotoAnalysis is the vision model's full response for one scan.
type PhotoAnalysis struct {
	IsFood       bool         `json:"is_food" dynamodbav:"is_food"`
	ImageQuality ImageQuality `json:"image_quality" dynamodbav:"image_quality"`
	Dishes       []Dish       `json:"dishes" dynamodbav:"dishes"`
}

// EstimateItem is one food item in a free-text estimate.
type EstimateItem struct {
	Name         string `json:"name"`
	QuantityDesc string `json:"quantity_desc"`
	Nutrients
	Scores
	Confidence float64 `json:"confidence"`
}

// TextEstimate is the text model's response for POST /meals/estimate.
type TextEstimate struct {
	IsFood      bool           `json:"is_food"`
	Label       string         `json:"label"`
	Assumptions string         `json:"assumptions"`
	Items       []EstimateItem `json:"items"`
}

// EstimateTotals is computed in Go (never by the model): nutrient sums,
// calorie-weighted score means.
type EstimateTotals struct {
	Nutrients
	Scores
}

// Totals sums items; score means are calorie-weighted (plain mean when the
// calorie sum is zero).
func (t *TextEstimate) Totals() EstimateTotals {
	var out EstimateTotals
	var nsNum, dqNum, kcal float64
	for _, it := range t.Items {
		out.CaloriesKcal += it.CaloriesKcal
		out.ProteinG += it.ProteinG
		out.FatG += it.FatG
		out.CarbsG += it.CarbsG
		out.FiberG += it.FiberG
		out.SugarG += it.SugarG
		out.SodiumMg += it.SodiumMg
		out.SaturatedFatG += it.SaturatedFatG
		out.IronMg += it.IronMg
		out.CalciumMg += it.CalciumMg
		out.Omega3G += it.Omega3G
		nsNum += float64(it.NutritionScore) * float64(it.CaloriesKcal)
		dqNum += float64(it.DietQualityScore) * float64(it.CaloriesKcal)
		kcal += float64(it.CaloriesKcal)
	}
	if kcal > 0 {
		out.NutritionScore = int(nsNum/kcal + 0.5)
		out.DietQualityScore = int(dqNum/kcal + 0.5)
	} else if n := len(t.Items); n > 0 {
		var ns, dq int
		for _, it := range t.Items {
			ns += it.NutritionScore
			dq += it.DietQualityScore
		}
		out.NutritionScore = ns / n
		out.DietQualityScore = dq / n
	}
	return out
}

// Scale returns a copy of the dish with nutrients and portion scaled
// linearly by factor. Scores and identity fields are untouched. Always
// scale from the base (factor relative to 1.0), never compound.
func (d Dish) Scale(factor float64) Dish {
	if factor == 1.0 {
		return d
	}
	out := d
	out.PortionG = int(float64(d.PortionG)*factor + 0.5)
	out.CaloriesKcal = int(float64(d.CaloriesKcal)*factor + 0.5)
	out.ProteinG = round1(d.ProteinG * factor)
	out.FatG = round1(d.FatG * factor)
	out.CarbsG = round1(d.CarbsG * factor)
	out.FiberG = round1(d.FiberG * factor)
	out.SugarG = round1(d.SugarG * factor)
	out.SodiumMg = round1(d.SodiumMg * factor)
	out.SaturatedFatG = round1(d.SaturatedFatG * factor)
	out.IronMg = round1(d.IronMg * factor)
	out.CalciumMg = round1(d.CalciumMg * factor)
	out.Omega3G = round1(d.Omega3G * factor)
	return out
}

func round1(f float64) float64 {
	if f < 0 {
		return 0
	}
	return float64(int(f*10+0.5)) / 10
}

// Clamp normalizes model output in place: scores into [0,100], negative
// numerics to 0. Run before any plausibility check or persistence.
func (d *Dish) Clamp() {
	d.NutritionScore = clampScore(d.NutritionScore)
	d.DietQualityScore = clampScore(d.DietQualityScore)
	if d.Confidence < 0 {
		d.Confidence = 0
	}
	if d.Confidence > 1 {
		d.Confidence = 1
	}
	if d.PortionG < 0 {
		d.PortionG = 0
	}
	d.Nutrients.clampNegatives()
	if d.ClarificationOptions == nil {
		d.ClarificationOptions = []string{}
	}
}

// ClampForStorage zeroes negative inputs on externally-supplied nutrient
// blocks (manual meal entry) — same rule the analysis path applies.
func (n *Nutrients) ClampForStorage() { n.clampNegatives() }

func (n *Nutrients) clampNegatives() {
	if n.CaloriesKcal < 0 {
		n.CaloriesKcal = 0
	}
	for _, f := range []*float64{&n.ProteinG, &n.FatG, &n.CarbsG, &n.FiberG, &n.SugarG, &n.SodiumMg, &n.SaturatedFatG, &n.IronMg, &n.CalciumMg, &n.Omega3G} {
		if *f < 0 {
			*f = 0
		}
	}
}

func clampScore(s int) int {
	if s < 0 {
		return 0
	}
	if s > 100 {
		return 100
	}
	return s
}
