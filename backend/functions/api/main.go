// API Lambda entrypoint. One binary, two transports: algnhsa on Lambda,
// plain net/http when LOCAL=1 (scripts/dev-api.sh).
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/akrylysov/algnhsa"
	"go.uber.org/zap"

	"smachnogo/pkg/api"
	"smachnogo/pkg/api/handlers"
	"smachnogo/pkg/awsx"
	"smachnogo/pkg/config"
	"smachnogo/pkg/llm"
	_ "smachnogo/pkg/llm/anthropic" // register provider
	"smachnogo/pkg/logging"
	"smachnogo/pkg/scanproc"
	"smachnogo/pkg/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}
	logger := logging.New(cfg.Local, cfg.GitSHA)
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
		if cfg.StaticBearerToken == "" {
			if tok, err := ssmClient.GetSecret(ctx, "static_bearer_token"); err == nil {
				cfg.StaticBearerToken = tok
			} else {
				logger.Warn("ssm static_bearer_token unavailable", zap.Error(err))
			}
		}
	}
	if cfg.AuthMode == "static" && cfg.StaticBearerToken == "" {
		logger.Fatal("AUTH_MODE=static requires STATIC_BEARER_TOKEN (env or SSM)")
	}

	st := store.New(awsCfg, cfg.TableName)
	s3c := awsx.NewS3(awsCfg, cfg.Bucket)

	scansH := &handlers.Scans{Cfg: cfg, Store: st, S3: s3c, SSM: ssmClient}
	if cfg.LocalSync {
		analyzer, err := llm.New(cfg.LLMProvider, cfg.AnthropicAPIKey, cfg.LLMModelVision, cfg.LLMModelText)
		if err != nil {
			logger.Fatal("llm init (LOCAL_SYNC needs the API key)", zap.Error(err))
		}
		scansH.Processor = &scanproc.Processor{
			Store: st, S3: s3c, Analyzer: analyzer,
			Provider: cfg.LLMProvider, Model: cfg.LLMModelVision,
		}
	} else {
		scansH.Queue = awsx.NewSQS(awsCfg, cfg.QueueURL)
	}

	router := api.NewRouter(api.Deps{
		Cfg:    cfg,
		Logger: logger,
		Scans:  scansH,
		Meals:  &handlers.Meals{Cfg: cfg, Store: st},
	})

	logger.Info("api starting",
		zap.String("env", cfg.Env),
		zap.Bool("local", cfg.Local),
		zap.Bool("local_sync", cfg.LocalSync),
		zap.String("table", cfg.TableName),
	)

	if cfg.Local {
		if err := st.Ping(ctx); err != nil {
			logger.Fatal("dynamodb unreachable", zap.Error(err))
		}
		logger.Info("listening", zap.String("addr", cfg.HTTPAddr))
		if err := http.ListenAndServe(cfg.HTTPAddr, router); err != nil {
			logger.Fatal("serve", zap.Error(err))
		}
		return
	}
	algnhsa.ListenAndServe(router, nil)
}
