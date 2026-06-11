package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"smachnogo/pkg/api/middleware"
	"smachnogo/pkg/models"
	"smachnogo/pkg/store"
)

type patchMealReq struct {
	Label         *string  `json:"label"`
	State         *string  `json:"state"`
	ConsumedAt    *string  `json:"consumed_at"`
	PortionFactor *float64 `json:"portion_factor"`
	NewDate       *string  `json:"new_date"`
}

// Patch: PATCH /v1/meals/{meal_id}?date= — edit label/state/consumed_at,
// rescale portion (always from the BASE estimate, never compounding), or
// move to a new date (transactional re-key).
func (h *Meals) Patch(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	mealID := chi.URLParam(r, "mealID")
	date := r.URL.Query().Get("date")
	if err := middleware.ValidateMealID(mealID); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "meal_id "+err.Error())
		return
	}
	if err := middleware.ValidateDate(date); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "date "+err.Error())
		return
	}
	var req patchMealReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}

	meal, err := h.Store.GetMeal(r.Context(), userID, date, mealID)
	if errors.Is(err, store.ErrMealNotFound) {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "meal not found")
		return
	}
	if err != nil {
		writeInternal(w, r, err, "get meal")
		return
	}

	if req.Label != nil {
		if *req.Label == "" || len(*req.Label) > 120 {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "label must be 1-120 chars")
			return
		}
		meal.Label = *req.Label
	}
	if req.State != nil {
		switch models.MealState(*req.State) {
		case models.MealStateLogged, models.MealStatePlanned:
			meal.State = models.MealState(*req.State)
		default:
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "state must be logged or planned")
			return
		}
	}
	if req.ConsumedAt != nil {
		meal.ConsumedAt = *req.ConsumedAt
	}
	if req.PortionFactor != nil {
		f := *req.PortionFactor
		if f <= 0 || f > 10 {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "portion_factor must be in (0, 10]")
			return
		}
		base, scores, ok := h.baseNutrients(r, userID, meal)
		if !ok {
			return // response already written
		}
		scaled := base.ScaledBy(f)
		meal.Nutrients = scaled
		meal.Scores = scores
		meal.PortionFactor = f
	}
	meal.UpdatedAt = time.Now().UTC()

	if req.NewDate != nil && *req.NewDate != meal.Date {
		if err := middleware.ValidateDate(*req.NewDate); err != nil {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "new_date "+err.Error())
			return
		}
		oldDate := meal.Date
		meal.Date = *req.NewDate
		if err := h.Store.MoveMeal(r.Context(), userID, meal, oldDate); err != nil {
			if errors.Is(err, store.ErrMealNotFound) {
				writeErr(w, http.StatusConflict, "MOVE_CONFLICT", "meal moved or already exists at target date")
				return
			}
			writeInternal(w, r, err, "move meal")
			return
		}
	} else {
		if err := h.Store.ReplaceMeal(r.Context(), userID, meal); err != nil {
			if errors.Is(err, store.ErrMealNotFound) {
				writeErr(w, http.StatusNotFound, "NOT_FOUND", "meal not found")
				return
			}
			writeInternal(w, r, err, "replace meal")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"meal": meal})
}

// baseNutrients resolves the meal's base (portion_factor == 1.0) estimate:
// the scan dish (refined wins) for scan meals, the components sum for text
// meals, else divide-back from the stored values. Rescales NEVER compound —
// always from base.
func (h *Meals) baseNutrients(r *http.Request, userID string, meal *models.Meal) (models.Nutrients, models.Scores, bool) {
	if meal.ScanID != "" && meal.DishIndex != nil {
		scan, err := h.Store.GetScan(r.Context(), userID, meal.ScanID)
		if err == nil && scan.Result != nil && *meal.DishIndex < len(scan.Result.Dishes) {
			dish := scan.Result.Dishes[*meal.DishIndex]
			if rd, ok := scan.Refinements[strconv.Itoa(*meal.DishIndex)]; ok {
				dish = rd
			}
			return dish.Nutrients, dish.Scores, true
		}
		// Scan TTL'd out — fall through to divide-back.
	}
	if len(meal.Components) > 0 {
		var base models.Nutrients
		for _, c := range meal.Components {
			base = base.Plus(c.Nutrients)
		}
		return base, meal.Scores, true
	}
	// Divide-back fallback (manual meals, or scan meals whose scan TTL'd
	// out). portion_factor is validated > 0 at every write; defend anyway.
	pf := meal.PortionFactor
	if pf <= 0 {
		pf = 1
	}
	return meal.Nutrients.ScaledBy(1 / pf), meal.Scores, true
}

// Delete: DELETE /v1/meals/{meal_id}?date= → 204.
func (h *Meals) Delete(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	mealID := chi.URLParam(r, "mealID")
	date := r.URL.Query().Get("date")
	if err := middleware.ValidateMealID(mealID); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "meal_id "+err.Error())
		return
	}
	if err := middleware.ValidateDate(date); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "date "+err.Error())
		return
	}
	err := h.Store.DeleteMeal(r.Context(), userID, date, mealID)
	if errors.Is(err, store.ErrMealNotFound) {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "meal not found")
		return
	}
	if err != nil {
		writeInternal(w, r, err, "delete meal")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
