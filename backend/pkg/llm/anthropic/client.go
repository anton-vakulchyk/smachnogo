// Package anthropic adapts the canonical analysis contract to the Claude
// API. Rules + schema ride in the system role; the image/text is the user
// turn. Structured outputs (output_config.format) guarantee schema-valid
// JSON — no temperature/top_p/thinking params (removed on opus-4-8; 400).
package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"smachnogo/pkg/llm"
	"smachnogo/pkg/llm/schema"
	"smachnogo/pkg/models"
)

func init() {
	llm.Register("anthropic", func(apiKey, visionModel, textModel string) (llm.Analyzer, error) {
		if apiKey == "" {
			return nil, fmt.Errorf("anthropic: API key is empty")
		}
		return New(apiKey, visionModel, textModel), nil
	})
}

type Client struct {
	api         sdk.Client
	visionModel string
	textModel   string
}

func New(apiKey, visionModel, textModel string) *Client {
	return &Client{
		api: sdk.NewClient(
			option.WithAPIKey(apiKey),
			option.WithMaxRetries(2), // SDK handles 429/5xx backoff; SQS is the outer retry layer
		),
		visionModel: visionModel,
		textModel:   textModel,
	}
}

func (c *Client) AnalyzePhoto(ctx context.Context, jpeg []byte) (*models.PhotoAnalysis, llm.Usage, error) {
	b64 := base64.StdEncoding.EncodeToString(jpeg)
	var out models.PhotoAnalysis
	usage, err := c.call(ctx, callSpec{
		model:     c.visionModel,
		system:    schema.VisionSystem,
		schema:    schema.PhotoAnalysis(),
		maxTokens: 4096,
		timeout:   75 * time.Second,
		blocks: []sdk.ContentBlockParamUnion{
			sdk.NewImageBlockBase64("image/jpeg", b64),
			sdk.NewTextBlock(schema.VisionUser),
		},
	}, &out)
	return &out, usage, err
}

func (c *Client) EstimateText(ctx context.Context, text string) (*models.TextEstimate, llm.Usage, error) {
	var out models.TextEstimate
	usage, err := c.call(ctx, callSpec{
		model:     c.textModel,
		system:    schema.TextEstimateSystem,
		schema:    schema.TextEstimate(),
		maxTokens: 2048,
		timeout:   15 * time.Second,
		blocks:    []sdk.ContentBlockParamUnion{sdk.NewTextBlock(text)},
	}, &out)
	return &out, usage, err
}

func (c *Client) RefineDish(ctx context.Context, dish models.Dish, answer string) (*models.Dish, llm.Usage, error) {
	dishJSON, err := json.Marshal(dish)
	if err != nil {
		return nil, llm.Usage{}, fmt.Errorf("%w: marshal dish: %v", llm.ErrTerminal, err)
	}
	user := fmt.Sprintf("Original dish estimate:\n%s\n\nUser's answer about its contents: %s", dishJSON, answer)
	var out models.Dish
	usage, err := c.call(ctx, callSpec{
		model:     c.textModel,
		system:    schema.RefineSystem,
		schema:    schema.Dish(),
		maxTokens: 1024,
		timeout:   15 * time.Second,
		blocks:    []sdk.ContentBlockParamUnion{sdk.NewTextBlock(user)},
	}, &out)
	return &out, usage, err
}

type callSpec struct {
	model     string
	system    string
	schema    map[string]any
	maxTokens int64
	timeout   time.Duration
	blocks    []sdk.ContentBlockParamUnion
}

// call runs one structured-output request and unmarshals the guaranteed-JSON
// first text block into dst.
func (c *Client) call(ctx context.Context, spec callSpec, dst any) (llm.Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, spec.timeout)
	defer cancel()

	start := time.Now()
	msg, err := c.api.Messages.New(ctx, sdk.MessageNewParams{
		Model:     sdk.Model(spec.model),
		MaxTokens: spec.maxTokens,
		System:    []sdk.TextBlockParam{{Text: spec.system}},
		Messages:  []sdk.MessageParam{sdk.NewUserMessage(spec.blocks...)},
		OutputConfig: sdk.OutputConfigParam{
			Format: sdk.JSONOutputFormatParam{Schema: spec.schema},
		},
	})
	usage := llm.Usage{LatencyMS: time.Since(start).Milliseconds()}
	if err != nil {
		return usage, classify(err)
	}
	usage.InputTokens = int(msg.Usage.InputTokens)
	usage.OutputTokens = int(msg.Usage.OutputTokens)

	if msg.StopReason == sdk.StopReasonRefusal {
		return usage, fmt.Errorf("%w: model refusal", llm.ErrTerminal)
	}
	if msg.StopReason == sdk.StopReasonMaxTokens {
		// Truncated JSON cannot be schema-valid; a retry with the same input
		// would truncate again.
		return usage, fmt.Errorf("%w: max_tokens hit", llm.ErrTerminal)
	}

	var text string
	for _, block := range msg.Content {
		if tb, ok := block.AsAny().(sdk.TextBlock); ok {
			text = tb.Text
			break
		}
	}
	if text == "" {
		return usage, fmt.Errorf("%w: no text block in response", llm.ErrRetryable)
	}
	if err := json.Unmarshal([]byte(text), dst); err != nil {
		// Structured outputs should make this impossible; treat as one
		// retryable anomaly rather than terminal.
		return usage, fmt.Errorf("%w: unmarshal structured output: %v", llm.ErrRetryable, err)
	}
	return usage, nil
}

// classify maps API errors to the worker's retry decision. 429/5xx/529 and
// transport errors are retryable (SQS redelivery); 4xx are terminal.
func classify(err error) error {
	var apiErr *sdk.Error
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == 429 || apiErr.StatusCode >= 500:
			return fmt.Errorf("%w: api %d: %v", llm.ErrRetryable, apiErr.StatusCode, err)
		default:
			return fmt.Errorf("%w: api %d: %v", llm.ErrTerminal, apiErr.StatusCode, err)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %v", llm.ErrRetryable, err)
	}
	// Network/transport errors without an HTTP status.
	return fmt.Errorf("%w: %v", llm.ErrRetryable, err)
}
