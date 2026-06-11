package handlers

import (
	"net/http"
	"time"

	"smachnogo/pkg/api/middleware"
	"smachnogo/pkg/summary"
)

// Summary: GET /v1/summary?granularity=day|week|month&from=&to= — folds the
// meal range on read (day views are served instantly client-side from
// loaded meals; this endpoint powers week/month/stats).
func (h *Meals) Summary(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	g := summary.Granularity(r.URL.Query().Get("granularity"))
	if g == "" {
		g = summary.GranularityDay
	}
	switch g {
	case summary.GranularityDay, summary.GranularityWeek, summary.GranularityMonth:
	default:
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "granularity must be day, week or month")
		return
	}
	if err := middleware.ValidateDate(from); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "from "+err.Error())
		return
	}
	if err := middleware.ValidateDate(to); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "to "+err.Error())
		return
	}
	if from > to {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "from must be <= to")
		return
	}
	fromT, _ := time.Parse("2006-01-02", from)
	toT, _ := time.Parse("2006-01-02", to)
	if int(toT.Sub(fromT).Hours()/24) > summary.MaxRangeDays(g) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "range exceeds the cap for this granularity")
		return
	}

	meals, err := h.Store.ListMealsRange(r.Context(), userID, from, to)
	if err != nil {
		writeInternal(w, r, err, "list meals for summary")
		return
	}
	writeJSON(w, http.StatusOK, summary.Fold(meals, g))
}
