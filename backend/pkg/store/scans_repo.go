package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"smachnogo/pkg/models"
)

var (
	ErrScanNotFound    = errors.New("store: scan not found")
	ErrAlreadyExists   = errors.New("store: item already exists")
	ErrAlreadyTerminal = errors.New("store: scan already terminal")
	ErrWrongState      = errors.New("store: wrong scan state")
)

func scanKey(userID, scanID string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: UserPK(userID)},
		"SK": &types.AttributeValueMemberS{Value: ScanSK(scanID)},
	}
}

// CreateScan conditionally creates a PENDING_UPLOAD scan. Returns
// ErrAlreadyExists when the scan_id was seen before (caller re-reads and
// answers idempotently). Maps are initialized empty so later
// `SET map.#idx` updates never hit a missing parent.
func (s *Store) CreateScan(ctx context.Context, userID string, scan *models.Scan) error {
	item, err := attributevalue.MarshalMap(scan)
	if err != nil {
		return fmt.Errorf("marshal scan: %w", err)
	}
	item["PK"] = &types.AttributeValueMemberS{Value: UserPK(userID)}
	item["SK"] = &types.AttributeValueMemberS{Value: ScanSK(scan.ScanID)}
	item["refinements"] = &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{}}
	item["confirmed_dishes"] = &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{}}

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
		return fmt.Errorf("create scan: %w", err)
	}
	return nil
}

func (s *Store) GetScan(ctx context.Context, userID, scanID string) (*models.Scan, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      &s.table,
		Key:            scanKey(userID, scanID),
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("get scan: %w", err)
	}
	if out.Item == nil {
		return nil, ErrScanNotFound
	}
	var scan models.Scan
	if err := attributevalue.UnmarshalMap(out.Item, &scan); err != nil {
		return nil, fmt.Errorf("unmarshal scan: %w", err)
	}
	return &scan, nil
}

// TransitionToQueued moves PENDING_UPLOAD → QUEUED. Reports whether THIS
// call made the transition (only the winner enqueues — confirm-upload
// retries must not re-enqueue).
func (s *Store) TransitionToQueued(ctx context.Context, userID, scanID string, now time.Time) (bool, error) {
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           &s.table,
		Key:                 scanKey(userID, scanID),
		UpdateExpression:    aws.String("SET #st = :queued, updated_at = :now"),
		ConditionExpression: aws.String("attribute_exists(PK) AND #st = :pending"),
		ExpressionAttributeNames: map[string]string{
			"#st": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":queued":  &types.AttributeValueMemberS{Value: string(models.ScanStatusQueued)},
			":pending": &types.AttributeValueMemberS{Value: string(models.ScanStatusPendingUpload)},
			":now":     mustMarshalTime(now),
		},
	})
	if err != nil {
		var ccf *types.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return false, nil // already queued/processed — idempotent success
		}
		return false, fmt.Errorf("transition to queued: %w", err)
	}
	return true, nil
}

// SetProcessing is cosmetic UI state. It must never stomp a terminal state
// on duplicate delivery, hence the condition; conditional failure is ignored.
func (s *Store) SetProcessing(ctx context.Context, userID, scanID string, now time.Time) {
	_, _ = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           &s.table,
		Key:                 scanKey(userID, scanID),
		UpdateExpression:    aws.String("SET #st = :processing, updated_at = :now"),
		ConditionExpression: aws.String("attribute_exists(PK) AND #st <> :ready AND #st <> :failed"),
		ExpressionAttributeNames: map[string]string{
			"#st": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":processing": &types.AttributeValueMemberS{Value: string(models.ScanStatusProcessing)},
			":ready":      &types.AttributeValueMemberS{Value: string(models.ScanStatusReady)},
			":failed":     &types.AttributeValueMemberS{Value: string(models.ScanStatusFailed)},
			":now":        mustMarshalTime(now),
		},
	})
}

// WriteResult attaches the analysis and flips to READY. The result is
// immutable: the condition loses on duplicate delivery (ErrAlreadyTerminal —
// ack and exit). Dish identity (scan_id + index) feeds meal IDs, so a
// reprocess must never swap dish arrays under a user mid-selection.
func (s *Store) WriteResult(ctx context.Context, userID, scanID string, analysis *models.PhotoAnalysis, provider, model string, tokensIn, tokensOut int, now time.Time) error {
	resultAV, err := attributevalue.Marshal(analysis)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &s.table,
		Key:       scanKey(userID, scanID),
		UpdateExpression: aws.String(
			"SET #st = :ready, #res = :res, result_version = :rv, analysis_provider = :prov, analysis_model = :model, tokens_in = :tin, tokens_out = :tout, updated_at = :now"),
		ConditionExpression: aws.String("attribute_exists(PK) AND attribute_not_exists(#res) AND #st <> :failed"),
		ExpressionAttributeNames: map[string]string{
			"#st":  "status",
			"#res": "result",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":ready":  &types.AttributeValueMemberS{Value: string(models.ScanStatusReady)},
			":failed": &types.AttributeValueMemberS{Value: string(models.ScanStatusFailed)},
			":res":    resultAV,
			":rv":     &types.AttributeValueMemberN{Value: strconv.Itoa(models.ResultVersion)},
			":prov":   &types.AttributeValueMemberS{Value: provider},
			":model":  &types.AttributeValueMemberS{Value: model},
			":tin":    &types.AttributeValueMemberN{Value: strconv.Itoa(tokensIn)},
			":tout":   &types.AttributeValueMemberN{Value: strconv.Itoa(tokensOut)},
			":now":    mustMarshalTime(now),
		},
	})
	if err != nil {
		var ccf *types.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrAlreadyTerminal
		}
		return fmt.Errorf("write result: %w", err)
	}
	return nil
}

