package middleware

import (
	"fmt"
	"regexp"
	"time"
)

// IDs flow into S3 keys and DynamoDB sort keys — traversal and
// `#`-delimiter injection are real, hence regex-strict validation at the
// edge and nowhere else.
var (
	uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	// Scan-meal IDs are `{scan_uuid}-{dish_index}` — a plain-UUID rule
	// would 400 every scanned meal's PATCH/DELETE.
	mealIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}(-[0-7])?$`)
	dateRe   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

func ValidateUUID(id string) error {
	if !uuidRe.MatchString(id) {
		return fmt.Errorf("must be a UUID")
	}
	return nil
}

func ValidateMealID(id string) error {
	if !mealIDRe.MatchString(id) {
		return fmt.Errorf("must be a UUID with optional -<dish index> suffix")
	}
	return nil
}

// ValidateDate enforces YYYY-MM-DD, real calendar date, within ±1 year of
// now (the server treats the VALUE as opaque — no timezone logic — but the
// SK namespace must not be pollutable).
func ValidateDate(date string) error {
	if !dateRe.MatchString(date) {
		return fmt.Errorf("must be YYYY-MM-DD")
	}
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return fmt.Errorf("not a calendar date")
	}
	now := time.Now().UTC()
	if t.Before(now.AddDate(-1, 0, -1)) || t.After(now.AddDate(1, 0, 1)) {
		return fmt.Errorf("outside the accepted ±1 year window")
	}
	return nil
}

const MaxEstimateTextLen = 500
