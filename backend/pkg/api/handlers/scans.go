package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"smachnogo/pkg/api/middleware"
	"smachnogo/pkg/awsx"
	"smachnogo/pkg/config"
	"smachnogo/pkg/devicecheck"
	"smachnogo/pkg/llm"
	"smachnogo/pkg/logging"
	"smachnogo/pkg/models"
	"smachnogo/pkg/scanproc"
	"smachnogo/pkg/store"
)

// Scans wires the scan endpoints. Queue is nil in LOCAL_SYNC mode (the
// processor runs inline); Processor is nil in deployed mode.
type Scans struct {
	Cfg         *config.Config
	Store       *store.Store
	S3          *awsx.S3
	Queue       *awsx.SQS
	Processor   *scanproc.Processor
	SSM         *awsx.SSM           // nil when SSM_PREFIX unset (local dev)
	Analyzer    llm.Analyzer        // dish refinement (text model)
	DeviceCheck devicecheck.Checker // reinstall-abuse guard; Disabled until the .p8 exists
}

func nowUTC() time.Time { return time.Now().UTC() }

type createScanReq struct {
	ScanID string `json:"scan_id"`
}

type uploadInfo struct {
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expires_at"`
}

type scanCreateResp struct {
	ScanID string            `json:"scan_id"`
	Status models.ScanStatus `json:"status"`
	Upload *uploadInfo       `json:"upload,omitempty"`
}

// Create: POST /v1/scans — idempotent on client-generated scan_id.
// Order matters: entitlement (402, M7) runs in middleware BEFORE this
// handler consumes quota (429), so a paywalled request never strands an
// un-refundable increment.
func (h *Scans) Create(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())

	if !h.scansEnabled(r) {
		writeErr(w, http.StatusServiceUnavailable, "SCANS_DISABLED", "scanning is temporarily disabled")
		return
	}

	var req createScanReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	if err := middleware.ValidateUUID(req.ScanID); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "scan_id "+err.Error())
		return
	}

	now := time.Now().UTC()
	date := now.Format("2006-01-02")

	// Idempotency BEFORE payment: a network-level retry of an existing scan
	// must answer 201 from state, not 402/429 — the original create already
	// paid. Concurrent same-id first-creates race past this read and settle
	// at the create-conditional below.
	if existing, err := h.Store.GetScan(r.Context(), userID, req.ScanID); err == nil {
		h.respondCreate(w, r, existing)
		return
	} else if !errors.Is(err, store.ErrScanNotFound) {
		writeInternal(w, r, err, "idempotency pre-check")
		return
	}

	// Free-allowance gate FIRST (402-class), then daily quota (429-class) —
	// a paywalled request must never strand an increment the worker won't
	// see. The conditional write is the authoritative, race-safe decision.
	ent := middleware.EntitlementFrom(r.Context())
	freeConsumed := false
	if ent.Enforced && !ent.Subscribed {
		// First scan = allowance grant — the DeviceCheck moment. Errors and
		// missing tokens fail OPEN (availability beats abuse-protection).
		if ent.Profile.AllowanceStartedAt == 0 && h.DeviceCheck != nil {
			used, derr := h.DeviceCheck.CheckAndSet(r.Context(), r.Header.Get("X-Device-Token"))
			if derr != nil {
				logging.From(r.Context()).Warn("devicecheck failed open", zap.Error(derr))
			} else if used {
				writePaywall(w, models.PaywallDeviceAlreadyUsed, 0)
				return
			}
		}
		window := time.Duration(h.Cfg.FreeWindowDays) * 24 * time.Hour
		err := h.Store.ConsumeFreeScan(r.Context(), userID, h.Cfg.FreeScanAllowance, window, now)
		var pw *store.PaywallError
		if errors.As(err, &pw) {
			writePaywall(w, pw.Reason, 0)
			return
		}
		if err != nil {
			writeInternal(w, r, err, "consume free allowance")
			return
		}
		freeConsumed = true
	}

	if err := h.Store.Consume(r.Context(), userID, date, store.QuotaScans, h.Cfg.DailyScanCap, now.Unix()); err != nil {
		if freeConsumed {
			if uerr := h.Store.UnconsumeScanCounters(r.Context(), userID, date, false, true); uerr != nil {
				logging.From(r.Context()).Warn("free-allowance handback failed", zap.Error(uerr))
			}
		}
		if errors.Is(err, store.ErrQuotaExceeded) {
			writeErr(w, http.StatusTooManyRequests, "RATE_LIMITED", "daily scan limit reached")
			return
		}
		writeInternal(w, r, err, "consume scan quota")
		return
	}

	scan := &models.Scan{
		ScanID:            req.ScanID,
		Status:            models.ScanStatusPendingUpload,
		S3Key:             awsx.ScanKey(userID, req.ScanID),
		AllowanceConsumed: freeConsumed,
		CreatedAt:         now,
		UpdatedAt:         now,
		ExpiresAt:         now.Add(h.Cfg.ScanResultTTL).Unix(),
	}
	err := h.Store.CreateScan(r.Context(), userID, scan)
	if errors.Is(err, store.ErrAlreadyExists) {
		// Retry of an existing create: hand back exactly the counters this
		// duplicate took (direct decrements — the quota_refunded flag stays
		// reserved for the scan's own terminal refund), then answer
		// idempotently from current state.
		if rerr := h.Store.UnconsumeScanCounters(r.Context(), userID, date, true, freeConsumed); rerr != nil {
			logging.From(r.Context()).Warn("idempotent-create handback failed", zap.Error(rerr))
		}
		existing, gerr := h.Store.GetScan(r.Context(), userID, req.ScanID)
		if gerr != nil {
			writeInternal(w, r, gerr, "get existing scan")
			return
		}
		h.respondCreate(w, r, existing)
		return
	}
	if err != nil {
		writeInternal(w, r, err, "create scan")
		return
	}
	h.respondCreate(w, r, scan)
}

