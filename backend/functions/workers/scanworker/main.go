// Scan worker Lambda: SQS → scanproc.Process. Partial-batch failure
// reporting returns retryable scans to the queue; terminal outcomes are
// handled inside Process (FAILED + quota refund) and ack.
package main

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"go.uber.org/zap"

	"smachnogo/pkg/awsx"
	"smachnogo/pkg/config"
	"smachnogo/pkg/llm"
	// Anthropic provider disabled until keys exist (owner decision 2026-06-10);
	// re-enable by restoring the import: _ "smachnogo/pkg/llm/anthropic"
	_ "smachnogo/pkg/llm/gemini" // register providers
	"smachnogo/pkg/logging"
	"smachnogo/pkg/scanproc"
	"smachnogo/pkg/store"
)

type handler struct {
	cfg       *config.Config
	logger    *zap.Logger
	processor *scanproc.Processor
	ssm       *awsx.SSM
	dlqMode   bool // DLQ_MODE=1: dead-letter consumer (mark FAILED, no LLM)
}

func (h *handler) handle(ctx context.Context, event events.SQSEvent) (events.SQSEventResponse, error) {
	var failures []events.SQSBatchItemFailure
	for _, record := range event.Records {
		var err error
		if h.dlqMode {
			err = h.handleDLQRecord(ctx, record)
		} else {
			err = h.handleRecord(ctx, record)
		}
		if err != nil {
			failures = append(failures, events.SQSBatchItemFailure{ItemIdentifier: record.MessageId})
		}
	}
	return events.SQSEventResponse{BatchItemFailures: failures}, nil
}

// handleDLQRecord runs when DLQ_MODE=1: a message that exhausted its
// retries lands here — mark the scan FAILED(internal) (+ quota refund via
// the shared failure path) so clients aren't polling a zombie forever.
func (h *handler) handleDLQRecord(ctx context.Context, record events.SQSMessage) error {
	log := h.logger.With(zap.String("sqs_message_id", record.MessageId), zap.Bool("dlq", true))
	ctx = logging.Into(ctx, log)

	var msg awsx.ScanMessage
	if err := json.Unmarshal([]byte(record.Body), &msg); err != nil || msg.UserID == "" || msg.ScanID == "" {
		log.Error("malformed DLQ message — acking", zap.String("body", record.Body))
		return nil
	}
	if err := h.processor.FailScan(ctx, msg.UserID, msg.ScanID); err != nil {
		log.Error("DLQ fail-scan errored — acking anyway (alarm already fired)", zap.Error(err))
	}
	log.Warn("scan dead-lettered → FAILED(internal)", zap.String("scan_id", msg.ScanID))
	return nil
}

func (h *handler) handleRecord(ctx context.Context, record events.SQSMessage) error {
	requestID := "-"
	if attr, ok := record.MessageAttributes["request_id"]; ok && attr.StringValue != nil {
		requestID = *attr.StringValue
	}
	log := h.logger.With(zap.String("request_id", requestID), zap.String("sqs_message_id", record.MessageId))
	ctx = logging.Into(ctx, log)

	// Kill switch: keep messages in flight (retryable) rather than dropping
	// scans during an emergency stop.
	enabled := h.cfg.ScansEnabled
	if h.ssm != nil {
		enabled = h.ssm.ScansEnabled(ctx, h.cfg.ScansEnabled)
	}
	if !enabled {
		log.Warn("scans disabled — returning message to queue")
		return scanproc.ErrRetry
	}

	var msg awsx.ScanMessage
	if err := json.Unmarshal([]byte(record.Body), &msg); err != nil || msg.UserID == "" || msg.ScanID == "" {
		// Malformed message: retrying can't fix it; ack and log loudly.
		log.Error("malformed scan message — acking", zap.String("body", record.Body), zap.Error(err))
		return nil
	}

	if err := h.processor.Process(ctx, msg.UserID, msg.ScanID); err != nil {
		log.Warn("scan processing will retry", zap.Error(err), zap.String("scan_id", msg.ScanID))
		return err
	}
	return nil
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}
	logger := logging.New(false, cfg.GitSHA)
	defer func() { _ = logger.Sync() }()
	zap.ReplaceGlobals(logger)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	awsCfg, err := awsx.LoadConfig(ctx, cfg.AWSRegion)
	if err != nil {
		logger.Fatal("aws config", zap.Error(err))
	}

	var ssmClient *awsx.SSM
	if cfg.SSMPrefix != "" {
		ssmClient = awsx.NewSSM(awsCfg, cfg.SSMPrefix)
		if cfg.LLMKey() == "" {
			secretName := cfg.LLMProvider + "_api_key"
			if key, err := ssmClient.GetSecret(ctx, secretName); err == nil {
				cfg.SetLLMKey(key)
			} else {
				logger.Warn("ssm llm key unavailable", zap.String("param", secretName), zap.Error(err))
			}
		}
	}

	analyzer, err := llm.New(cfg.LLMProvider, cfg.LLMKey(), cfg.LLMModelVision, cfg.LLMModelText)
	if err != nil {
		logger.Fatal("llm init", zap.Error(err))
	}

	h := &handler{
		cfg:     cfg,
		logger:  logger,
		ssm:     ssmClient,
		dlqMode: os.Getenv("DLQ_MODE") == "1",
		processor: &scanproc.Processor{
			Store:    store.New(awsCfg, cfg.TableName),
			S3:       awsx.NewS3(awsCfg, cfg.Bucket),
			Analyzer: analyzer,
			Provider: cfg.LLMProvider,
			Model:    cfg.LLMModelVision,
		},
	}
	logger.Info("scanworker starting", zap.String("env", cfg.Env), zap.String("model", cfg.LLMModelVision))
	lambda.Start(h.handle)
}
