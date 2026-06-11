package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"smachnogo/pkg/api/middleware"
	"smachnogo/pkg/config"
	"smachnogo/pkg/llm"
	"smachnogo/pkg/models"
	"smachnogo/pkg/store"
)

type Meals struct {
	Cfg      *config.Config
	Store    *store.Store
	Analyzer llm.Analyzer // text estimates + dish refinement
}

// Recent: GET /v1/meals/recent?limit= — label-deduped recents for quick
// re-add and "same as last time" refinement chips.
func (h *Meals) Recent(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 50 {
			limit = n
		}
	}
	// Scan back 90 days; newest first, dedupe by normalized label.
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -90).Format("2006-01-02")
	to := now.Format("2006-01-02")
	meals, err := h.Store.ListMealsRange(r.Context(), userID, from, to)
	if err != nil {
		writeInternal(w, r, err, "list recent meals")
		return
	}
	seen := map[string]bool{}
	recents := make([]models.Meal, 0, limit)
	for i := len(meals) - 1; i >= 0 && len(recents) < limit; i-- { // SK order is chronological → walk backward
		m := meals[i]
		if m.State != models.MealStateLogged {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(m.Label))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		recents = append(recents, m)
	}
	writeJSON(w, http.StatusOK, map[string]any{"meals": recents})
}

type createMealReq struct {
	MealID     string                `json:"meal_id"`
	Date       string                `json:"date"`
	State      models.MealState      `json:"state"`
	ConsumedAt string                `json:"consumed_at"`
	Label      string                `json:"label"`
	Source     models.MealSource     `json:"source"`
	models.Nutrients
	models.Scores
	Components []models.EstimateItem `json:"components"`
}

// Create: POST /v1/meals — manual save / planned meal / re-add. Client
// supplies meal_id (idempotency: a retried save can't double-log).
func (h *Meals) Create(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	var req createMealReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	if err := middleware.ValidateMealID(req.MealID); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "meal_id "+err.Error())
		return
	}
	if err := middleware.ValidateDate(req.Date); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "date "+err.Error())
		return
	}
	if req.Label == "" || len(req.Label) > 120 {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "label must be 1-120 chars")
		return
	}
	switch req.State {
	case models.MealStateLogged, models.MealStatePlanned:
	case "":
		req.State = models.MealStateLogged
	default:
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "state must be logged or planned")
		return
	}
	switch req.Source {
	case models.MealSourceText, models.MealSourceManual, models.MealSourceReadd:
	case "":
		req.Source = models.MealSourceManual
	default:
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "source must be text, manual or readd")
		return
	}

	now := time.Now().UTC()
	consumedAt := req.ConsumedAt
	if consumedAt == "" {
		consumedAt = now.Format(time.RFC3339)
	}
	meal := models.Meal{
		MealID:        req.MealID,
		Date:          req.Date,
		State:         req.State,
		ConsumedAt:    consumedAt,
		Label:         req.Label,
		Source:        req.Source,
		Nutrients:     req.Nutrients,
		Scores:        req.Scores,
		PortionFactor: 1.0,
		Components:    req.Components,
		SchemaVersion: models.MealSchemaVersion,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	meal.Nutrients.ClampForStorage()

	err := h.Store.CreateMeal(r.Context(), userID, &meal)
	if errors.Is(err, store.ErrAlreadyExists) {
		existing, gerr := h.Store.GetMeal(r.Context(), userID, req.Date, req.MealID)
		if gerr != nil {
			writeInternal(w, r, gerr, "get existing meal")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"meal": existing})
		return
	}
	if err != nil {
		writeInternal(w, r, err, "create meal")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"meal": meal})
}

// List: GET /v1/meals?from=&to= — diary range (both states; the client
// partitions planned vs logged).
func (h *Meals) List(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
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
	if toT.Sub(fromT) > 92*24*time.Hour {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "range exceeds 92 days")
		return
	}

	meals, err := h.Store.ListMealsRange(r.Context(), userID, from, to)
	if err != nil {
		writeInternal(w, r, err, "list meals")
		return
	}
	if meals == nil {
		meals = []models.Meal{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"meals": meals})
}
