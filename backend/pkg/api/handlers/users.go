package handlers

import (
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"smachnogo/pkg/api/middleware"
	"smachnogo/pkg/awsx"
	"smachnogo/pkg/logging"
	"smachnogo/pkg/models"
	"smachnogo/pkg/store"
)

// Users: account deletion (App Store 5.1.1(v) / GDPR) and data export.
type Users struct {
	Store   *store.Store
	S3      *awsx.S3
	Cognito *awsx.Cognito // nil in static-auth local dev — DDB+S3 cascade still runs
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
