package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"smachnogo/pkg/models"
)

// ReplaceMeal writes the full updated meal at the same key (read-modify-
// write; the handler owns merge semantics). Condition: the meal must exist.
func (s *Store) ReplaceMeal(ctx context.Context, userID string, meal *models.Meal) error {
	item, err := attributevalue.MarshalMap(meal)
	if err != nil {
		return fmt.Errorf("marshal meal: %w", err)
	}
	item["PK"] = &types.AttributeValueMemberS{Value: UserPK(userID)}
	item["SK"] = &types.AttributeValueMemberS{Value: MealSK(meal.Date, meal.MealID)}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           &s.table,
		Item:                item,
		ConditionExpression: aws.String("attribute_exists(PK)"),
	})
	if err != nil {
		var ccf *types.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrMealNotFound
		}
		return fmt.Errorf("replace meal: %w", err)
	}
	return nil
}

// MoveMeal re-keys a meal to a new date in one transaction: put at the new
// SK (must not exist) + delete the old SK (must exist).
func (s *Store) MoveMeal(ctx context.Context, userID string, meal *models.Meal, oldDate string) error {
	item, err := attributevalue.MarshalMap(meal)
	if err != nil {
		return fmt.Errorf("marshal meal: %w", err)
	}
	item["PK"] = &types.AttributeValueMemberS{Value: UserPK(userID)}
	item["SK"] = &types.AttributeValueMemberS{Value: MealSK(meal.Date, meal.MealID)}

	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{
				Put: &types.Put{
					TableName:           &s.table,
					Item:                item,
					ConditionExpression: aws.String("attribute_not_exists(PK)"),
				},
			},
			{
				Delete: &types.Delete{
					TableName: &s.table,
					Key: map[string]types.AttributeValue{
						"PK": &types.AttributeValueMemberS{Value: UserPK(userID)},
						"SK": &types.AttributeValueMemberS{Value: MealSK(oldDate, meal.MealID)},
					},
					ConditionExpression: aws.String("attribute_exists(PK)"),
				},
			},
		},
	})
	if err != nil {
		var canceled *types.TransactionCanceledException
		if errors.As(err, &canceled) {
			return fmt.Errorf("%w: move conflict (target exists or source missing)", ErrMealNotFound)
		}
		return fmt.Errorf("move meal: %w", err)
	}
	return nil
}

func (s *Store) DeleteMeal(ctx context.Context, userID, date, mealID string) error {
	_, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &s.table,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: UserPK(userID)},
			"SK": &types.AttributeValueMemberS{Value: MealSK(date, mealID)},
		},
		ConditionExpression: aws.String("attribute_exists(PK)"),
	})
	if err != nil {
		var ccf *types.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrMealNotFound
		}
		return fmt.Errorf("delete meal: %w", err)
	}
	return nil
}

// DeleteUserData removes EVERY item in the user's partition (meals, scans,
// quotas, profile) — the account-deletion cascade. Returns the number of
// items deleted.
func (s *Store) DeleteUserData(ctx context.Context, userID string) (int, error) {
	deleted := 0
	var startKey map[string]types.AttributeValue
	for {
		out, err := s.db.Query(ctx, &dynamodb.QueryInput{
			TableName:              &s.table,
			KeyConditionExpression: aws.String("PK = :pk"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": &types.AttributeValueMemberS{Value: UserPK(userID)},
			},
			ProjectionExpression: aws.String("PK, SK"),
			ExclusiveStartKey:    startKey,
		})
		if err != nil {
			return deleted, fmt.Errorf("query partition: %w", err)
		}
		// BatchWrite in chunks of 25 with unprocessed-item retry.
		for chunk := 0; chunk < len(out.Items); chunk += 25 {
			end := min(chunk+25, len(out.Items))
			reqs := make([]types.WriteRequest, 0, end-chunk)
			for _, item := range out.Items[chunk:end] {
				reqs = append(reqs, types.WriteRequest{
					DeleteRequest: &types.DeleteRequest{Key: map[string]types.AttributeValue{
						"PK": item["PK"], "SK": item["SK"],
					}},
				})
			}
			pending := map[string][]types.WriteRequest{s.table: reqs}
			for attempt := 0; len(pending[s.table]) > 0 && attempt < 5; attempt++ {
				resp, err := s.db.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{RequestItems: pending})
				if err != nil {
					return deleted, fmt.Errorf("batch delete: %w", err)
				}
				done := len(pending[s.table]) - len(resp.UnprocessedItems[s.table])
				deleted += done
				pending = resp.UnprocessedItems
			}
			if len(pending[s.table]) > 0 {
				return deleted, fmt.Errorf("batch delete: unprocessed items remain")
			}
		}
		if out.LastEvaluatedKey == nil {
			return deleted, nil
		}
		startKey = out.LastEvaluatedKey
	}
}
