package handlers

import "sandboxd-o/sandboxd-orch/types"

type ErrorResponse struct {
	Error string `json:"error"`
}

type HealthResponse struct {
	OK bool `json:"ok"`
}

type NodeResponse struct {
	Node *types.Node `json:"node"`
}

type NodesResponse struct {
	Items []types.Node `json:"items"`
}

type DeleteNodeResponse struct {
	Deleted string `json:"deleted"`
}

type HeartbeatResponse struct {
	Node           *types.Node `json:"node"`
	Heartbeat      string      `json:"heartbeat"`
	Resources      any         `json:"resources,omitempty"`
	HeartbeatError string      `json:"heartbeat_error,omitempty"`
	StatusError    string      `json:"status_error,omitempty"`
}

type SandboxObjectResponse struct {
	Sandbox *types.Sandbox `json:"sandbox"`
}

type SandboxObjectsResponse struct {
	Items []types.Sandbox `json:"items"`
}

type DeleteSandboxResponse struct {
	Deleted string `json:"deleted"`
}

type ProxyResponse map[string]any
