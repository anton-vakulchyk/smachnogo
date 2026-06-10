package logging

import (
	"context"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type ctxKey struct{}

// New builds the production JSON logger. local=true switches to console encoding.
func New(local bool, gitSHA string) *zap.Logger {
	var cfg zap.Config
	if local {
		cfg = zap.NewDevelopmentConfig()
	} else {
		cfg = zap.NewProductionConfig()
		cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	}
	logger, err := cfg.Build()
	if err != nil {
		panic(err)
	}
	return logger.With(zap.String("git_sha", gitSHA))
}

// Into stores a logger in the context; From retrieves it (falls back to zap.L()).
// Middleware enriches the context logger with request_id/user_id so every
// handler log line carries them without threading fields manually.
func Into(ctx context.Context, l *zap.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

func From(ctx context.Context) *zap.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*zap.Logger); ok {
		return l
	}
	return zap.L()
}
