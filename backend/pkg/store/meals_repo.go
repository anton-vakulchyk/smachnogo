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

var ErrMealNotFound = errors.New("store: meal not found")

// CreateMeal is the single write path for non-scan meals (manual, text
// save, re-add). Conditional on the composite key: a retried save with the
// same client-generated meal_id can't double-log.
func (s *Store) CreateMeal(ctx context.Context, userID string, meal *models.Meal) error {
	item, err := attributevalue.MarshalMap(meal)
	if err != nil {
		return fmt.Errorf("marshal meal: %w", err)
	}
	item["PK"] = &types.AttributeValueMemberS{Value: UserPK(userID)}
	item["SK"] = &types.AttributeValueMemberS{Value: MealSK(meal.Date, meal.MealID)}

	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           &s.table,
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		var ccf *types.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrAlreadyExists
		}
		return fmt.Errorf("create meal: %w", err)
	}
	return nil
}

func (s *Store) GetMeal(ctx context.Context, userID, date, mealID string) (*models.Meal, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.table,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: UserPK(userID)},
			"SK": &types.AttributeValueMemberS{Value: MealSK(date, mealID)},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("get meal: %w", err)
	}
	if out.Item == nil {
		return nil, ErrMealNotFound
	}
	var meal models.Meal
	if err := attributevalue.UnmarshalMap(out.Item, &meal); err != nil {
		return nil, fmt.Errorf("unmarshal meal: %w", err)
	}
	return &meal, nil
}

// ListMealsRange returns all meals with date in [from, to], chronologically
// (lexicographic SK order). Pages through LastEvaluatedKey as hygiene even
// though ranges at this scale fit one page.
func (s *Store) ListMealsRange(ctx context.Context, userID, from, to string) ([]models.Meal, error) {
	lo, hi := MealSKRange(from, to)
	var meals []models.Meal
	var startKey map[string]types.AttributeValue
	for {
		out, err := s.db.Query(ctx, &dynamodb.QueryInput{
			TableName:              &s.table,
			KeyConditionExpression: aws.String("PK = :pk AND SK BETWEEN :lo AND :hi"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": &types.AttributeValueMemberS{Value: UserPK(userID)},
				":lo": &types.AttributeValueMemberS{Value: lo},
				":hi": &types.AttributeValueMemberS{Value: hi},
			},
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return nil, fmt.Errorf("query meals: %w", err)
		}
		page := make([]models.Meal, 0, len(out.Items))
		if err := attributevalue.UnmarshalListOfMaps(out.Items, &page); err != nil {
			return nil, fmt.Errorf("unmarshal meals: %w", err)
		}
		meals = append(meals, page...)
		if out.LastEvaluatedKey == nil {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	return meals, nil
}
