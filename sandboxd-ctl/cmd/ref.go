package cmd

import (
	"fmt"
	"strings"
)

type objectRef struct {
	Resource string
	Name     string
}

func normalizeResource(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "sandbox", "sandboxes", "sbx", "s":
		return "sandbox"
	case "node", "nodes", "n":
		return "node"
	default:
		return s
	}
}

func parseObjectRef(arg string) (objectRef, error) {
	arg = strings.TrimSpace(arg)
	parts := strings.Split(arg, "/")
	if len(parts) != 2 {
		return objectRef{}, fmt.Errorf("expected <resource>/<name>, got %q", arg)
	}

	res := normalizeResource(parts[0])
	name := strings.TrimSpace(parts[1])
	if res == "" || name == "" {
		return objectRef{}, fmt.Errorf("invalid object reference %q", arg)
	}

	return objectRef{Resource: res, Name: name}, nil
}

func parseResourceList(arg string) ([]string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return nil, fmt.Errorf("resource is required")
	}

	parts := strings.Split(arg, ",")
	if len(parts) == 0 {
		return nil, fmt.Errorf("resource is required")
	}

	seen := map[string]struct{}{}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		res := normalizeResource(p)
		if res != "sandbox" && res != "node" {
			return nil, fmt.Errorf("unsupported resource %q", strings.TrimSpace(p))
		}

		if _, ok := seen[res]; ok {
			continue
		}

		seen[res] = struct{}{}
		out = append(out, res)
	}

	return out, nil
}
