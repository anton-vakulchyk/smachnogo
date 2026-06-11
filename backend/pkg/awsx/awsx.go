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
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
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

// DeletePrefix removes every object under prefix (account-deletion cascade
// for the user's photos). Returns the number of objects deleted.
func (s *S3) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	deleted := 0
	var token *string
	for {
		page, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket: &s.bucket, Prefix: &prefix, ContinuationToken: token,
		})
		if err != nil {
			return deleted, fmt.Errorf("list prefix: %w", err)
		}
		if len(page.Contents) > 0 {
			objs := make([]s3types.ObjectIdentifier, 0, len(page.Contents))
			for _, o := range page.Contents {
				objs = append(objs, s3types.ObjectIdentifier{Key: o.Key})
			}
			out, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: &s.bucket,
				Delete: &s3types.Delete{Objects: objs, Quiet: aws.Bool(true)},
			})
			if err != nil {
				return deleted, fmt.Errorf("delete objects: %w", err)
			}
			deleted += len(objs) - len(out.Errors)
			if len(out.Errors) > 0 {
				return deleted, fmt.Errorf("delete objects: %d failed", len(out.Errors))
			}
		}
		if page.IsTruncated == nil || !*page.IsTruncated {
			return deleted, nil
		}
		token = page.NextContinuationToken
	}
}

// --- Cognito (admin ops for account deletion) ---

type Cognito struct {
	client *cognitoidentityprovider.Client
	poolID string
}

func NewCognito(cfg aws.Config, poolID string) *Cognito {
	return &Cognito{client: cognitoidentityprovider.NewFromConfig(cfg), poolID: poolID}
}

// DeleteUserBySub removes the Cognito user whose sub matches userID.
// Usernames are opaque, so resolve sub → username via ListUsers (sub is a
// filterable standard attribute).
func (c *Cognito) DeleteUserBySub(ctx context.Context, sub string) error {
	filter := fmt.Sprintf(`sub = "%s"`, sub)
	out, err := c.client.ListUsers(ctx, &cognitoidentityprovider.ListUsersInput{
		UserPoolId: &c.poolID,
		Filter:     &filter,
		Limit:      aws.Int32(1),
	})
	if err != nil {
		return fmt.Errorf("list users by sub: %w", err)
	}
	if len(out.Users) == 0 {
		return nil // already gone — deletion is idempotent
	}
	_, err = c.client.AdminDeleteUser(ctx, &cognitoidentityprovider.AdminDeleteUserInput{
		UserPoolId: &c.poolID,
		Username:   out.Users[0].Username,
	})
	if err != nil {
		return fmt.Errorf("admin delete user: %w", err)
	}
	return nil
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
