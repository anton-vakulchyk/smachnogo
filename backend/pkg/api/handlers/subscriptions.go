package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/awa/go-iap/appstore"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"smachnogo/pkg/api/middleware"
	"smachnogo/pkg/config"
	"smachnogo/pkg/logging"
	"smachnogo/pkg/models"
	"smachnogo/pkg/store"
)

// Subscriptions: StoreKit 2 server-side verification. Two entry points feed
// the same entitlement state: the client posting its jwsRepresentation
// (purchase, restore, launch reconcile) and Apple's App Store Server
// Notifications V2 webhook. Client-supplied product/expiry fields are never
// trusted — only what's inside Apple-signed JWS payloads.
type Subscriptions struct {
	Cfg   *config.Config
	Store *store.Store
	// verify parses a JWS and validates its x5c chain against the Apple
	// root CA (go-iap). In APPSTORE_VERIFY_MODE=insecure_dev it only
	// decodes — Xcode StoreKit-testing transactions are signed by a local
	// test cert, not Apple; config refuses that mode in prod.
	verify func(jws string, claims jwt.Claims) error
}

func NewSubscriptions(cfg *config.Config, st *store.Store) *Subscriptions {
	s := &Subscriptions{Cfg: cfg, Store: st}
	if cfg.AppStoreVerifyMode == "insecure_dev" {
		s.verify = parseJWSUnverified
	} else {
		client := appstore.New()
		s.verify = client.ParseNotificationV2WithClaim
	}
	return s
}

func parseJWSUnverified(tokenStr string, claims jwt.Claims) error {
	_, _, err := jwt.NewParser().ParseUnverified(tokenStr, claims)
	return err
}

// entitlementFromTransaction derives the billing state from an Apple-signed
// transaction: revoked beats everything; inside the period it's trialing
// (introductory offer) or active; past expiry it's expired. Grace and
// billing-retry states only arrive via webhook notifications.
func entitlementFromTransaction(tx *appstore.JWSTransactionDecodedPayload, now time.Time) models.Entitlement {
	if tx.RevocationDate > 0 {
		return models.EntitlementRevoked
	}
	if tx.ExpiresDate > 0 && now.UnixMilli() < tx.ExpiresDate {
		if tx.OfferType == 1 { // introductory offer = the 7-day trial
			return models.EntitlementTrialing
		}
		return models.EntitlementActive
	}
	return models.EntitlementExpired
}

type receiptReq struct {
	JWS string `json:"jws_representation"`
}

// Receipt: POST /v1/subscriptions/receipt — the authed client posts the
// JWS for its current transaction. Sets entitlement and claims the
// originalTransactionId for this user (latest claim wins): restore on a new
// device transfers the subscription, demoting the previous owner.
func (h *Subscriptions) Receipt(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	log := logging.From(r.Context())

	var req receiptReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.JWS == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "jws_representation required")
		return
	}

	tx := &appstore.JWSTransactionDecodedPayload{}
	if err := h.verify(req.JWS, tx); err != nil {
		log.Warn("receipt JWS rejected", zap.Error(err))
		writeErr(w, http.StatusBadRequest, "INVALID_RECEIPT", "transaction verification failed")
		return
	}
	if tx.BundleId != h.Cfg.AppleAppBundleID || tx.OriginalTransactionId == "" {
		writeErr(w, http.StatusBadRequest, "INVALID_RECEIPT", "transaction is not for this app")
		return
	}

	now := time.Now().UTC()
	ent := entitlementFromTransaction(tx, now)

	prevOwner, err := h.Store.ClaimTransaction(r.Context(), tx.OriginalTransactionId, userID, now)
	if err != nil {
		writeInternal(w, r, err, "claim transaction")
		return
	}
	if prevOwner != "" {
		// Device/account transfer: one active user per subscription.
		if err := h.Store.SetEntitlement(r.Context(), prevOwner, models.EntitlementExpired, tx.OriginalTransactionId, now.UnixMilli(), now); err != nil && !errors.Is(err, store.ErrStaleEntitlement) {
			writeInternal(w, r, err, "demote previous owner")
			return
		}
		log.Info("subscription transferred", zap.String("from_user", prevOwner))
	}

	err = h.Store.SetEntitlement(r.Context(), userID, ent, tx.OriginalTransactionId, tx.SignedDate, now)
	if errors.Is(err, store.ErrStaleEntitlement) {
		// A newer webhook already landed — report the stored state.
		p, gerr := h.Store.GetProfile(r.Context(), userID)
		if gerr != nil {
			writeInternal(w, r, gerr, "read profile after stale receipt")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"entitlement": p.Ent()})
		return
	}
	if err != nil {
		writeInternal(w, r, err, "set entitlement")
		return
	}
	log.Info("entitlement set from receipt",
		zap.String("entitlement", string(ent)),
		zap.String("product_id", tx.ProductId),
	)
	writeJSON(w, http.StatusOK, map[string]any{"entitlement": ent})
}

