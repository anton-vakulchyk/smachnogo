package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"smachnogo/pkg/api/middleware"
	"smachnogo/pkg/llm"
	"smachnogo/pkg/logging"
	"smachnogo/pkg/models"
	"smachnogo/pkg/store"
)

// Estimate: POST /v1/meals/estimate — sync free-text estimation (does NOT
// save; the client edits then POSTs /v1/meals). Powers the free-forever
// diary, so it runs on the cheap text model.
func (h *Meals) Estimate(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	if h.Analyzer == nil {
		writeErr(w, http.StatusServiceUnavailable, "ESTIMATE_UNAVAILABLE", "estimation is not configured")
		return
	}

	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" || len(req.Text) > middleware.MaxEstimateTextLen {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "text must be 1-500 chars")
		return
	}

	now := time.Now().UTC()
	if err := h.Store.Consume(r.Context(), userID, now.Format("2006-01-02"), store.QuotaEstimates, h.Cfg.DailyEstimateCap, now.Unix()); err != nil {
		if errors.Is(err, store.ErrQuotaExceeded) {
			writeErr(w, http.StatusTooManyRequests, "RATE_LIMITED", "daily estimate limit reached")
			return
		}
		writeInternal(w, r, err, "consume estimate quota")
		return
	}

	est, usage, err := h.Analyzer.EstimateText(r.Context(), req.Text)
	if err != nil {
		// Sync path: no queue to retry through — surface a friendly failure
		// and let the client retry. The quota was consumed; estimates are
		// ~$0.001 so failed-call refunds aren't worth the machinery.
		writeErr(w, http.StatusBadGateway, "ESTIMATE_FAILED", "couldn't estimate right now — try again")
		return
	}
	logUsage(r, "estimate", usage)

	for i := range est.Items {
		est.Items[i].Nutrients.ClampForStorage()
		est.Items[i].NutritionScore = clampScore(est.Items[i].NutritionScore)
		est.Items[i].DietQualityScore = clampScore(est.Items[i].DietQualityScore)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"is_food":     est.IsFood,
		"label":       est.Label,
		"assumptions": est.Assumptions,
		"items":       emptyIfNil(est.Items),
		"totals":      est.Totals(),
	})
}

func clampScore(s int) int {
	if s < 0 {
		return 0
	}
	if s > 100 {
		return 100
	}
	return s
}

func emptyIfNil(items []models.EstimateItem) []models.EstimateItem {
	if items == nil {
		return []models.EstimateItem{}
	}
	return items
}

func logUsage(r *http.Request, what string, usage llm.Usage) {
	logging.From(r.Context()).Info("llm usage",
		zap.String("op", what),
		zap.Int("tokens_in", usage.InputTokens),
		zap.Int("tokens_out", usage.OutputTokens),
		zap.Int64("latency_ms", usage.LatencyMS),
	)
}
