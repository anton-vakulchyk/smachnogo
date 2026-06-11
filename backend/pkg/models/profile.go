package models

import "time"

// Entitlement is the user's billing state, maintained by the App Store
// webhook + receipt endpoints (M7.2). Absent on the profile item = free.
type Entitlement string

const (
	EntitlementFree         Entitlement = "free"
	EntitlementTrialing     Entitlement = "trialing"
	EntitlementActive       Entitlement = "active"
	EntitlementGrace        Entitlement = "grace"
	EntitlementBillingRetry Entitlement = "billing_retry"
	EntitlementExpired      Entitlement = "expired"
	EntitlementRevoked      Entitlement = "revoked"
)

// Subscribed reports whether this state permits photo scanning beyond the
// free allowance. Grace and billing-retry stay entitled — a card hiccup must
// not hard-lock a paying user (App Store Billing Grace Period semantics).
func (e Entitlement) Subscribed() bool {
	switch e {
	case EntitlementTrialing, EntitlementActive, EntitlementGrace, EntitlementBillingRetry:
		return true
	}
	return false
}

// Paywall reasons carried in the 402 body — they drive distinct paywall copy
// (window_expired must acknowledge the returning user; a generic "out of
// scans" reads as a lie to someone with visibly unused scans).
const (
	PaywallScansExhausted    = "scans_exhausted"
	PaywallWindowExpired     = "window_expired"
	PaywallDeviceAlreadyUsed = "device_already_used" // reserved for DeviceCheck
)

// Profile is the USER#<id> / PROFILE item.
type Profile struct {
	Entitlement           Entitlement `dynamodbav:"entitlement,omitempty"`
	FreeScansUsed         int         `dynamodbav:"free_scans_used,omitempty"`
	AllowanceStartedAt    int64       `dynamodbav:"allowance_started_at,omitempty"` // epoch seconds; set on first scan
	EntitlementUpdatedAt  int64       `dynamodbav:"entitlement_updated_at,omitempty"` // epoch millis (Apple signedDate) — webhook ordering authority
	OriginalTransactionID string      `dynamodbav:"original_transaction_id,omitempty"`
	AppleSub              string      `dynamodbav:"apple_sub,omitempty"` // Sign-in-with-Apple linkage (M8)
	// Limits are user-set daily caps keyed by summary field name (M9).
	// Enforcement is zero: coloring is pure client-side mapping over the
	// summary buckets; the server only persists and validates keys.
	Limits    map[string]float64 `dynamodbav:"limits,omitempty"`
	CreatedAt int64              `dynamodbav:"created_at,omitempty"` // epoch seconds
}

// LimitableFields are the summary fields a daily cap may target — exactly
// the nutrient field names the summary buckets serve, so the client's
// status mapping is mechanical.
var LimitableFields = map[string]bool{
	"calories_kcal":   true,
	"protein_g":       true,
	"fat_g":           true,
	"carbs_g":         true,
	"fiber_g":         true,
	"sugar_g":         true,
	"sodium_mg":       true,
	"saturated_fat_g": true,
	"iron_mg":         true,
	"calcium_mg":      true,
	"omega3_g":        true,
}

// Ent returns the effective entitlement (absent attribute = free).
func (p *Profile) Ent() Entitlement {
	if p == nil || p.Entitlement == "" {
		return EntitlementFree
	}
	return p.Entitlement
}

// FreeAllowance computes the free-tier scan allowance state: scans remaining
// and, when zero, the paywall reason. The window check precedes the count
// check — an expired window wins even with unused scans. Pure function; the
// authoritative enforcement is the conditional write in ConsumeFreeScan,
// which mirrors exactly this logic.
func (p *Profile) FreeAllowance(allowance int, window time.Duration, now time.Time) (remaining int, reason string) {
	if p != nil && p.AllowanceStartedAt > 0 && now.After(time.Unix(p.AllowanceStartedAt, 0).Add(window)) {
		return 0, PaywallWindowExpired
	}
	used := 0
	if p != nil {
		used = p.FreeScansUsed
	}
	if used >= allowance {
		return 0, PaywallScansExhausted
	}
	return allowance - used, ""
}

// AllowanceEndsAt returns when the free window closes, or zero time when the
// allowance hasn't started (first scan starts the clock).
func (p *Profile) AllowanceEndsAt(window time.Duration) time.Time {
	if p == nil || p.AllowanceStartedAt == 0 {
		return time.Time{}
	}
	return time.Unix(p.AllowanceStartedAt, 0).Add(window)
}
