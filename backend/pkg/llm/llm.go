// Package llm owns the provider-agnostic analysis contract. The canonical
// JSON schemas live in pkg/llm/schema; providers (anthropic now, gemini
// later) translate them into their native structured-output format. Swapping
// providers is a new adapter + env var — nothing else changes.
package llm

import (
	"context"
	"errors"

	"smachnogo/pkg/models"
)

// Usage is per-call token accounting — the observability spine. The M7
// model bake-off ("quality-per-dollar") and whale detection are fiction
// without it, which is why it is part of the interface from day one.
type Usage struct {
	InputTokens  int
	OutputTokens int
	LatencyMS    int64
}

type Analyzer interface {
	AnalyzePhoto(ctx context.Context, jpeg []byte) (*models.PhotoAnalysis, Usage, error)
	EstimateText(ctx context.Context, text string) (*models.TextEstimate, Usage, error)
	// RefineDish re-estimates one dish given the user's clarification answer.
	// In the interface from M1 (stubbed) so M4 is an implementation, not an
	// interface retrofit. The revised dish must carry clarification fields
	// forced false/empty (no question loops).
	RefineDish(ctx context.Context, dish models.Dish, answer string) (*models.Dish, Usage, error)
}

// Error classification drives the worker's SQS decision: retryable errors
// return the message to the queue (redelivery/backoff), terminal errors mark
// the scan FAILED and delete the message.
var (
	ErrRetryable = errors.New("llm: retryable")
	ErrTerminal  = errors.New("llm: terminal")
)

func IsRetryable(err error) bool { return errors.Is(err, ErrRetryable) }
func IsTerminal(err error) bool  { return errors.Is(err, ErrTerminal) }
