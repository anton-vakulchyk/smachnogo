// Package gemini adapts the canonical analysis contract to the Gemini API
// (REST, no SDK dependency). Rules ride in systemInstruction; the canonical
// JSON Schemas pass through verbatim via responseJsonSchema — guaranteed
// schema-valid JSON, same as the Anthropic adapter.
package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"smachnogo/pkg/llm"
	"smachnogo/pkg/llm/schema"
	"smachnogo/pkg/models"
)

const baseURL = "https://generativelanguage.googleapis.com/v1beta/models"

func init() {
	llm.Register("gemini", func(apiKey, visionModel, textModel string) (llm.Analyzer, error) {
		if apiKey == "" {
			return nil, fmt.Errorf("gemini: API key is empty")
		}
		return New(apiKey, visionModel, textModel), nil
	})
}

type Client struct {
	apiKey      string
	visionModel string
	textModel   string
	http        *http.Client
}

func New(apiKey, visionModel, textModel string) *Client {
	return &Client{
		apiKey:      apiKey,
		visionModel: visionModel,
		textModel:   textModel,
		http:        &http.Client{}, // per-call deadlines via context
	}
}

func (c *Client) AnalyzePhoto(ctx context.Context, jpeg []byte) (*models.PhotoAnalysis, llm.Usage, error) {
	parts := []part{
		{InlineData: &inlineData{MimeType: "image/jpeg", Data: base64.StdEncoding.EncodeToString(jpeg)}},
		{Text: schema.VisionUser},
	}
	var out models.PhotoAnalysis
	usage, err := c.call(ctx, callSpec{
		model: c.visionModel, system: schema.VisionSystem, schema: schema.PhotoAnalysis(),
		maxTokens: 8192, thinkingBudget: 1024, timeout: 75 * time.Second, parts: parts,
	}, &out)
	return &out, usage, err
}

func (c *Client) EstimateText(ctx context.Context, text string) (*models.TextEstimate, llm.Usage, error) {
	var out models.TextEstimate
	usage, err := c.call(ctx, callSpec{
		model: c.textModel, system: schema.TextEstimateSystem, schema: schema.TextEstimate(),
		maxTokens: 4096, thinkingBudget: 0, timeout: 15 * time.Second, parts: []part{{Text: text}},
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
		model: c.textModel, system: schema.RefineSystem, schema: schema.Dish(),
		maxTokens: 2048, thinkingBudget: 0, timeout: 15 * time.Second, parts: []part{{Text: user}},
	}, &out)
	return &out, usage, err
}

// --- wire types (request) ---

type inlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *inlineData `json:"inlineData,omitempty"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type thinkingConfig struct {
	ThinkingBudget int `json:"thinkingBudget"`
}

type generationConfig struct {
	ResponseMimeType   string          `json:"responseMimeType"`
	ResponseJSONSchema map[string]any  `json:"responseJsonSchema,omitempty"`
	MaxOutputTokens    int             `json:"maxOutputTokens,omitempty"`
	// Gemini 3 thinks by default and thoughts bill INTO maxOutputTokens —
	// unbounded thinking can exhaust the budget before any JSON is emitted
	// (observed: finishReason MAX_TOKENS on a vision call). Extraction
	// tasks need a small bounded budget, not open-ended reasoning.
	ThinkingConfig *thinkingConfig `json:"thinkingConfig,omitempty"`
}

type generateRequest struct {
	SystemInstruction *content         `json:"systemInstruction,omitempty"`
	Contents          []content        `json:"contents"`
	GenerationConfig  generationConfig `json:"generationConfig"`
}

// --- wire types (response) ---

type generateResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text    string `json:"text"`
				Thought bool   `json:"thought"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

type callSpec struct {
	model          string
	system         string
	schema         map[string]any
	maxTokens      int
	thinkingBudget int // bounded thinking; 0 disables
	timeout        time.Duration
	parts          []part
}

