// Package devicecheck guards the free allowance against reinstall/erase
// abuse via Apple's per-device two-bit store (bit 0 = "allowance granted",
// survives reinstall AND device erase).
//
// The real implementation needs an Apple Developer .p8 key (ES256 JWT to
// api.devicecheck.apple.com) — pending; see docs/appstore-readiness.md.
// Until then Disabled{} fails open: availability beats abuse-protection,
// and DeviceCheck errors must NEVER block a legitimate first scan.
package devicecheck

import "context"

// Checker is consulted once per user — at free-allowance grant (first scan).
type Checker interface {
	// CheckAndSet reports whether this physical device was already granted
	// a free allowance, marking it as granted when it wasn't. deviceToken
	// is the client's DCDevice token (empty on simulators/old devices —
	// treat as not-used; fail open).
	CheckAndSet(ctx context.Context, deviceToken string) (alreadyUsed bool, err error)
}

// Disabled is the stand-in until the .p8 key exists, and the fallback when
// DEVICECHECK_ENABLED=false.
type Disabled struct{}

func (Disabled) CheckAndSet(context.Context, string) (bool, error) { return false, nil }
