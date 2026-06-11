package models

import (
	"testing"
	"time"
)

func TestFreeAllowance(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour

	cases := []struct {
		name       string
		p          *Profile
		wantRemain int
		wantReason string
	}{
		{"fresh user", &Profile{}, 10, ""},
		{"nil profile", nil, 10, ""},
		{"partial use", &Profile{FreeScansUsed: 4, AllowanceStartedAt: now.Add(-24 * time.Hour).Unix()}, 6, ""},
		{"exhausted", &Profile{FreeScansUsed: 10, AllowanceStartedAt: now.Add(-24 * time.Hour).Unix()}, 0, PaywallScansExhausted},
		{"over-exhausted", &Profile{FreeScansUsed: 12, AllowanceStartedAt: now.Add(-24 * time.Hour).Unix()}, 0, PaywallScansExhausted},
		{"window expired", &Profile{FreeScansUsed: 3, AllowanceStartedAt: now.Add(-8 * 24 * time.Hour).Unix()}, 0, PaywallWindowExpired},
		// Window precedence: expired window wins even when scans are also
		// exhausted — the paywall copy must acknowledge the returning user.
		{"window expired and exhausted", &Profile{FreeScansUsed: 10, AllowanceStartedAt: now.Add(-30 * 24 * time.Hour).Unix()}, 0, PaywallWindowExpired},
		{"window edge: just inside", &Profile{FreeScansUsed: 9, AllowanceStartedAt: now.Add(-week).Add(time.Minute).Unix()}, 1, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			remain, reason := tc.p.FreeAllowance(10, week, now)
			if remain != tc.wantRemain || reason != tc.wantReason {
				t.Fatalf("got (%d, %q), want (%d, %q)", remain, reason, tc.wantRemain, tc.wantReason)
			}
		})
	}
}

func TestEntitlementSubscribed(t *testing.T) {
	subscribed := []Entitlement{EntitlementTrialing, EntitlementActive, EntitlementGrace, EntitlementBillingRetry}
	for _, e := range subscribed {
		if !e.Subscribed() {
			t.Errorf("%s must be subscribed", e)
		}
	}
	notSubscribed := []Entitlement{EntitlementFree, EntitlementExpired, EntitlementRevoked, ""}
	for _, e := range notSubscribed {
		if e.Subscribed() {
			t.Errorf("%s must NOT be subscribed", e)
		}
	}
}

func TestProfileEnt(t *testing.T) {
	var nilP *Profile
	if nilP.Ent() != EntitlementFree {
		t.Fatal("nil profile must be free")
	}
	if (&Profile{}).Ent() != EntitlementFree {
		t.Fatal("empty entitlement must be free")
	}
	if (&Profile{Entitlement: EntitlementActive}).Ent() != EntitlementActive {
		t.Fatal("explicit entitlement must pass through")
	}
}

func TestAllowanceEndsAt(t *testing.T) {
	week := 7 * 24 * time.Hour
	if !(&Profile{}).AllowanceEndsAt(week).IsZero() {
		t.Fatal("unstarted allowance has no end")
	}
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	got := (&Profile{AllowanceStartedAt: start.Unix()}).AllowanceEndsAt(week)
	if !got.Equal(start.Add(week)) {
		t.Fatalf("got %v, want %v", got, start.Add(week))
	}
}