func (c *Client) call(ctx context.Context, spec callSpec, dst any) (llm.Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, spec.timeout)
	defer cancel()

	reqBody := generateRequest{
		SystemInstruction: &content{Parts: []part{{Text: spec.system}}},
		Contents:          []content{{Role: "user", Parts: spec.parts}},
		GenerationConfig: generationConfig{
			ResponseMimeType:   "application/json",
			ResponseJSONSchema: spec.schema,
			MaxOutputTokens:    spec.maxTokens,
			ThinkingConfig:     &thinkingConfig{ThinkingBudget: spec.thinkingBudget},
		},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return llm.Usage{}, fmt.Errorf("%w: marshal request: %v", llm.ErrTerminal, err)
	}

	start := time.Now()
	// One in-adapter retry on 429/5xx; SQS redelivery is the outer layer.
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return llm.Usage{LatencyMS: time.Since(start).Milliseconds()}, fmt.Errorf("%w: %v", llm.ErrRetryable, ctx.Err())
			case <-time.After(2 * time.Second):
			}
		}
		usage, err := c.once(ctx, spec.model, payload, dst, start)
		if err == nil || llm.IsTerminal(err) {
			return usage, err
		}
		lastErr = err
	}
	return llm.Usage{LatencyMS: time.Since(start).Milliseconds()}, lastErr
}

func (c *Client) once(ctx context.Context, model string, payload []byte, dst any, start time.Time) (llm.Usage, error) {
	url := fmt.Sprintf("%s/%s:generateContent", baseURL, model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return llm.Usage{}, fmt.Errorf("%w: %v", llm.ErrTerminal, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return llm.Usage{LatencyMS: time.Since(start).Milliseconds()}, fmt.Errorf("%w: transport: %v", llm.ErrRetryable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return llm.Usage{LatencyMS: time.Since(start).Milliseconds()}, fmt.Errorf("%w: read body: %v", llm.ErrRetryable, err)
	}

	usage := llm.Usage{LatencyMS: time.Since(start).Milliseconds()}

	if resp.StatusCode != http.StatusOK {
		msg := truncate(string(body), 300)
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			return usage, fmt.Errorf("%w: gemini %d: %s", llm.ErrRetryable, resp.StatusCode, msg)
		}
		return usage, fmt.Errorf("%w: gemini %d: %s", llm.ErrTerminal, resp.StatusCode, msg)
	}

	var gr generateResponse
	if err := json.Unmarshal(body, &gr); err != nil {
		return usage, fmt.Errorf("%w: decode response: %v", llm.ErrRetryable, err)
	}
	usage.InputTokens = gr.UsageMetadata.PromptTokenCount
	// Thoughts are billed as output — count them so the cost spine is honest.
	usage.OutputTokens = gr.UsageMetadata.CandidatesTokenCount + gr.UsageMetadata.ThoughtsTokenCount

	if len(gr.Candidates) == 0 {
		return usage, fmt.Errorf("%w: no candidates", llm.ErrRetryable)
	}
	cand := gr.Candidates[0]
	switch cand.FinishReason {
	case "STOP", "":
	case "MAX_TOKENS":
		return usage, fmt.Errorf("%w: max tokens", llm.ErrTerminal)
	case "SAFETY", "PROHIBITED_CONTENT", "BLOCKLIST":
		return usage, fmt.Errorf("%w: blocked (%s)", llm.ErrTerminal, cand.FinishReason)
	default:
		return usage, fmt.Errorf("%w: finish reason %s", llm.ErrRetryable, cand.FinishReason)
	}

	var text string
	for _, p := range cand.Content.Parts {
		if !p.Thought && p.Text != "" {
			text = p.Text
			break
		}
	}
	if text == "" {
		return usage, fmt.Errorf("%w: empty text part", llm.ErrRetryable)
	}
	if err := json.Unmarshal([]byte(text), dst); err != nil {
		return usage, fmt.Errorf("%w: unmarshal structured output: %v", llm.ErrRetryable, err)
	}
	return usage, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
