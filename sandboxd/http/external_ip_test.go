package httpserver

import (
	"context"
	"testing"
	"time"
)

func TestCommandExternalIPService(t *testing.T) {
	svc := commandExternalIPService{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = svc.Lookup(ctx)
}
