package models

import "time"

type ScanStatus string

const (
	ScanStatusPendingUpload ScanStatus = "PENDING_UPLOAD"
	ScanStatusQueued        ScanStatus = "QUEUED"
	ScanStatusProcessing    ScanStatus = "PROCESSING" // cosmetic — worker sets it unconditionally
	ScanStatusReady         ScanStatus = "READY"
	ScanStatusFailed        ScanStatus = "FAILED"
)

type FailureReason string

const (
	FailNoImage         FailureReason = "no_image"
	FailImageUnreadable FailureReason = "image_unreadable"
	FailNotProcessable  FailureReason = "not_processable"
	FailImplausible     FailureReason = "analysis_implausible"
	FailInternal        FailureReason = "internal"
)

// ConfirmedDish records which meal a dish index produced — the dedup
// authority for additive confirms (meal-side conditional puts alone leak
// across dates when a retry carries a corrected date).
type ConfirmedDish struct {
	MealID string `json:"meal_id" dynamodbav:"meal_id"`
	Date   string `json:"date" dynamodbav:"date"`
}

// Scan is one photo-analysis job. Result is immutable once READY; user
// corrections live in Refinements keyed by dish index.
type Scan struct {
	ScanID          string                   `json:"scan_id" dynamodbav:"scan_id"`
	Status          ScanStatus               `json:"status" dynamodbav:"status"`
	S3Key           string                   `json:"-" dynamodbav:"s3_key"`
	Result          *PhotoAnalysis           `json:"result,omitempty" dynamodbav:"result,omitempty"`
	ResultVersion   int                      `json:"result_version,omitempty" dynamodbav:"result_version,omitempty"`
	Refinements     map[string]Dish          `json:"refinements,omitempty" dynamodbav:"refinements,omitempty"` // key: dish index as string
	ConfirmedDishes map[string]ConfirmedDish `json:"confirmed_dishes,omitempty" dynamodbav:"confirmed_dishes,omitempty"`
	FailureReason   FailureReason            `json:"failure_reason,omitempty" dynamodbav:"failure_reason,omitempty"`
	QuotaRefunded   bool                     `json:"-" dynamodbav:"quota_refunded"`
	// AllowanceConsumed records whether this scan ate a free-allowance unit
	// at create — the refund-decision authority (an entitlement read at
	// refund time would mis-handle a user who subscribed in between).
	AllowanceConsumed bool `json:"-" dynamodbav:"allowance_consumed,omitempty"`
	Provider        string                   `json:"-" dynamodbav:"analysis_provider,omitempty"`
	Model           string                   `json:"-" dynamodbav:"analysis_model,omitempty"`
	TokensIn        int                      `json:"-" dynamodbav:"tokens_in,omitempty"`
	TokensOut       int                      `json:"-" dynamodbav:"tokens_out,omitempty"`
	CreatedAt       time.Time                `json:"created_at" dynamodbav:"created_at"`
	UpdatedAt       time.Time                `json:"updated_at" dynamodbav:"updated_at"`
	ExpiresAt       int64                    `json:"-" dynamodbav:"expires_at"` // DDB TTL, epoch seconds
}

const ResultVersion = 1
