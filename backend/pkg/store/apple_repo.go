package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// GetAppleLink resolves the user owning an Apple sub, or "" when unlinked.
func (s *Store) GetAppleLink(ctx context.Context, appleSub string) (string, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.table,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: ApplePK(appleSub)},
			"SK": &types.AttributeValueMemberS{Value: MetaSK()},
		},
		ProjectionExpression: aws.String("user_id"),
	})
	if err != nil {
		return "", fmt.Errorf("get apple link: %w", err)
	}
	if v, ok := out.Item["user_id"].(*types.AttributeValueMemberS); ok {
		return v.Value, nil
	}
	return "", nil
}

// PutAppleLink points an Apple sub at a user (link and repoint are the same
// unconditional write — recovery's latest-device-wins) and stamps apple_sub
// on the user's profile.
func (s *Store) PutAppleLink(ctx context.Context, appleSub, userID string, now time.Time) error {
	_, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &s.table,
		Item: map[string]types.AttributeValue{
			"PK":        &types.AttributeValueMemberS{Value: ApplePK(appleSub)},
			"SK":        &types.AttributeValueMemberS{Value: MetaSK()},
			"user_id":   &types.AttributeValueMemberS{Value: userID},
			"linked_at": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", now.Unix())},
		},
	})
	if err != nil {
		return fmt.Errorf("put apple link: %w", err)
	}
	_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &s.table,
		Key:       s.profileKey(userID),
		UpdateExpression: aws.String(
			"SET apple_sub = :s, created_at = if_not_exists(created_at, :now)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":s":   &types.AttributeValueMemberS{Value: appleSub},
			":now": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", now.Unix())},
		},
	})
	if err != nil {
		return fmt.Errorf("stamp profile apple_sub: %w", err)
	}
	return nil
}

// CopyUserData copies the durable items (meals + profile) from one user
// partition to another — the M8 recovery move. Scans and quota items are
// deliberately skipped: both are transient (TTL'd) and scan photos live
// under the OLD user's S3 prefix, which the caller deletes after the copy.
// Idempotent: re-running overwrites the same keys with the same values.
func (s *Store) CopyUserData(ctx context.Context, fromUserID, toUserID string) (int, error) {
	var (
		copied  int
		lastKey map[string]types.AttributeValue
		batch   []types.WriteRequest
	)
	flush := func() error {
		for len(batch) > 0 {
			n := len(batch)
			if n > 25 {
				n = 25
			}
			out, err := s.db.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
				RequestItems: map[string][]types.WriteRequest{s.table: batch[:n]},
			})
			if err != nil {
				return fmt.Errorf("batch write: %w", err)
			}
			rest := batch[n:]
			if unp := out.UnprocessedItems[s.table]; len(unp) > 0 {
				rest = append(unp, rest...) // retry throttled writes
			}
			batch = rest
		}
		return nil
	}

	for {
		out, err := s.db.Query(ctx, &dynamodb.QueryInput{
			TableName:              &s.table,
			KeyConditionExpression: aws.String("PK = :pk"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": &types.AttributeValueMemberS{Value: UserPK(fromUserID)},
			},
			ExclusiveStartKey: lastKey,
		})
		if err != nil {
			return copied, fmt.Errorf("query source partition: %w", err)
		}
		for _, item := range out.Items {
			sk, ok := item["SK"].(*types.AttributeValueMemberS)
			if !ok {
				continue
			}
			if sk.Value != ProfileSK() && !strings.HasPrefix(sk.Value, skMealPrefix) {
				continue
			}
			clone := make(map[string]types.AttributeValue, len(item))
			for k, v := range item {
				clone[k] = v
			}
			clone["PK"] = &types.AttributeValueMemberS{Value: UserPK(toUserID)}
			batch = append(batch, types.WriteRequest{PutRequest: &types.PutRequest{Item: clone}})
			copied++
		}
		if err := flush(); err != nil {
			return copied, err
		}
		if out.LastEvaluatedKey == nil {
			break
		}
		lastKey = out.LastEvaluatedKey
	}
	return copied, nil
}
