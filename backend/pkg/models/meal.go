package models

import "time"

type MealState string

const (
	MealStateLogged  MealState = "logged"
	MealStatePlanned MealState = "planned"
)

type MealSource string

const (
	MealSourceScan   MealSource = "scan"
	MealSourceText   MealSource = "text"
	MealSourceManual MealSource = "manual"
	MealSourceReadd  MealSource = "readd"
)

// Meal is one diary entry. SchemaVersion pins the stored shape so future
// struct changes can detect old items instead of silently zero-filling.
type Meal struct {
	MealID     string     `json:"meal_id" dynamodbav:"meal_id"`
	Date       string     `json:"date" dynamodbav:"date"` // YYYY-MM-DD, device-local, opaque to the server
	State      MealState  `json:"state" dynamodbav:"state"`
	ConsumedAt string     `json:"consumed_at" dynamodbav:"consumed_at"` // ISO8601 with offset
	Label      string     `json:"label" dynamodbav:"label"`
	Source     MealSource `json:"source" dynamodbav:"source"`
	Nutrients
	Scores
	PortionFactor    float64        `json:"portion_factor" dynamodbav:"portion_factor"` // 1.0 default; PATCH rescales from base
	Refined          bool           `json:"refined" dynamodbav:"refined"`
	RefinementAnswer string         `json:"refinement_answer" dynamodbav:"refinement_answer"` // denormalized: scans TTL out, meals are the durable copy
	Components       []EstimateItem `json:"components,omitempty" dynamodbav:"components,omitempty"`
	ScanID           string         `json:"scan_id,omitempty" dynamodbav:"scan_id,omitempty"`
	DishIndex        *int           `json:"dish_index,omitempty" dynamodbav:"dish_index,omitempty"`
	PhotoS3Key       string         `json:"photo_s3_key,omitempty" dynamodbav:"photo_s3_key,omitempty"`
	SchemaVersion    int            `json:"schema_version" dynamodbav:"schema_version"`
	CreatedAt        time.Time      `json:"created_at" dynamodbav:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at" dynamodbav:"updated_at"`
}

const MealSchemaVersion = 1
