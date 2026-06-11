package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"smachnogo/pkg/api/middleware"
	"smachnogo/pkg/models"
	"smachnogo/pkg/store"
)

// Refine: POST /v1/scans/{scan_id}/refine {dish_index, answer} —
// re-estimates one low-confidence dish via the text model given the user's
// answer about its contents. The original result stays immutable; revisions
// live in refinements{}. Last answer wins; 409 once the index is confirmed
// (edit the meal instead).
func (h *Scans) Refine(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	scanID := chi.URLParam(r, "scanID")
	if err := middleware.ValidateUUID(scanID); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "scan_id "+err.Error())
		return
	}
	if h.Analyzer == nil {
		writeErr(w, http.StatusServiceUnavailable, "REFINE_UNAVAILABLE", "refinement is not configured")
		return
	}
	var req struct {
		DishIndex int    `json:"dish_index"`
		Answer    string `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	req.Answer = strings.TrimSpace(req.Answer)
	if req.Answer == "" || len(req.Answer) > middleware.MaxEstimateTextLen {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "answer must be 1-500 chars")
		return
	}

	scan, err := h.Store.GetScan(r.Context(), userID, scanID)
	if errors.Is(err, store.ErrScanNotFound) {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "scan not found")
		return
	}
	if err != nil {
		writeInternal(w, r, err, "get scan")
		return
	}
	if scan.Status != models.ScanStatusReady || scan.Result == nil {
		writeErr(w, http.StatusConflict, "NOT_READY", "scan is not READY")
		return
	}
	if req.DishIndex < 0 || req.DishIndex >= len(scan.Result.Dishes) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "dish_index out of range")
		return
	}
	idx := strconv.Itoa(req.DishIndex)
	if _, confirmed := scan.ConfirmedDishes[idx]; confirmed {
		writeErr(w, http.StatusConflict, "ALREADY_CONFIRMED", "dish already saved — edit the meal instead")
		return
	}

	// Estimates quota covers refinement (same cheap text model).
	now := nowUTC()
	if err := h.Store.Consume(r.Context(), userID, now.Format("2006-01-02"), store.QuotaEstimates, estimateCap(r, h.Cfg.DailyEstimateCap, h.Cfg.DailyEstimateCapSub), now.Unix()); err != nil {
		if errors.Is(err, store.ErrQuotaExceeded) {
			writeErr(w, http.StatusTooManyRequests, "RATE_LIMITED", "daily estimate limit reached")
			return
		}
		writeInternal(w, r, err, "consume estimate quota")
		return
	}

	// Always refine from the ORIGINAL dish (last answer wins — refining a
	// refinement would compound assumptions).
	original := scan.Result.Dishes[req.DishIndex]
	revised, usage, err := h.Analyzer.RefineDish(r.Context(), original, req.Answer)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "REFINE_FAILED", "couldn't refine right now — try again")
		return
	}
	logUsage(r, "refine", usage)

	revised.Clamp()
	revised.NeedsClarification = false
	revised.ClarificationQuestion = ""
	revised.ClarificationOptions = []string{}
	// Description doubles as the durable record of what the user told us.
	if !strings.Contains(strings.ToLower(revised.Description), strings.ToLower(req.Answer)) {
		revised.Description = strings.TrimRight(revised.Description, ". ") + ". (" + req.Answer + ")"
	}

	if err := h.Store.WriteRefinement(r.Context(), userID, scanID, req.DishIndex, *revised, now); err != nil {
		writeInternal(w, r, err, "write refinement")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dish_index": req.DishIndex, "dish": revised})
}
