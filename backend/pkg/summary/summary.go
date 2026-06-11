// Package summary folds meals into day/week/month buckets on read. Pure
// functions over []models.Meal — the upgrade path to pre-aggregated
// SUMMARY# items keeps this exact output contract.
package summary

import (
	"fmt"
	"time"

	"smachnogo/pkg/models"
)

type Granularity string

const (
	GranularityDay   Granularity = "day"
	GranularityWeek  Granularity = "week"
	GranularityMonth Granularity = "month"
)

// Caps per granularity (on-read fold cost bounds; week/month may take time
// per the owner, day ranges serve the diary).
func MaxRangeDays(g Granularity) int {
	switch g {
	case GranularityWeek:
		return 27 * 7
	case GranularityMonth:
		return 13 * 31
	default:
		return 92
	}
}

// Bucket is one aggregated period. Key: day "2026-06-10", week the Monday
// date "2026-06-08" (ISO Mon–Sun), month "2026-06". Scores are
// calorie-weighted means over logged meals.
type Bucket struct {
	Key string `json:"key"`
	models.Nutrients
	models.Scores
	MealCount  int `json:"meal_count"`
	DaysLogged int `json:"days_logged"`
}

type Result struct {
	Granularity Granularity `json:"granularity"`
	Buckets     []Bucket    `json:"buckets"`
	Totals      Bucket      `json:"totals"` // Key = "total"
}

type accumulator struct {
	n          models.Nutrients
	nsNum      float64
	dqNum      float64
	kcal       float64
	scoreCount int
	nsSum      int
	dqSum      int
	mealCount  int
	days       map[string]struct{}
}

func (a *accumulator) add(m *models.Meal) {
	a.n = addNutrients(a.n, m.Nutrients)
	a.nsNum += float64(m.NutritionScore) * float64(m.CaloriesKcal)
	a.dqNum += float64(m.DietQualityScore) * float64(m.CaloriesKcal)
	a.kcal += float64(m.CaloriesKcal)
	a.nsSum += m.NutritionScore
	a.dqSum += m.DietQualityScore
	a.scoreCount++
	a.mealCount++
	if a.days == nil {
		a.days = map[string]struct{}{}
	}
	a.days[m.Date] = struct{}{}
}

func (a *accumulator) bucket(key string) Bucket {
	b := Bucket{Key: key, Nutrients: a.n, MealCount: a.mealCount, DaysLogged: len(a.days)}
	if a.kcal > 0 {
		b.NutritionScore = int(a.nsNum/a.kcal + 0.5)
		b.DietQualityScore = int(a.dqNum/a.kcal + 0.5)
	} else if a.scoreCount > 0 {
		b.NutritionScore = a.nsSum / a.scoreCount
		b.DietQualityScore = a.dqSum / a.scoreCount
	}
	return b
}

// Fold aggregates LOGGED meals only (planned meals never count until
// eaten). Buckets are returned in chronological key order, only for
// periods that have data — clients treat missing buckets as empty.
func Fold(meals []models.Meal, g Granularity) Result {
	accs := map[string]*accumulator{}
	var order []string
	total := &accumulator{}

	for i := range meals {
		m := &meals[i]
		if m.State != models.MealStateLogged {
			continue
		}
		key, err := bucketKey(m.Date, g)
		if err != nil {
			continue // unparseable date — validated at write, defensive here
		}
		acc, ok := accs[key]
		if !ok {
			acc = &accumulator{}
			accs[key] = acc
			order = append(order, key)
		}
		acc.add(m)
		total.add(m)
	}

	// Input is SK-ordered (chronological), so first-seen key order is
	// already chronological.
	buckets := make([]Bucket, 0, len(order))
	for _, key := range order {
		buckets = append(buckets, accs[key].bucket(key))
	}
	return Result{Granularity: g, Buckets: buckets, Totals: total.bucket("total")}
}

// bucketKey maps a meal date to its period key.
func bucketKey(date string, g Granularity) (string, error) {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", err
	}
	switch g {
	case GranularityDay:
		return date, nil
	case GranularityWeek:
		// ISO Mon–Sun: key is that week's Monday.
		wd := int(t.Weekday()) // Sunday=0
		offset := (wd + 6) % 7 // days since Monday
		return t.AddDate(0, 0, -offset).Format("2006-01-02"), nil
	case GranularityMonth:
		return t.Format("2006-01"), nil
	default:
		return "", fmt.Errorf("unknown granularity %q", g)
	}
}

func addNutrients(a, b models.Nutrients) models.Nutrients {
	return models.Nutrients{
		CaloriesKcal:  a.CaloriesKcal + b.CaloriesKcal,
		ProteinG:      a.ProteinG + b.ProteinG,
		FatG:          a.FatG + b.FatG,
		CarbsG:        a.CarbsG + b.CarbsG,
		FiberG:        a.FiberG + b.FiberG,
		SugarG:        a.SugarG + b.SugarG,
		SodiumMg:      a.SodiumMg + b.SodiumMg,
		SaturatedFatG: a.SaturatedFatG + b.SaturatedFatG,
		IronMg:        a.IronMg + b.IronMg,
		CalciumMg:     a.CalciumMg + b.CalciumMg,
		Omega3G:       a.Omega3G + b.Omega3G,
	}
}
