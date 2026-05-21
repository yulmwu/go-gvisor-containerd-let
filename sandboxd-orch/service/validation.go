package service

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

var ErrInvalidInput = errors.New("invalid input")

func validateNodeInput(id, ip string, port int) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidInput)
	}

	if net.ParseIP(strings.TrimSpace(ip)) == nil {
		return fmt.Errorf("%w: invalid ip", ErrInvalidInput)
	}

	if port < 1 || port > 65535 {
		return fmt.Errorf("%w: invalid port", ErrInvalidInput)
	}

	return nil
}
