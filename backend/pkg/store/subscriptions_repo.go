package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const notificationDedupTTL = 30 * 24 * time.Hour

// MarkNotificationProcessed records an App Store notificationUUID; a
// duplicate delivery loses the conditional put and returns ErrAlreadyExists
// so the webhook acks without re-applying.
func (s *Store) MarkNotificationProcessed(ctx context.Context, notificationUUID string, now time.Time) error {
	_, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &s.table,
		Item: map[string]types.AttributeValue{
			"PK":           &types.AttributeValueMemberS{Value: NotifPK(notificationUUID)},
			"SK":           &types.AttributeValueMemberS{Value: MetaSK()},
			"processed_at": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", now.Unix())},
			"expires_at":   &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", now.Add(notificationDedupTTL).Unix())},
		},
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		var ccf *types.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrAlreadyExists
		}
		return fmt.Errorf("mark notification: %w", err)
	}
	return nil
}

// NotificationProcessed reports whether this notificationUUID was already
// applied — the webhook's early-exit read. The conditional put in
// MarkNotificationProcessed remains the race-safe authority.
func (s *Store) NotificationProcessed(ctx context.Context, notificationUUID string) (bool, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.table,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: NotifPK(notificationUUID)},
			"SK": &types.AttributeValueMemberS{Value: MetaSK()},
		},
		ProjectionExpression: aws.String("PK"),
	})
	if err != nil {
		return false, fmt.Errorf("notification processed check: %w", err)
	}
	return len(out.Item) > 0, nil
}

// ClaimTransaction points an Apple originalTransactionId at userID and
// returns the previous owner ("" when none or unchanged). Latest claim wins
// — one active user per subscription bounds Apple-ID sharing and makes
// restore-on-new-device a transfer, not a duplication.
func (s *Store) ClaimTransaction(ctx context.Context, originalTxnID, userID string, now time.Time) (previousOwner string, err error) {
	out, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &s.table,
		Item: map[string]types.AttributeValue{
			"PK":         &types.AttributeValueMemberS{Value: TxnPK(originalTxnID)},
			"SK":         &types.AttributeValueMemberS{Value: MetaSK()},
			"user_id":    &types.AttributeValueMemberS{Value: userID},
			"claimed_at": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", now.Unix())},
		},
		ReturnValues: types.ReturnValueAllOld,
	})
	if err != nil {
		return "", fmt.Errorf("claim transaction: %w", err)
	}
	if prev, ok := out.Attributes["user_id"].(*types.AttributeValueMemberS); ok && prev.Value != userID {
		return prev.Value, nil
	}
	return "", nil
}

// TransactionOwner resolves the user owning an originalTransactionId, or ""
// when unclaimed.
func (s *Store) TransactionOwner(ctx context.Context, originalTxnID string) (string, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.table,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: TxnPK(originalTxnID)},
			"SK": &types.AttributeValueMemberS{Value: MetaSK()},
		},
		ProjectionExpression: aws.String("user_id"),
	})
	if err != nil {
		return "", fmt.Errorf("get transaction owner: %w", err)
	}
	if v, ok := out.Item["user_id"].(*types.AttributeValueMemberS); ok {
		return v.Value, nil
	}
	return "", nil
}
