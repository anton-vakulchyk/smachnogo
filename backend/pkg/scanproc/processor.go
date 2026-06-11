// Package scanproc is the single scan-processing core, shared verbatim by
// the SQS worker Lambda and the API's LOCAL_SYNC seam — business logic is
// identical in both transports.
package scanproc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"smachnogo/pkg/awsx"
	"smachnogo/pkg/llm"
	"smachnogo/pkg/logging"
	"smachnogo/pkg/models"
	"smachnogo/pkg/store"
)

const maxDishes = 8

type Processor struct {
	Store    *store.Store
	S3       *awsx.S3
	Analyzer llm.Analyzer
	Provider string
	Model    string
}

// ErrRetry signals the caller (worker) to return the message to SQS.
var ErrRetry = errors.New("scanproc: retryable")

// Process runs one scan to a terminal state. Returning nil means the
// message can be deleted (including "already terminal" duplicates);
// ErrRetry means redeliver.
func (p *Processor) Process(ctx context.Context, userID, scanID string) error {
	log := logging.From(ctx).With(zap.String("user_id", userID), zap.String("scan_id", scanID))
	now := time.Now().UTC()

	scan, err := p.Store.GetScan(ctx, userID, scanID)
	if errors.Is(err, store.ErrScanNotFound) {
		log.Warn("scan not found — acking stale message")
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRetry, err)
	}
	if scan.Status == models.ScanStatusReady || scan.Status == models.ScanStatusFailed {
		log.Info("scan already terminal — duplicate delivery", zap.String("status", string(scan.Status)))
		return nil
	}

	p.Store.SetProcessing(ctx, userID, scanID, now)

	jpeg, err := p.S3.GetObject(ctx, scan.S3Key)
	if errors.Is(err, awsx.ErrNoObject) {
		return p.fail(ctx, log, userID, scanID, scan, models.FailNoImage)
	}
	if err != nil {
		// Size-cap violations are terminal; transport errors retryable.
		if isSizeError(err) {
			return p.fail(ctx, log, userID, scanID, scan, models.FailImageUnreadable)
		}
		return fmt.Errorf("%w: s3 get: %v", ErrRetry, err)
	}

	analysis, usage, err := p.analyzeWithPlausibilityRetry(ctx, log, jpeg)
	if err != nil {
		if llm.IsTerminal(err) {
			log.Warn("terminal analysis error", zap.Error(err))
			return p.fail(ctx, log, userID, scanID, scan, models.FailNotProcessable)
		}
		if errors.Is(err, errImplausible) {
			log.Warn("implausible analysis after retry")
			return p.fail(ctx, log, userID, scanID, scan, models.FailImplausible)
		}
		return fmt.Errorf("%w: analyze: %v", ErrRetry, err)
	}

	normalize(analysis)

	log.Info("analysis complete",
		zap.Bool("is_food", analysis.IsFood),
		zap.Int("dishes", len(analysis.Dishes)),
		zap.Int("tokens_in", usage.InputTokens),
		zap.Int("tokens_out", usage.OutputTokens),
		zap.Int64("latency_ms", usage.LatencyMS),
		zap.String("model", p.Model),
	)

	err = p.Store.WriteResult(ctx, userID, scanID, analysis, p.Provider, p.Model, usage.InputTokens, usage.OutputTokens, now)
	if errors.Is(err, store.ErrAlreadyTerminal) {
		log.Info("result already written — duplicate delivery raced")
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: write result: %v", ErrRetry, err)
	}

	// Not-food consumes no allowance: refund the quota consumed at create.
	if !analysis.IsFood {
		p.refund(ctx, log, userID, scanID, scan)
	}
	return nil
}

