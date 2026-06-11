package handlers

import (
	"testing"
	"time"

	"github.com/awa/go-iap/appstore"

	"smachnogo/pkg/models"
)

func TestEntitlementFromTransaction(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	in := now.Add(24 * time.Hour).UnixMilli()
	past := now.Add(-24 * time.Hour).UnixMilli()

	cases := []struct {
		name string
		tx   appstore.JWSTransactionDecodedPayload
		want models.Entitlement
	}{
		{"active inside period", appstore.JWSTransactionDecodedPayload{ExpiresDate: in}, models.EntitlementActive},
		{"trial via introductory offer", appstore.JWSTransactionDecodedPayload{ExpiresDate: in, OfferType: 1}, models.EntitlementTrialing},
		{"expired", appstore.JWSTransactionDecodedPayload{ExpiresDate: past}, models.EntitlementExpired},
		{"no expiry at all", appstore.JWSTransactionDecodedPayload{}, models.EntitlementExpired},
		{"revoked beats active period", appstore.JWSTransactionDecodedPayload{ExpiresDate: in, RevocationDate: past}, models.EntitlementRevoked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := entitlementFromTransaction(&tc.tx, now); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestEntitlementForNotification(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	live := &appstore.JWSTransactionDecodedPayload{ExpiresDate: now.Add(time.Hour).UnixMilli()}
	trial := &appstore.JWSTransactionDecodedPayload{ExpiresDate: now.Add(time.Hour).UnixMilli(), OfferType: 1}

	cases := []struct {
		typ, subtype   string
		tx             *appstore.JWSTransactionDecodedPayload
		want           models.Entitlement
		wantApplicable bool
	}{
		{"SUBSCRIBED", "INITIAL_BUY", trial, models.EntitlementTrialing, true},
		{"SUBSCRIBED", "RESUBSCRIBE", live, models.EntitlementActive, true},
		{"DID_RENEW", "", live, models.EntitlementActive, true},
		{"OFFER_REDEEMED", "", live, models.EntitlementActive, true},
		{"RENEWAL_EXTENDED", "", live, models.EntitlementActive, true},
		{"DID_FAIL_TO_RENEW", "GRACE_PERIOD", live, models.EntitlementGrace, true},
		{"DID_FAIL_TO_RENEW", "", live, models.EntitlementBillingRetry, true},
		{"GRACE_PERIOD_EXPIRED", "", live, models.EntitlementExpired, true},
		{"EXPIRED", "VOLUNTARY", live, models.EntitlementExpired, true},
		{"REFUND", "", live, models.EntitlementRevoked, true},
		{"REVOKE", "", live, models.EntitlementRevoked, true},
		{"DID_CHANGE_RENEWAL_STATUS", "AUTO_RENEW_DISABLED", live, "", false},
		{"CONSUMPTION_REQUEST", "", live, "", false},
		{"PRICE_INCREASE", "", live, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.typ+"/"+tc.subtype, func(t *testing.T) {
			got, applicable := entitlementForNotification(tc.typ, tc.subtype, tc.tx, now)
			if applicable != tc.wantApplicable || (applicable && got != tc.want) {
				t.Fatalf("got (%s, %v) want (%s, %v)", got, applicable, tc.want, tc.wantApplicable)
			}
		})
	}
}
