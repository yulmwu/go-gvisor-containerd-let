package model

import (
	"fmt"
	"strings"
)

func (r CreateSandboxRequest) Validate() error {
	if r.ID == "" {
		return fmt.Errorf("id is required")
	}

	if len(r.Containers) < 1 {
		return fmt.Errorf("at least one container is required")
	}

	seenNames := map[string]struct{}{}
	for _, c := range r.Containers {
		if c.Name == "" || c.Image == "" {
			return fmt.Errorf("container name and image are required")
		}

		if _, err := ParseCPUMilli(c.Resource.CPU); err != nil {
			return fmt.Errorf("container %s: %w", c.Name, err)
		}

		if _, err := ParseMemoryBytes(c.Resource.Memory); err != nil {
			return fmt.Errorf("container %s: %w", c.Name, err)
		}

		if _, ok := seenNames[c.Name]; ok {
			return fmt.Errorf("duplicate container name: %s", c.Name)
		}

		seenNames[c.Name] = struct{}{}
	}

	seenHostPorts := map[int]struct{}{}
	for _, p := range r.Ports {
		if p.HostPort < 1 || p.HostPort > 65535 || p.ContainerPort < 1 || p.ContainerPort > 65535 {
			return fmt.Errorf("invalid port mapping: %d:%d", p.HostPort, p.ContainerPort)
		}

		if p.HostPort < 1024 {
			return fmt.Errorf("privileged host ports are not allowed: %d", p.HostPort)
		}

		proto := strings.ToLower(strings.TrimSpace(p.Protocol))
		if proto == "" {
			proto = "tcp"
		}

		if proto != "tcp" && proto != "udp" {
			return fmt.Errorf("unsupported protocol: %s", p.Protocol)
		}

		if _, ok := seenHostPorts[p.HostPort]; ok {
			return fmt.Errorf("duplicate host port: %d", p.HostPort)
		}

		seenHostPorts[p.HostPort] = struct{}{}
	}

	return nil
}