// entitlementForNotification maps a notification (type, subtype, signed
// transaction) to a billing state; applicable=false means the notification
// carries no entitlement change (renewal-pref toggles etc.).
func entitlementForNotification(typ, subtype string, tx *appstore.JWSTransactionDecodedPayload, now time.Time) (ent models.Entitlement, applicable bool) {
	switch typ {
	case "SUBSCRIBED", "DID_RENEW", "OFFER_REDEEMED", "RENEWAL_EXTENDED":
		return entitlementFromTransaction(tx, now), true
	case "DID_FAIL_TO_RENEW":
		// Billing Grace Period keeps access while Apple retries the card;
		// without the subtype the user is in retry WITHOUT entitlement only
		// after grace expires — handled by GRACE_PERIOD_EXPIRED below.
		if subtype == "GRACE_PERIOD" {
			return models.EntitlementGrace, true
		}
		return models.EntitlementBillingRetry, true
	case "GRACE_PERIOD_EXPIRED", "EXPIRED":
		return models.EntitlementExpired, true
	case "REFUND", "REVOKE":
		return models.EntitlementRevoked, true
	}
	return "", false
}

// Webhook: POST /v1/webhooks/appstore — App Store Server Notifications V2.
// Auth-exempt (Apple sends no bearer token); trusts JWS verification only.
// Duplicates and out-of-order delivery are NORMAL: the signedDate condition
// in SetEntitlement is the ordering authority, the notificationUUID dedup
// item is the fast path. Apply-then-mark: a failure after applying re-runs
// idempotently on Apple's retry.
func (h *Subscriptions) Webhook(w http.ResponseWriter, r *http.Request) {
	log := logging.From(r.Context())

	var body struct {
		SignedPayload string `json:"signedPayload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.SignedPayload == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "signedPayload required")
		return
	}

	payload := &appstore.SubscriptionNotificationV2DecodedPayload{}
	if err := h.verify(body.SignedPayload, payload); err != nil {
		log.Warn("webhook signature rejected", zap.Error(err))
		writeErr(w, http.StatusUnauthorized, "INVALID_SIGNATURE", "payload verification failed")
		return
	}
	typ, subtype := string(payload.NotificationType), string(payload.Subtype)
	log = log.With(
		zap.String("notification_type", typ),
		zap.String("subtype", subtype),
		zap.String("notification_uuid", payload.NotificationUUID),
	)

	if payload.NotificationUUID != "" {
		done, err := h.Store.NotificationProcessed(r.Context(), payload.NotificationUUID)
		if err != nil {
			writeInternal(w, r, err, "notification dedup check")
			return
		}
		if done {
			log.Info("duplicate notification — acked")
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	signedTx := string(payload.Data.SignedTransactionInfo)
	if signedTx == "" {
		log.Info("webhook without transaction — acked")
		w.WriteHeader(http.StatusOK)
		return
	}
	tx := &appstore.JWSTransactionDecodedPayload{}
	if err := h.verify(signedTx, tx); err != nil {
		log.Warn("webhook transaction JWS rejected", zap.Error(err))
		writeErr(w, http.StatusUnauthorized, "INVALID_SIGNATURE", "transaction verification failed")
		return
	}
	if tx.BundleId != h.Cfg.AppleAppBundleID {
		log.Warn("webhook for foreign bundle", zap.String("bundle", tx.BundleId))
		w.WriteHeader(http.StatusOK)
		return
	}

	now := time.Now().UTC()
	ent, applicable := entitlementForNotification(typ, subtype, tx, now)
	if !applicable {
		log.Info("webhook carries no entitlement change — acked")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Resolve the user: the ownership item wins (it reflects transfers; the
	// appAccountToken is frozen at purchase time and would resurrect a
	// previous owner after a device transfer), token is the bootstrap.
	userID, err := h.Store.TransactionOwner(r.Context(), tx.OriginalTransactionId)
	if err != nil {
		writeInternal(w, r, err, "resolve transaction owner")
		return
	}
	if userID == "" {
		if err := middleware.ValidateUUID(tx.AppAccountToken); err != nil {
			log.Warn("webhook unattributable — no owner item, no usable appAccountToken")
			w.WriteHeader(http.StatusOK)
			return
		}
		userID = tx.AppAccountToken
		if _, err := h.Store.ClaimTransaction(r.Context(), tx.OriginalTransactionId, userID, now); err != nil {
			writeInternal(w, r, err, "claim transaction from webhook")
			return
		}
	}

	err = h.Store.SetEntitlement(r.Context(), userID, ent, tx.OriginalTransactionId, payload.SignedDate, now)
	if err != nil && !errors.Is(err, store.ErrStaleEntitlement) {
		writeInternal(w, r, err, "apply webhook entitlement") // 5xx → Apple retries
		return
	}
	if errors.Is(err, store.ErrStaleEntitlement) {
		log.Info("webhook stale (out-of-order) — dropped")
	} else {
		log.Info("entitlement set from webhook", zap.String("entitlement", string(ent)))
	}

	if payload.NotificationUUID != "" {
		if derr := h.Store.MarkNotificationProcessed(r.Context(), payload.NotificationUUID, now); derr != nil && !errors.Is(derr, store.ErrAlreadyExists) {
			log.Warn("notification dedup write failed", zap.Error(derr))
		}
	}
	w.WriteHeader(http.StatusOK)
}
