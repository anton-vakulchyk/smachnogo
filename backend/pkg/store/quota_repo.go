package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// ErrQuotaExceeded maps to 429 RATE_LIMITED at the API edge.
var ErrQuotaExceeded = errors.New("store: daily quota exceeded")

const quotaTTLSeconds = 48 * 3600

type QuotaKind string

const (
	QuotaScans     QuotaKind = "scans"
	QuotaEstimates QuotaKind = "estimates"
)

// Consume atomically increments the day's counter, failing when at cap.
// One conditional UpdateItem — race-safe under concurrent requests.
func (s *Store) Consume(ctx context.Context, userID, date string, kind QuotaKind, cap int, nowEpoch int64) error {
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &s.table,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: UserPK(userID)},
			"SK": &types.AttributeValueMemberS{Value: QuotaSK(date)},
		},
		UpdateExpression:    aws.String("ADD #k :one SET expires_at = if_not_exists(expires_at, :exp)"),
		ConditionExpression: aws.String("attribute_not_exists(#k) OR #k < :cap"),
		ExpressionAttributeNames: map[string]string{
			"#k": string(kind),
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":one": &types.AttributeValueMemberN{Value: "1"},
			":cap": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", cap)},
			":exp": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", nowEpoch+quotaTTLSeconds)},
		},
	})
	if err != nil {
		var ccf *types.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrQuotaExceeded
		}
		return fmt.Errorf("consume quota: %w", err)
	}
	return nil
}

// refundTransactItems returns the transaction pieces for a scan-quota refund:
// (1) flip quota_refunded on the scan (the idempotency guard — only the
// winner of this conditional decrements anything), (2) decrement the day
// counter. M7 appends a free_scans_used decrement to the same transaction.
func (s *Store) refundTransactItems(userID, scanID, date string) []types.TransactWriteItem {
	return []types.TransactWriteItem{
		{
			Update: &types.Update{
				TableName: &s.table,
				Key: map[string]types.AttributeValue{
					"PK": &types.AttributeValueMemberS{Value: UserPK(userID)},
					"SK": &types.AttributeValueMemberS{Value: ScanSK(scanID)},
				},
				UpdateExpression:    aws.String("SET quota_refunded = :true"),
				ConditionExpression: aws.String("attribute_exists(PK) AND quota_refunded = :false"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":true":  &types.AttributeValueMemberBOOL{Value: true},
					":false": &types.AttributeValueMemberBOOL{Value: false},
				},
			},
		},
		{
			Update: &types.Update{
				TableName: &s.table,
				Key: map[string]types.AttributeValue{
					"PK": &types.AttributeValueMemberS{Value: UserPK(userID)},
					"SK": &types.AttributeValueMemberS{Value: QuotaSK(date)},
				},
				UpdateExpression:    aws.String("ADD scans :neg"),
				ConditionExpression: aws.String("attribute_exists(PK) AND scans > :zero"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":neg":  &types.AttributeValueMemberN{Value: "-1"},
					":zero": &types.AttributeValueMemberN{Value: "0"},
				},
			},
		},
	}
}

// RefundScanQuota refunds one scan consumption (FAILED or not-food
// terminal states). Idempotent: a second call loses the quota_refunded
// condition and is a no-op.
func (s *Store) RefundScanQuota(ctx context.Context, userID, scanID, date string) error {
	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: s.refundTransactItems(userID, scanID, date),
	})
	if err != nil {
		var canceled *types.TransactionCanceledException
		if errors.As(err, &canceled) {
			// Already refunded (flag condition) or counter at zero — both fine.
			return nil
		}
		return fmt.Errorf("refund scan quota: %w", err)
	}
	return nil
}
