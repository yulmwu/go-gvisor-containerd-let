package service

import (
	"fmt"
	"net"
	"strings"
)

func validateNodeInput(name, ip string, port int) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("name is required")
	}

	if net.ParseIP(strings.TrimSpace(ip)) == nil {
		return fmt.Errorf("invalid ip")
	}

	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid port")
	}

	return nil
}
