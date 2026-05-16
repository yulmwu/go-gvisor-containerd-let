package service

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

var ErrInvalidInput = errors.New("invalid input")

func validateNodeInput(name, ip string, port int) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidInput)
	}

	if net.ParseIP(strings.TrimSpace(ip)) == nil {
		return fmt.Errorf("%w: invalid ip", ErrInvalidInput)
	}

	if port < 1 || port > 65535 {
		return fmt.Errorf("%w: invalid port", ErrInvalidInput)
	}

	return nil
}