// WriteFailure flips to FAILED with a reason. Loses (silently) to an
// existing terminal state.
func (s *Store) WriteFailure(ctx context.Context, userID, scanID string, reason models.FailureReason, now time.Time) error {
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           &s.table,
		Key:                 scanKey(userID, scanID),
		UpdateExpression:    aws.String("SET #st = :failed, failure_reason = :reason, updated_at = :now"),
		ConditionExpression: aws.String("attribute_exists(PK) AND attribute_not_exists(#res) AND #st <> :failed"),
		ExpressionAttributeNames: map[string]string{
			"#st":  "status",
			"#res": "result",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":failed": &types.AttributeValueMemberS{Value: string(models.ScanStatusFailed)},
			":reason": &types.AttributeValueMemberS{Value: string(reason)},
			":now":    mustMarshalTime(now),
		},
	})
	if err != nil {
		var ccf *types.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrAlreadyTerminal
		}
		return fmt.Errorf("write failure: %w", err)
	}
	return nil
}

// ConfirmDish atomically creates the meal AND records the dish index on the
// scan item. The scan-side condition is the dedup authority (meal-side
// conditions alone leak across dates when a retry carries a corrected date).
// Returns ErrAlreadyExists when the index was confirmed before — caller
// fetches the recorded meal and returns it as success (additive semantics).
func (s *Store) ConfirmDish(ctx context.Context, userID string, scanID string, dishIndex int, meal *models.Meal) error {
	mealItem, err := attributevalue.MarshalMap(meal)
	if err != nil {
		return fmt.Errorf("marshal meal: %w", err)
	}
	mealItem["PK"] = &types.AttributeValueMemberS{Value: UserPK(userID)}
	mealItem["SK"] = &types.AttributeValueMemberS{Value: MealSK(meal.Date, meal.MealID)}

	confirmedAV, err := attributevalue.Marshal(models.ConfirmedDish{MealID: meal.MealID, Date: meal.Date})
	if err != nil {
		return fmt.Errorf("marshal confirmed dish: %w", err)
	}

	idx := strconv.Itoa(dishIndex)
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{
				Update: &types.Update{
					TableName:           &s.table,
					Key:                 scanKey(userID, scanID),
					UpdateExpression:    aws.String("SET confirmed_dishes.#idx = :cd, updated_at = :now"),
					ConditionExpression: aws.String("attribute_exists(PK) AND attribute_not_exists(confirmed_dishes.#idx)"),
					ExpressionAttributeNames: map[string]string{
						"#idx": idx,
					},
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":cd":  confirmedAV,
						":now": mustMarshalTime(meal.CreatedAt),
					},
				},
			},
			{
				Put: &types.Put{
					TableName: &s.table,
					Item:      mealItem,
				},
			},
		},
	})
	if err != nil {
		var canceled *types.TransactionCanceledException
		if errors.As(err, &canceled) {
			return ErrAlreadyExists
		}
		return fmt.Errorf("confirm dish: %w", err)
	}
	return nil
}

// WriteRefinement stores (or replaces — last answer wins) the revised dish
// for one index. Condition: scan exists, the index is not yet confirmed,
// and the immutable result is present (READY).
func (s *Store) WriteRefinement(ctx context.Context, userID, scanID string, dishIndex int, dish models.Dish, now time.Time) error {
	dishAV, err := attributevalue.Marshal(dish)
	if err != nil {
		return fmt.Errorf("marshal refined dish: %w", err)
	}
	idx := strconv.Itoa(dishIndex)
	_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           &s.table,
		Key:                 scanKey(userID, scanID),
		UpdateExpression:    aws.String("SET refinements.#idx = :dish, updated_at = :now"),
		ConditionExpression: aws.String("attribute_exists(PK) AND attribute_exists(#res) AND attribute_not_exists(confirmed_dishes.#idx)"),
		ExpressionAttributeNames: map[string]string{
			"#idx": idx,
			"#res": "result",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":dish": dishAV,
			":now":  mustMarshalTime(now),
		},
	})
	if err != nil {
		var ccf *types.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrWrongState
		}
		return fmt.Errorf("write refinement: %w", err)
	}
	return nil
}

func mustMarshalTime(t time.Time) types.AttributeValue {
	av, err := attributevalue.Marshal(t)
	if err != nil {
		return &types.AttributeValueMemberS{Value: t.UTC().Format(time.RFC3339)}
	}
	return av
}
