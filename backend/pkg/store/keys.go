// Package store owns all DynamoDB access. Every PK/SK is built and parsed
// HERE and only here — SK-shape drift is a known silent-breakage class, so
// keys_test.go pins every format.
package store

import (
	"fmt"
	"strings"
)

const (
	pkUserPrefix  = "USER#"
	skProfile     = "PROFILE"
	skMealPrefix  = "MEAL#"
	skScanPrefix  = "SCAN#"
	skQuotaPrefix = "QUOTA#"
)

func UserPK(userID string) string { return pkUserPrefix + userID }

func ProfileSK() string { return skProfile }

func MealSK(date, mealID string) string { return skMealPrefix + date + "#" + mealID }

// MealSKRange returns the BETWEEN bounds covering all meals in [from, to]
// inclusive. ￿ sorts after any meal_id suffix.
func MealSKRange(from, to string) (lo, hi string) {
	return skMealPrefix + from, skMealPrefix + to + "￿"
}

func ScanSK(scanID string) string { return skScanPrefix + scanID }

func QuotaSK(date string) string { return skQuotaPrefix + date }

// ParseMealSK extracts (date, mealID). meal_id never contains '#'
// (validated at the API edge), so the split is unambiguous.
func ParseMealSK(sk string) (date, mealID string, err error) {
	rest, ok := strings.CutPrefix(sk, skMealPrefix)
	if !ok {
		return "", "", fmt.Errorf("not a meal SK: %q", sk)
	}
	date, mealID, ok = strings.Cut(rest, "#")
	if !ok || date == "" || mealID == "" {
		return "", "", fmt.Errorf("malformed meal SK: %q", sk)
	}
	return date, mealID, nil
}

func ParseScanSK(sk string) (scanID string, err error) {
	id, ok := strings.CutPrefix(sk, skScanPrefix)
	if !ok || id == "" {
		return "", fmt.Errorf("not a scan SK: %q", sk)
	}
	return id, nil
}

func ParseUserPK(pk string) (userID string, err error) {
	id, ok := strings.CutPrefix(pk, pkUserPrefix)
	if !ok || id == "" {
		return "", fmt.Errorf("not a user PK: %q", pk)
	}
	return id, nil
}
