package main

import (
	"context"
	"log"
	"log/slog"
	nethttp "net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"sandboxd-o/pkg/logging"
	"sandboxd-o/sandboxd-orch/config"
	_ "sandboxd-o/sandboxd-orch/docs"
	httpserver "sandboxd-o/sandboxd-orch/http"
	"sandboxd-o/sandboxd-orch/service"
)

// @title Orchestrator API Server
// @version 1.0
// @BasePath /
// @schemes http

func main() {
	cfg, err := config.Load()
	if err != nil {
		boot := slog.New(slog.NewJSONHandler(os.Stderr, nil))
		boot.Error("orchestrator config error", slog.Any("error", err))
		os.Exit(1)
	}

	logger, err := logging.New(logging.Config{
		Dir:        strings.TrimSpace(os.Getenv("ORCH_LOG_DIR")),
		FilePrefix: valueOrDefault(strings.TrimSpace(os.Getenv("ORCH_LOG_FILE_PREFIX")), "orchestrator"),
	}, logging.Options{Service: "orchestrator", Env: strings.TrimSpace(os.Getenv("APP_ENV")), AddSource: false, Level: slog.LevelInfo})
	if err != nil {
		boot := slog.New(slog.NewJSONHandler(os.Stderr, nil))
		boot.Error("orchestrator logging init error", slog.Any("error", err))
		os.Exit(1)
	}
	defer logger.Close()

	slog.SetDefault(logger.Logger)
	log.SetOutput(logger)
	log.SetFlags(0)

	svc, err := service.New(cfg)
	if err != nil {
		logger.Error("orchestrator init error", slog.Any("error", err))
		os.Exit(1)
	}
	defer svc.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := svc.BootstrapNodes(ctx); err != nil {
		logger.Error("bootstrap nodes error", slog.Any("error", err))
		os.Exit(1)
	}
	svc.StartHeartbeatLoop(ctx)
	svc.StartResourceSyncLoop(ctx)
	svc.StartSchedulerLoop(ctx)
	svc.StartSandboxReconcileLoop(ctx)

	router := httpserver.NewRouter(svc, cfg, logger)
	srv := &nethttp.Server{
		Addr:              svc.HTTPAddr(),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != nethttp.ErrServerClosed {
			logger.Error("orchestrator server error", slog.Any("error", err))
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), svc.ShutdownTimeout())
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func valueOrDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}

	return v
}
