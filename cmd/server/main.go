package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"sandboxd/internal/config"
	httpserver "sandboxd/internal/http"
	"sandboxd/internal/sandbox"
)

func main() {
	_ = godotenv.Load()

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
		log.Fatalf("init sandbox service: %v", err)
	}

	defer svc.Close()
	svc.StartReconcileLoop(ctx)

	h := httpserver.New(svc).Handler()
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
		log.Fatalf("http server failed: %v", err)
	}
}
