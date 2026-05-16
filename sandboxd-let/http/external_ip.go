package httpserver

import (
	"context"
	"os/exec"
	"strings"
)

type externalIPService interface {
	Lookup(ctx context.Context) string
}

type commandExternalIPService struct{}

func (commandExternalIPService) Lookup(ctx context.Context) string {
	cmd := exec.CommandContext(ctx, "sh", "-c", "dig +short myip.opendns.com @resolver1.opendns.com")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}