// FailScan marks a scan FAILED(internal) with quota refund — the DLQ
// consumer's path for messages that exhausted their retries. Idempotent:
// already-terminal scans are left alone.
func (p *Processor) FailScan(ctx context.Context, userID, scanID string) error {
	log := logging.From(ctx).With(zap.String("user_id", userID), zap.String("scan_id", scanID))
	scan, err := p.Store.GetScan(ctx, userID, scanID)
	if errors.Is(err, store.ErrScanNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if scan.Status == models.ScanStatusReady || scan.Status == models.ScanStatusFailed {
		return nil
	}
	return p.fail(ctx, log, userID, scanID, scan, models.FailInternal)
}

var errImplausible = errors.New("scanproc: implausible analysis")

// analyzeWithPlausibilityRetry runs the vision call, clamps, and gates on
// plausibility — one in-process retry, then errImplausible.
func (p *Processor) analyzeWithPlausibilityRetry(ctx context.Context, log *zap.Logger, jpeg []byte) (*models.PhotoAnalysis, llm.Usage, error) {
	var total llm.Usage
	for attempt := 0; attempt < 2; attempt++ {
		analysis, usage, err := p.Analyzer.AnalyzePhoto(ctx, jpeg)
		total.InputTokens += usage.InputTokens
		total.OutputTokens += usage.OutputTokens
		total.LatencyMS += usage.LatencyMS
		if err != nil {
			return nil, total, err
		}
		if reason := plausibilityIssue(analysis); reason != "" {
			log.Warn("plausibility gate", zap.String("issue", reason), zap.Int("attempt", attempt))
			continue
		}
		return analysis, total, nil
	}
	return nil, total, errImplausible
}

// plausibilityIssue returns "" for acceptable output. Catches schema-valid
// garbage: a 0-kcal pizza must not land in a diary the user trusts.
func plausibilityIssue(a *models.PhotoAnalysis) string {
	if !a.IsFood {
		return "" // nothing to validate
	}
	if len(a.Dishes) == 0 {
		return "is_food with no dishes"
	}
	allZero := true
	for i := range a.Dishes {
		d := &a.Dishes[i]
		d.Clamp()
		if d.CaloriesKcal > 0 {
			allZero = false
		}
		if d.PortionG <= 0 {
			return "non-positive portion_g"
		}
		// Macro-energy consistency: 4P + 9F + 4C should be within ~2.5x of
		// reported kcal (alcohol and fiber legitimately skew it).
		if d.CaloriesKcal > 50 {
			macroKcal := 4*d.ProteinG + 9*d.FatG + 4*d.CarbsG
			ratio := macroKcal / float64(d.CaloriesKcal)
			if ratio > 2.5 || ratio < 1/2.5 {
				return fmt.Sprintf("macro-energy mismatch ratio=%.2f", ratio)
			}
		}
	}
	if allZero {
		return "all dishes zero kcal"
	}
	return ""
}

// normalize applies post-gate normalization: dish cap + clamps (clamps are
// idempotent — already run inside the gate, run again for the not-food path).
func normalize(a *models.PhotoAnalysis) {
	if len(a.Dishes) > maxDishes {
		a.Dishes = a.Dishes[:maxDishes]
	}
	for i := range a.Dishes {
		a.Dishes[i].Clamp()
	}
	if a.Dishes == nil {
		a.Dishes = []models.Dish{}
	}
}

func (p *Processor) fail(ctx context.Context, log *zap.Logger, userID, scanID string, scan *models.Scan, reason models.FailureReason) error {
	err := p.Store.WriteFailure(ctx, userID, scanID, reason, time.Now().UTC())
	if errors.Is(err, store.ErrAlreadyTerminal) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: write failure: %v", ErrRetry, err)
	}
	log.Info("scan failed", zap.String("reason", string(reason)))
	p.refund(ctx, log, userID, scanID, scan)
	return nil
}

// refund returns the quota consumed at scan-create. Failed/not-food scans
// must not eat the user's allowance (conversion-window fairness). Refund
// date = the quota day consumed at create (scan creation date, UTC).
func (p *Processor) refund(ctx context.Context, log *zap.Logger, userID, scanID string, scan *models.Scan) {
	date := scan.CreatedAt.UTC().Format("2006-01-02")
	if err := p.Store.RefundScanQuota(ctx, userID, scanID, date); err != nil {
		// Refund failure must not fail the scan — log loudly instead.
		log.Error("quota refund failed", zap.Error(err))
	}
}

func isSizeError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "too large")
}
