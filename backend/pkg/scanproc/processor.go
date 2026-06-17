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

// macro-energy gate constants. The ratio (4P+9F+4C)/kcal must sit within
// [1/ratioBound, ratioBound] of reported kcal, EXCEPT alcoholic drinks whose
// ethanol energy (~7 kcal/g) lives in none of protein/fat/carbs and so pulls
// the ratio below the lower bound. See macroEnergyIssue.
const (
	macroGateMinKcal = 50  // skip the ratio check below this (drinks, garnishes)
	ratioBound       = 2.5 // 4P+9F+4C within 2.5x of kcal, both directions
	// Largest plausible ethanol-attributable remainder for a single serving
	// (kcal - 4P+9F+4C). A strong large drink is ~24 g ethanol ≈ 170 kcal; 250
	// is a generous hard cap. A "drink" claiming more unexplained energy than
	// this is garbage, not alcohol, so the lower-bound relaxation does not apply.
	maxEthanolRemainderKcal = 250.0
	drinkMacroEpsilonG      = 1.0 // protein/fat treated as ~0 for the drink signature
)

// macroEnergyIssue applies the macro-energy plausibility check to one nutrient
// block and returns a non-empty reason when it is implausible. The UPPER bound
// is always enforced; the LOWER bound is relaxed for an alcoholic-drink
// signature — protein≈0, fat≈0, some carbs (real beer/wine residual sugars),
// and an unexplained remainder consistent with a single serving of ethanol —
// since ethanol calories legitimately live outside protein/fat/carbs. Blocks at
// or below macroGateMinKcal (e.g. a 0-kcal Diet variant) are exempt entirely.
func macroEnergyIssue(n models.Nutrients) string {
	if n.CaloriesKcal <= macroGateMinKcal {
		return ""
	}
	macroKcal := 4*n.ProteinG + 9*n.FatG + 4*n.CarbsG
	kcal := float64(n.CaloriesKcal)
	ratio := macroKcal / kcal
	if ratio > ratioBound {
		return fmt.Sprintf("macro-energy mismatch ratio=%.2f", ratio)
	}
	if ratio < 1/ratioBound && !isDrinkSignature(n, macroKcal) {
		return fmt.Sprintf("macro-energy mismatch ratio=%.2f", ratio)
	}
	return ""
}

// isDrinkSignature reports whether a nutrient block looks like an alcoholic
// drink whose sub-bound macro ratio is explained by ethanol: negligible protein
// and fat, a positive carb reading (real beer/wine/cocktails carry residual
// sugars — a 0-macro block is garbage, not a drink), and an unexplained calorie
// remainder that is positive and within one serving of ethanol.
func isDrinkSignature(n models.Nutrients, macroKcal float64) bool {
	remainder := float64(n.CaloriesKcal) - macroKcal
	return n.ProteinG <= drinkMacroEpsilonG &&
		n.FatG <= drinkMacroEpsilonG &&
		n.CarbsG > 0 &&
		remainder > 0 &&
		remainder <= maxEthanolRemainderKcal
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
		d.DefaultToFirstVariant() // headline must equal variants[0] before we validate + store it
		if d.CaloriesKcal > 0 {
			allZero = false
		}
		if d.PortionG <= 0 {
			return "non-positive portion_g"
		}
		// Macro-energy consistency on the headline the user sees first.
		if reason := macroEnergyIssue(d.Nutrients); reason != "" {
			return reason
		}
		// Non-default variant blocks (e.g. a Diet/Zero fork) are stored and
		// logged when the user taps that fork, so hold them to the same numeric
		// sanity as the headline — a 0-kcal Diet block is naturally exempt.
		for vi := range d.Variants {
			if reason := macroEnergyIssue(d.Variants[vi].Nutrients); reason != "" {
				return fmt.Sprintf("variant %q %s", d.Variants[vi].Label, reason)
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
	if err := p.Store.RefundScanQuota(ctx, userID, scanID, date, scan.AllowanceConsumed); err != nil {
		// Refund failure must not fail the scan — log loudly instead.
		log.Error("quota refund failed", zap.Error(err))
	}
}

func isSizeError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "too large")
}
