package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"sandboxd-o/pkg/logging"
	"sandboxd-o/sandboxd/config"
	httpserver "sandboxd-o/sandboxd/http"
	"sandboxd-o/sandboxd/sandbox"

	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()

	logger, err := logging.New(logging.Config{
		Dir:        strings.TrimSpace(os.Getenv("SANDBOX_LOG_DIR")),
		FilePrefix: valueOrDefault(strings.TrimSpace(os.Getenv("SANDBOX_LOG_FILE_PREFIX")), "sandboxd"),
	}, logging.Options{Service: "sandboxd", Env: strings.TrimSpace(os.Getenv("APP_ENV")), AddSource: false})
	if err != nil {
		boot := slog.New(slog.NewJSONHandler(os.Stderr, nil))
		boot.Error("sandboxd logging init error", slog.Any("error", err))
		os.Exit(1)
	}
	defer logger.Close()

	slog.SetDefault(logger.Logger)
	log.SetOutput(logger)
	log.SetFlags(0)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.DefaultConfig()
	if v := os.Getenv("SANDBOX_STATE_BASE_DIR"); v != "" {
		cfg.StateBaseDir = v
	}

	if v := os.Getenv("SANDBOX_LOCK_DIR"); v != "" {
		cfg.LockDir = v
	}

	if v := os.Getenv("SANDBOX_BRIDGE_INTERFACE"); v != "" {
		cfg.BridgeInterface = v
	}

	if v := os.Getenv("SANDBOX_SUBNET_CIDR"); v != "" {
		cfg.SubnetCIDR = v
	}

	svc, err := sandbox.New(ctx, cfg)
	if err != nil {
		logger.Error("init sandbox service error", slog.Any("error", err))
		os.Exit(1)
	}

	defer svc.Close()
	svc.StartReconcileLoop(ctx)

	h := httpserver.New(svc, logger).Handler()
	addr := ":8080"
	if v := os.Getenv("HTTP_ADDR"); v != "" {
		addr = v
	}

	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("http server failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func valueOrDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}

	return v
}
