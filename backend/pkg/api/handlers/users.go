package handlers

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"go.uber.org/zap"

	"smachnogo/pkg/api/middleware"
	"smachnogo/pkg/awsx"
	"smachnogo/pkg/config"
	"smachnogo/pkg/logging"
	"smachnogo/pkg/models"
	"smachnogo/pkg/store"
)

// Users: account deletion (App Store 5.1.1(v) / GDPR), data export, and the
// billing-state endpoint behind the scans-remaining indicator.
type Users struct {
	Cfg     *config.Config
	Store   *store.Store
	S3      *awsx.S3
	Cognito *awsx.Cognito // nil in static-auth local dev — DDB+S3 cascade still runs
}

// Me: GET /v1/users/me → {entitlement, scans_remaining, allowance_ends_at}.
// Powers the scans-remaining indicator and proactive paywall moments — the
// client never has to probe-by-scanning. Subscribers report the daily cap's
// remainder and a null allowance_ends_at.
func (h *Users) Me(w http.ResponseWriter, r *http.Request) {
	ent := middleware.EntitlementFrom(r.Context())
	now := time.Now().UTC()

	resp := struct {
		Entitlement    models.Entitlement `json:"entitlement"`
		ScansRemaining int                `json:"scans_remaining"`
		AllowanceEnds  *time.Time         `json:"allowance_ends_at"`
		AppleLinked    bool               `json:"apple_linked"`
		Limits         map[string]float64 `json:"limits"`
	}{Entitlement: ent.Profile.Ent(), AppleLinked: ent.Profile.AppleSub != "", Limits: emptyLimitsIfNil(ent.Profile.Limits)}

	if !ent.Enforced || ent.Subscribed {
		used, err := h.Store.GetDailyScans(r.Context(), middleware.UserID(r.Context()), now.Format("2006-01-02"))
		if err != nil {
			writeInternal(w, r, err, "read daily scans")
			return
		}
		resp.ScansRemaining = max(0, h.Cfg.DailyScanCap-used)
	} else {
		window := time.Duration(h.Cfg.FreeWindowDays) * 24 * time.Hour
		remaining, _ := ent.Profile.FreeAllowance(h.Cfg.FreeScanAllowance, window, now)
		resp.ScansRemaining = remaining
		if ends := ent.Profile.AllowanceEndsAt(window); !ends.IsZero() {
			resp.AllowanceEnds = &ends
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func emptyLimitsIfNil(m map[string]float64) map[string]float64 {
	if m == nil {
		return map[string]float64{}
	}
	return m
}

// maxLimitValue bounds limit inputs to something physically meaningful
// (sodium in mg is the largest sane unit; 1e6 covers it with headroom).
const maxLimitValue = 1_000_000

// UpdateMe: PATCH /v1/users/me {limits} — replace-semantics daily caps.
// Keys must be summary field names; values positive. Enforcement is purely
// client-side coloring (plan M9): the server is just durable storage.
func (h *Users) UpdateMe(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())

	var req struct {
		Limits map[string]float64 `json:"limits"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Limits == nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "limits object required (send {} to clear)")
		return
	}
	if len(req.Limits) > len(models.LimitableFields) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "too many limits")
		return
	}
	for k, v := range req.Limits {
		if !models.LimitableFields[k] {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "unknown limit field: "+k)
			return
		}
		if v <= 0 || v > maxLimitValue || math.IsNaN(v) || math.IsInf(v, 0) {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "limit out of range: "+k)
			return
		}
	}
	if err := h.Store.SetLimits(r.Context(), userID, req.Limits, time.Now().UTC()); err != nil {
		writeInternal(w, r, err, "set limits")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"limits": req.Limits})
}

// DeleteMe: DELETE /v1/users/me — full cascade: every DDB item in the
// partition, every S3 photo under the user's prefix, then the Cognito user.
// Order matters: identity last, so a failed run can be retried with the
// same token. An active App Store subscription is NOT cancelled by this
// (the client must tell the user and link to Apple's manage page).
//
// Known residual: Cognito access tokens are stateless — one already issued
// stays verifiable for up to 1h after deletion. Refresh is dead and the
// data is gone; a write in that window would only create an orphaned
// partition. Accepted for v1 (the client discards identity immediately).
func (h *Users) DeleteMe(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	log := logging.From(r.Context())

	items, err := h.Store.DeleteUserData(r.Context(), userID)
	if err != nil {
		writeInternal(w, r, fmt.Errorf("ddb cascade (deleted %d): %w", items, err), "delete user data")
		return
	}
	objects, err := h.S3.DeletePrefix(r.Context(), fmt.Sprintf("scans/%s/", userID))
	if err != nil {
		writeInternal(w, r, fmt.Errorf("s3 cascade (deleted %d): %w", objects, err), "delete user photos")
		return
	}
	if h.Cognito != nil {
		if err := h.Cognito.DeleteUserBySub(r.Context(), userID); err != nil {
			writeInternal(w, r, err, "delete cognito user")
			return
		}
	}
	log.Info("account deleted",
		zap.Int("ddb_items", items),
		zap.Int("s3_objects", objects),
	)
	w.WriteHeader(http.StatusNoContent)
}

// Export: GET /v1/export — the user's full diary as JSON (data
// portability). Meals only: scans are transient processing artifacts.
func (h *Users) Export(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	// Full history: page year-by-year from 2024 (well before launch) to a
	// year ahead (planned meals).
	var all []models.Meal
	start := 2024
	end := time.Now().UTC().Year() + 1
	for y := start; y <= end; y++ {
		meals, err := h.Store.ListMealsRange(r.Context(), userID,
			fmt.Sprintf("%d-01-01", y), fmt.Sprintf("%d-12-31", y))
		if err != nil {
			writeInternal(w, r, err, "export meals")
			return
		}
		all = append(all, meals...)
	}
	if all == nil {
		all = []models.Meal{}
	}
	w.Header().Set("Content-Disposition", `attachment; filename="smachnogo-export.json"`)
	writeJSON(w, http.StatusOK, map[string]any{
		"exported_at": time.Now().UTC().Format(time.RFC3339),
		"meals":       all,
	})
}
