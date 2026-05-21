package manifest

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type SandboxManifest struct {
	APIVersion string         `yaml:"apiVersion"`
	Kind       string         `yaml:"kind"`
	ID         string         `yaml:"id"`
	Spec       map[string]any `yaml:"spec"`
}

func ParseManifest(raw []byte) (map[string]any, error) {
	var m SandboxManifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	if strings.TrimSpace(m.Kind) == "" {
		return nil, fmt.Errorf("kind is required")
	}

	kind := strings.ToLower(strings.TrimSpace(m.Kind))
	id := strings.TrimSpace(m.ID)
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}

	switch kind {
	case "sandbox":
		if m.Spec == nil {
			return nil, fmt.Errorf("spec is required")
		}
		if _, ok := m.Spec["containers"]; !ok {
			return nil, fmt.Errorf("spec.containers is required")
		}
		return map[string]any{"kind": "Sandbox", "id": id, "spec": m.Spec}, nil
	case "node":
		if m.Spec == nil {
			return nil, fmt.Errorf("spec is required")
		}
		if _, ok := m.Spec["ip"]; !ok {
			return nil, fmt.Errorf("spec.ip is required")
		}
		if _, ok := m.Spec["port"]; !ok {
			return nil, fmt.Errorf("spec.port is required")
		}
		return map[string]any{"kind": "Node", "id": id, "spec": m.Spec}, nil
	case "external":
		if m.Spec == nil {
			return nil, fmt.Errorf("spec is required")
		}
		if _, ok := m.Spec["node_id"]; !ok {
			return nil, fmt.Errorf("spec.node_id is required")
		}
		if _, ok := m.Spec["external"]; !ok {
			return nil, fmt.Errorf("spec.external is required")
		}
		return map[string]any{"kind": "External", "id": id, "spec": m.Spec}, nil
	default:
		return nil, fmt.Errorf("unsupported kind %q (expected Sandbox|Node|External)", m.Kind)
	}
}
