// Package awsx wraps the AWS SDK pieces the app touches: config load, S3
// presign/get, SQS enqueue, SSM secrets.
package awsx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

func LoadConfig(ctx context.Context, region string) (aws.Config, error) {
	return awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
}

// --- S3 ---

type S3 struct {
	client   *s3.Client
	presign  *s3.PresignClient
	bucket   string
}

func NewS3(cfg aws.Config, bucket string) *S3 {
	c := s3.NewFromConfig(cfg)
	return &S3{client: c, presign: s3.NewPresignClient(c), bucket: bucket}
}

// ScanKey is the single source of the photo key shape (user-scoped from
// day 1 — the auth seam).
func ScanKey(userID, scanID string) string {
	return fmt.Sprintf("scans/%s/%s.jpg", userID, scanID)
}

// PresignPut pins Content-Type into the signature; the client must send
// image/jpeg or the PUT is rejected.
func (s *S3) PresignPut(ctx context.Context, key string, ttl time.Duration) (url string, err error) {
	out, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      &s.bucket,
		Key:         &key,
		ContentType: aws.String("image/jpeg"),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign put: %w", err)
	}
	return out.URL, nil
}

var ErrNoObject = errors.New("awsx: object not found")

const MaxImageBytes = 5 * 1024 * 1024 // provider per-image cap

// GetObject downloads the photo, enforcing the size cap.
func (s *S3) GetObject(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		var noKey *s3types.NoSuchKey
		if errors.As(err, &noKey) {
			return nil, ErrNoObject
		}
		return nil, fmt.Errorf("get object: %w", err)
	}
	defer out.Body.Close()
	if out.ContentLength != nil && *out.ContentLength > MaxImageBytes {
		return nil, fmt.Errorf("object too large: %d bytes", *out.ContentLength)
	}
	data, err := io.ReadAll(io.LimitReader(out.Body, MaxImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read object: %w", err)
	}
	if len(data) > MaxImageBytes {
		return nil, fmt.Errorf("object too large: >%d bytes", MaxImageBytes)
	}
	return data, nil
}

// --- SQS ---

type SQS struct {
	client   *sqs.Client
	queueURL string
}

func NewSQS(cfg aws.Config, queueURL string) *SQS {
	return &SQS{client: sqs.NewFromConfig(cfg), queueURL: queueURL}
}

// ScanMessage is the one message contract between API and worker.
type ScanMessage struct {
	UserID string `json:"user_id"`
	ScanID string `json:"scan_id"`
}

func (q *SQS) SendScan(ctx context.Context, userID, scanID, requestID string) error {
	body := fmt.Sprintf(`{"user_id":%q,"scan_id":%q}`, userID, scanID)
	_, err := q.client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    &q.queueURL,
		MessageBody: &body,
		MessageAttributes: map[string]sqstypes.MessageAttributeValue{
			"request_id": {DataType: aws.String("String"), StringValue: aws.String(orDash(requestID))},
		},
	})
	if err != nil {
		return fmt.Errorf("send scan message: %w", err)
	}
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// --- SSM ---

type SSM struct {
	client *ssm.Client
	prefix string

	mu          sync.Mutex
	scansCached *bool
	cachedAt    time.Time
}

func NewSSM(cfg aws.Config, prefix string) *SSM {
	return &SSM{client: ssm.NewFromConfig(cfg), prefix: prefix}
}

// GetSecret reads a SecureString (e.g. anthropic_api_key) at cold start.
func (s *SSM) GetSecret(ctx context.Context, name string) (string, error) {
	full := s.prefix + "/" + name
	out, err := s.client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           &full,
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("ssm get %s: %w", full, err)
	}
	return *out.Parameter.Value, nil
}

// ScansEnabled reads the kill switch with a 60s cache. SSM-backed so a
// `terraform apply` can't silently revert a console flip; fail-open to the
// provided fallback when the parameter is absent/unreachable (availability
// over control for a read failure — the env fallback is the real value).
func (s *SSM) ScansEnabled(ctx context.Context, fallback bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scansCached != nil && time.Since(s.cachedAt) < 60*time.Second {
		return *s.scansCached
	}
	full := s.prefix + "/scans_enabled"
	out, err := s.client.GetParameter(ctx, &ssm.GetParameterInput{Name: &full})
	val := fallback
	if err == nil && out.Parameter != nil && out.Parameter.Value != nil {
		val = *out.Parameter.Value == "true" || *out.Parameter.Value == "1"
	}
	s.scansCached = &val
	s.cachedAt = time.Now()
	return val
}
