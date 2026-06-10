// Scan worker Lambda: SQS → scanproc.Process. Partial-batch failure
// reporting returns retryable scans to the queue; terminal outcomes are
// handled inside Process (FAILED + quota refund) and ack.
package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"go.uber.org/zap"

	"smachnogo/pkg/awsx"
	"smachnogo/pkg/config"
	"smachnogo/pkg/llm"
	_ "smachnogo/pkg/llm/anthropic" // register provider
	"smachnogo/pkg/logging"
	"smachnogo/pkg/scanproc"
	"smachnogo/pkg/store"
)

type handler struct {
	cfg       *config.Config
	logger    *zap.Logger
	processor *scanproc.Processor
	ssm       *awsx.SSM
}

func (h *handler) handle(ctx context.Context, event events.SQSEvent) (events.SQSEventResponse, error) {
	var failures []events.SQSBatchItemFailure
	for _, record := range event.Records {
		if err := h.handleRecord(ctx, record); err != nil {
			failures = append(failures, events.SQSBatchItemFailure{ItemIdentifier: record.MessageId})
		}
	}
	return events.SQSEventResponse{BatchItemFailures: failures}, nil
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
		if cfg.AnthropicAPIKey == "" {
			if key, err := ssmClient.GetSecret(ctx, "anthropic_api_key"); err == nil {
				cfg.AnthropicAPIKey = key
			} else {
				logger.Warn("ssm anthropic_api_key unavailable", zap.Error(err))
			}
		}
	}

	analyzer, err := llm.New(cfg.LLMProvider, cfg.AnthropicAPIKey, cfg.LLMModelVision, cfg.LLMModelText)
	if err != nil {
		logger.Fatal("llm init", zap.Error(err))
	}

	h := &handler{
		cfg:    cfg,
		logger: logger,
		ssm:    ssmClient,
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
