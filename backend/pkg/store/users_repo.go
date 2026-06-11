package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"smachnogo/pkg/models"
)

// PaywallError maps to 402 PAYWALL at the API edge; Reason is one of the
// models.Paywall* constants and drives the client's paywall copy variant.
type PaywallError struct{ Reason string }

func (e *PaywallError) Error() string { return "store: free allowance unavailable: " + e.Reason }

// ErrStaleEntitlement: the update carried an older signedDate than the
// stored state — out-of-order webhook delivery, drop it.
var ErrStaleEntitlement = errors.New("store: entitlement update is stale")

func (s *Store) profileKey(userID string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: UserPK(userID)},
		"SK": &types.AttributeValueMemberS{Value: ProfileSK()},
	}
}

// GetProfile returns the user's profile, or an empty (free, zero-scans)
// profile when the item doesn't exist yet — it's created lazily by the
// first ConsumeFreeScan / SetEntitlement.
func (s *Store) GetProfile(ctx context.Context, userID string) (*models.Profile, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.table,
		Key:       s.profileKey(userID),
	})
	if err != nil {
		return nil, fmt.Errorf("get profile: %w", err)
	}
	p := &models.Profile{}
	if len(out.Item) == 0 {
		return p, nil
	}
	if err := attributevalue.UnmarshalMap(out.Item, p); err != nil {
		return nil, fmt.Errorf("unmarshal profile: %w", err)
	}
	return p, nil
}

// ConsumeFreeScan atomically takes one unit of the free allowance, starting
// the window on first use. The condition mirrors Profile.FreeAllowance:
// inside the window AND under the cap. One conditional UpdateItem — the
// authoritative paywall decision, race-safe under concurrent requests; on
// failure the old item rides back in the exception so the reason costs no
// second read.
func (s *Store) ConsumeFreeScan(ctx context.Context, userID string, allowance int, window time.Duration, now time.Time) error {
	// The DDB condition's attribute_not_exists(free_scans_used) arm treats a
	// missing counter as under-cap — true for any cap ≥ 1, wrong for a zero
	// grant (DeviceCheck's device_already_used). Zero never consumes.
	if allowance <= 0 {
		return &PaywallError{Reason: models.PaywallScansExhausted}
	}
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &s.table,
		Key:       s.profileKey(userID),
		UpdateExpression: aws.String(
			"ADD free_scans_used :one " +
				"SET allowance_started_at = if_not_exists(allowance_started_at, :now), created_at = if_not_exists(created_at, :now)"),
		ConditionExpression: aws.String(
			"(attribute_not_exists(allowance_started_at) OR allowance_started_at > :cutoff) AND " +
				"(attribute_not_exists(free_scans_used) OR free_scans_used < :cap)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":one":    &types.AttributeValueMemberN{Value: "1"},
			":now":    &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", now.Unix())},
			":cutoff": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", now.Add(-window).Unix())},
			":cap":    &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", allowance)},
		},
		ReturnValuesOnConditionCheckFailure: types.ReturnValuesOnConditionCheckFailureAllOld,
	})
	if err != nil {
		var ccf *types.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			p := &models.Profile{}
			if len(ccf.Item) > 0 {
				_ = attributevalue.UnmarshalMap(ccf.Item, p)
			}
			_, reason := p.FreeAllowance(allowance, window, now)
			if reason == "" {
				// Condition failed but state says allowed — racing refund;
				// treat as exhausted, the client will retry.
				reason = models.PaywallScansExhausted
			}
			return &PaywallError{Reason: reason}
		}
		return fmt.Errorf("consume free scan: %w", err)
	}
	return nil
}

// UnconsumeScanCounters directly hands back counters taken by THIS request —
// the idempotent-create-retry path (both) and the free-took-but-daily-429'd
// path (free only). The consumption being reversed is this request's extra,
// not the scan's original (which the quota_refunded-guarded refund still
// owns). Floor-guarded decrements; never touches the refund flag.
func (s *Store) UnconsumeScanCounters(ctx context.Context, userID, date string, daily, free bool) error {
	dec := func(key map[string]types.AttributeValue, attr string) error {
		_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName:           &s.table,
			Key:                 key,
			UpdateExpression:    aws.String("ADD #a :neg"),
			ConditionExpression: aws.String("attribute_exists(PK) AND #a > :zero"),
			ExpressionAttributeNames: map[string]string{"#a": attr},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":neg":  &types.AttributeValueMemberN{Value: "-1"},
				":zero": &types.AttributeValueMemberN{Value: "0"},
			},
		})
		var ccf *types.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return nil // already at zero / missing — nothing to hand back
		}
		return err
	}
	if daily {
		quotaKey := map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: UserPK(userID)},
			"SK": &types.AttributeValueMemberS{Value: QuotaSK(date)},
		}
		if err := dec(quotaKey, "scans"); err != nil {
			return fmt.Errorf("unconsume daily scan: %w", err)
		}
	}
	if free {
		if err := dec(s.profileKey(userID), "free_scans_used"); err != nil {
			return fmt.Errorf("unconsume free scan: %w", err)
		}
	}
	return nil
}

// SetEntitlement applies a billing-state transition from the receipt
// endpoint or the App Store webhook. signedAtMS (Apple's signedDate) is the
// ordering authority: an update older than the stored one is dropped with
// ErrStaleEntitlement — duplicate and out-of-order notifications are normal.
func (s *Store) SetEntitlement(ctx context.Context, userID string, ent models.Entitlement, originalTxnID string, signedAtMS int64, now time.Time) error {
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &s.table,
		Key:       s.profileKey(userID),
		UpdateExpression: aws.String(
			"SET entitlement = :e, original_transaction_id = :o, entitlement_updated_at = :t, created_at = if_not_exists(created_at, :now)"),
		ConditionExpression: aws.String(
			"attribute_not_exists(entitlement_updated_at) OR entitlement_updated_at < :t"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":e":   &types.AttributeValueMemberS{Value: string(ent)},
			":o":   &types.AttributeValueMemberS{Value: originalTxnID},
			":t":   &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", signedAtMS)},
			":now": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", now.Unix())},
		},
	})
	if err != nil {
		var ccf *types.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrStaleEntitlement
		}
		return fmt.Errorf("set entitlement: %w", err)
	}
	return nil
}

// GetDailyScans reads today's consumed scan count (the /users/me indicator).
func (s *Store) GetDailyScans(ctx context.Context, userID, date string) (int, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.table,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: UserPK(userID)},
			"SK": &types.AttributeValueMemberS{Value: QuotaSK(date)},
		},
		ProjectionExpression: aws.String("scans"),
	})
	if err != nil {
		return 0, fmt.Errorf("get daily scans: %w", err)
	}
	var q struct {
		Scans int `dynamodbav:"scans"`
	}
	if len(out.Item) == 0 {
		return 0, nil
	}
	if err := attributevalue.UnmarshalMap(out.Item, &q); err != nil {
		return 0, fmt.Errorf("unmarshal quota: %w", err)
	}
	return q.Scans, nil
}