// respondCreate returns the create response; a fresh presigned URL is
// issued whenever the scan still awaits its upload (URLs need not be
// stable across retries — both stay valid until expiry).
func (h *Scans) respondCreate(w http.ResponseWriter, r *http.Request, scan *models.Scan) {
	resp := scanCreateResp{ScanID: scan.ScanID, Status: scan.Status}
	if scan.Status == models.ScanStatusPendingUpload {
		url, err := h.S3.PresignPut(r.Context(), scan.S3Key, h.Cfg.PresignTTL)
		if err != nil {
			writeInternal(w, r, err, "presign put")
			return
		}
		resp.Upload = &uploadInfo{
			URL:       url,
			Method:    http.MethodPut,
			Headers:   map[string]string{"Content-Type": "image/jpeg"},
			ExpiresAt: time.Now().UTC().Add(h.Cfg.PresignTTL),
		}
	}
	writeJSON(w, http.StatusCreated, resp)
}

// Uploaded: POST /v1/scans/{scan_id}/uploaded — the explicit confirm that
// replaces S3 event notifications. Idempotent; only the PENDING_UPLOAD→
// QUEUED transition winner enqueues (or processes inline under LOCAL_SYNC).
func (h *Scans) Uploaded(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	scanID := chi.URLParam(r, "scanID")
	if err := middleware.ValidateUUID(scanID); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "scan_id "+err.Error())
		return
	}

	transitioned, err := h.Store.TransitionToQueued(r.Context(), userID, scanID, time.Now().UTC())
	if err != nil {
		writeInternal(w, r, err, "transition to queued")
		return
	}
	if transitioned {
		if h.Cfg.LocalSync {
			// Inline processing on a detached context — the request returns
			// QUEUED immediately and the client polls, identical to prod.
			h.processDetached(logging.From(r.Context()), userID, scanID)
		} else {
			if err := h.Queue.SendScan(r.Context(), userID, scanID, middleware.RequestID(r.Context())); err != nil {
				writeInternal(w, r, err, "enqueue scan")
				return
			}
		}
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
	writeJSON(w, http.StatusOK, map[string]any{"scan_id": scanID, "status": scan.Status})
}

// processDetached runs the shared processor on a background context (the
// HTTP request context dies when the response is written). LOCAL_SYNC only.
func (h *Scans) processDetached(log *zap.Logger, userID, scanID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		ctx = logging.Into(ctx, log)
		if err := h.Processor.Process(ctx, userID, scanID); err != nil {
			log.Error("local-sync processing failed", zap.Error(err),
				zap.String("scan_id", scanID))
		}
	}()
}

// Get: GET /v1/scans/{scan_id} — the poll endpoint. Returns refinements so
// a re-opened scan renders refined dishes over the immutable original.
func (h *Scans) Get(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	scanID := chi.URLParam(r, "scanID")
	if err := middleware.ValidateUUID(scanID); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "scan_id "+err.Error())
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
	writeJSON(w, http.StatusOK, scan)
}

type confirmDishReq struct {
	Index         int      `json:"index"`
	PortionFactor *float64 `json:"portion_factor"` // nil → 1.0
}

