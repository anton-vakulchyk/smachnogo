package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config carries every environment-derived setting. One parse at cold start;
// handlers receive it explicitly — no package-level globals.
type Config struct {
	Env       string // dev | prod
	Local     bool   // serve HTTP on :8080 instead of Lambda
	LocalSync bool   // process scans inline instead of SQS

	TableName string
	Bucket    string
	QueueURL  string
	AWSRegion string

	AuthMode          string // static | cognito
	StaticBearerToken string
	StaticUserID      string
	CognitoPoolID     string
	CognitoClientID   string

	LLMProvider     string
	LLMModelVision  string
	LLMModelText    string
	LLMVisionPolicy string
	AnthropicAPIKey string // resolved from SSM when SSMPrefix set, else env
	GeminiAPIKey    string // same resolution rule

	SSMPrefix string // e.g. /smachnogo/dev — when set, secrets+kill switch read from SSM

	DailyScanCap     int
	DailyEstimateCap int
	ScansEnabled     bool // env fallback; SSM value (cached) wins when SSMPrefix set
	ClarifyThreshold float64

	PresignTTL    time.Duration
	GitSHA        string
	HTTPAddr      string
	ScanResultTTL time.Duration // DDB expires_at horizon for scan items
}

func Load() (*Config, error) {
	c := &Config{
		Env:       getenv("ENV", "dev"),
		Local:     getbool("LOCAL", false),
		LocalSync: getbool("LOCAL_SYNC", false),

		TableName: getenv("TABLE_NAME", "smachnogo-main-dev"),
		Bucket:    os.Getenv("BUCKET"),
		QueueURL:  os.Getenv("QUEUE_URL"),
		AWSRegion: getenv("AWS_REGION", "us-east-1"),

		AuthMode:          getenv("AUTH_MODE", "static"),
		StaticBearerToken: os.Getenv("STATIC_BEARER_TOKEN"),
		StaticUserID:      getenv("STATIC_USER_ID", "8a2fb1f4-3c5e-4b9a-9d27-6e1f0c4a7b53"),
		CognitoPoolID:     os.Getenv("COGNITO_POOL_ID"),
		CognitoClientID:   os.Getenv("COGNITO_CLIENT_ID"),

		LLMProvider:     getenv("LLM_PROVIDER", "gemini"),
		LLMVisionPolicy: getenv("LLM_VISION_POLICY", "single"),
		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
		GeminiAPIKey:    os.Getenv("GEMINI_API_KEY"),

		SSMPrefix: os.Getenv("SSM_PREFIX"),

		DailyScanCap:     getint("DAILY_SCAN_CAP", 20),
		DailyEstimateCap: getint("DAILY_ESTIMATE_CAP", 20),
		ScansEnabled:     getbool("SCANS_ENABLED", true),
		ClarifyThreshold: getfloat("CLARIFY_THRESHOLD", 0.6),

		PresignTTL:    15 * time.Minute,
		GitSHA:        getenv("GIT_SHA", "dev"),
		HTTPAddr:      getenv("HTTP_ADDR", ":8080"),
		ScanResultTTL: 30 * 24 * time.Hour,
	}

	// Model defaults are provider-aware; env overrides win.
	switch c.LLMProvider {
	case "gemini":
		c.LLMModelVision = getenv("LLM_MODEL_VISION", "gemini-3-flash-preview")
		c.LLMModelText = getenv("LLM_MODEL_TEXT", "gemini-3.1-flash-lite")
	default:
		c.LLMModelVision = getenv("LLM_MODEL_VISION", "claude-opus-4-8")
		c.LLMModelText = getenv("LLM_MODEL_TEXT", "claude-haiku-4-5")
	}

	// STATIC_BEARER_TOKEN may also arrive via SSM after Load (deployed mode);
	// the API entrypoint validates it post-resolution.
	if c.Bucket == "" {
		return nil, fmt.Errorf("BUCKET is required")
	}
	// QUEUE_URL is required only for the enqueue path; the worker (consumer)
	// and LOCAL_SYNC mode don't send. Role declares which validation applies.
	if getenv("ROLE", "api") == "api" && !c.LocalSync && c.QueueURL == "" {
		return nil, fmt.Errorf("QUEUE_URL is required for the api unless LOCAL_SYNC=1")
	}
	return c, nil
}

// LLMKey returns the configured provider's API key (post-SSM-resolution).
func (c *Config) LLMKey() string {
	if c.LLMProvider == "gemini" {
		return c.GeminiAPIKey
	}
	return c.AnthropicAPIKey
}

// SetLLMKey stores an SSM-resolved key for the configured provider.
func (c *Config) SetLLMKey(key string) {
	if c.LLMProvider == "gemini" {
		c.GeminiAPIKey = key
		return
	}
	c.AnthropicAPIKey = key
}

func getenv(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func getbool(k string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	if v == "" {
		return def
	}
	return v == "1" || v == "true" || v == "yes"
}

func getint(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getfloat(k string, def float64) float64 {
	if v := os.Getenv(k); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}
