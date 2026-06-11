package store

import "testing"

// These tests pin the key formats. A failing test here means a data
// migration, not a refactor — change with extreme intent.
func TestKeyFormats(t *testing.T) {
	if got := UserPK("u1"); got != "USER#u1" {
		t.Fatalf("UserPK = %q", got)
	}
	if got := ProfileSK(); got != "PROFILE" {
		t.Fatalf("ProfileSK = %q", got)
	}
	if got := MealSK("2026-06-10", "abc-0"); got != "MEAL#2026-06-10#abc-0" {
		t.Fatalf("MealSK = %q", got)
	}
	if got := ScanSK("abc"); got != "SCAN#abc" {
		t.Fatalf("ScanSK = %q", got)
	}
	if got := QuotaSK("2026-06-10"); got != "QUOTA#2026-06-10" {
		t.Fatalf("QuotaSK = %q", got)
	}
	if got := TxnPK("2000000123456789"); got != "TXN#2000000123456789" {
		t.Fatalf("TxnPK = %q", got)
	}
	if got := NotifPK("uuid-1"); got != "NOTIF#uuid-1" {
		t.Fatalf("NotifPK = %q", got)
	}
	if got := MetaSK(); got != "META" {
		t.Fatalf("MetaSK = %q", got)
	}
}

func TestMealSKRoundTrip(t *testing.T) {
	sk := MealSK("2026-06-10", "550e8400-e29b-41d4-a716-446655440000-3")
	date, id, err := ParseMealSK(sk)
	if err != nil {
		t.Fatal(err)
	}
	if date != "2026-06-10" || id != "550e8400-e29b-41d4-a716-446655440000-3" {
		t.Fatalf("round trip: %q %q", date, id)
	}
	if _, _, err := ParseMealSK("SCAN#x"); err == nil {
		t.Fatal("expected error for non-meal SK")
	}
	if _, _, err := ParseMealSK("MEAL#nodate"); err == nil {
		t.Fatal("expected error for malformed meal SK")
	}
}

func TestMealSKRangeOrdering(t *testing.T) {
	lo, hi := MealSKRange("2026-06-01", "2026-06-10")
	inside := MealSK("2026-06-10", "zzz")
	outside := MealSK("2026-06-11", "aaa")
	if !(lo <= inside && inside <= hi) {
		t.Fatalf("inside key escapes range: %q not in [%q,%q]", inside, lo, hi)
	}
	if outside <= hi {
		t.Fatalf("outside key inside range: %q <= %q", outside, hi)
	}
}

func TestParseScanAndUser(t *testing.T) {
	if id, err := ParseScanSK("SCAN#s1"); err != nil || id != "s1" {
		t.Fatalf("ParseScanSK: %v %q", err, id)
	}
	if id, err := ParseUserPK("USER#u9"); err != nil || id != "u9" {
		t.Fatalf("ParseUserPK: %v %q", err, id)
	}
}