type confirmReq struct {
	Dishes     []confirmDishReq `json:"dishes"`
	Date       string           `json:"date"`
	ConsumedAt string           `json:"consumed_at"`
}

// Confirm: POST /v1/scans/{scan_id}/confirm — creates meals from selected
// dishes. Additive per index (re-confirm returns the existing meal as
// success); future date saves as planned; portion_factor scales nutrients
// linearly from the immutable (or refined) dish, scores untouched.
func (h *Scans) Confirm(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	scanID := chi.URLParam(r, "scanID")
	if err := middleware.ValidateUUID(scanID); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "scan_id "+err.Error())
		return
	}
	var req confirmReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	if err := middleware.ValidateDate(req.Date); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "date "+err.Error())
		return
	}
	if len(req.Dishes) == 0 {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "dishes must be non-empty")
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
	if !scan.Result.IsFood {
		writeErr(w, http.StatusConflict, "NOT_FOOD", "scan detected no food")
		return
	}

	now := time.Now().UTC()
	state := models.MealStateLogged
	if req.Date > now.Format("2006-01-02") {
		state = models.MealStatePlanned
	}
	consumedAt := req.ConsumedAt
	if consumedAt == "" {
		consumedAt = now.Format(time.RFC3339)
	}

	meals := make([]models.Meal, 0, len(req.Dishes))
	for _, dr := range req.Dishes {
		if dr.Index < 0 || dr.Index >= len(scan.Result.Dishes) {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", fmt.Sprintf("dish index %d out of range", dr.Index))
			return
		}
		factor := 1.0
		if dr.PortionFactor != nil {
			factor = *dr.PortionFactor
		}
		if factor <= 0 || factor > 10 {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "portion_factor must be in (0, 10]")
			return
		}

		// Refined dish wins over the immutable original.
		dish := scan.Result.Dishes[dr.Index]
		refined := false
		refinementAnswer := ""
		if rd, ok := scan.Refinements[strconv.Itoa(dr.Index)]; ok {
			dish = rd
			refined = true
			refinementAnswer = rd.Description // the revised description carries the answer's substance
		}
		scaled := dish.Scale(factor)

		idx := dr.Index
		meal := models.Meal{
			MealID:           fmt.Sprintf("%s-%d", scanID, dr.Index),
			Date:             req.Date,
			State:            state,
			ConsumedAt:       consumedAt,
			Label:            dish.Label,
			Source:           models.MealSourceScan,
			Nutrients:        scaled.Nutrients,
			Scores:           scaled.Scores,
			PortionFactor:    factor,
			Refined:          refined,
			RefinementAnswer: refinementAnswer,
			ScanID:           scanID,
			DishIndex:        &idx,
			PhotoS3Key:       awsx.ScanKey(userID, scanID),
			SchemaVersion:    models.MealSchemaVersion,
			CreatedAt:        now,
			UpdatedAt:        now,
		}

		err := h.Store.ConfirmDish(r.Context(), userID, scanID, dr.Index, &meal)
		if errors.Is(err, store.ErrAlreadyExists) {
			// Additive semantics: this index was confirmed before — return
			// the recorded meal as success.
			if cd, ok := scan.ConfirmedDishes[strconv.Itoa(dr.Index)]; ok {
				if existing, gerr := h.Store.GetMeal(r.Context(), userID, cd.Date, cd.MealID); gerr == nil {
					meals = append(meals, *existing)
					continue
				}
			}
			// Mapping raced ahead of our read — re-read scan once.
			if fresh, gerr := h.Store.GetScan(r.Context(), userID, scanID); gerr == nil {
				if cd, ok := fresh.ConfirmedDishes[strconv.Itoa(dr.Index)]; ok {
					if existing, gerr2 := h.Store.GetMeal(r.Context(), userID, cd.Date, cd.MealID); gerr2 == nil {
						meals = append(meals, *existing)
						continue
					}
				}
			}
			writeInternal(w, r, fmt.Errorf("confirmed dish mapping unreadable for index %d", dr.Index), "confirm dish")
			return
		}
		if err != nil {
			writeInternal(w, r, err, "confirm dish")
			return
		}
		meals = append(meals, meal)
	}

	writeJSON(w, http.StatusCreated, map[string]any{"meals": meals})
}

func (h *Scans) scansEnabled(r *http.Request) bool {
	if h.SSM != nil {
		return h.SSM.ScansEnabled(r.Context(), h.Cfg.ScansEnabled)
	}
	return h.Cfg.ScansEnabled
}
