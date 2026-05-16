package httpserver

import (
	"sandboxd-o/sandboxd-let/model"
	"sandboxd-o/sandboxd-let/sandbox"
)

type ErrorResponse struct {
	Error string `json:"error"`
}

type HealthResponse struct {
	OK bool `json:"ok"`
}

type NodeStatusResponse struct {
	OK        bool                       `json:"ok"`
	Resources model.NodeResourceSnapshot `json:"resources"`
}

type CreateSandboxResponse struct {
	Sandbox    *model.Sandbox `json:"sandbox"`
	ExternalIP string         `json:"external_ip,omitempty"`
}

type GetSandboxResponse struct {
	Sandbox    *model.Sandbox `json:"sandbox"`
	ExternalIP string         `json:"external_ip,omitempty"`
}

type ListSandboxesResponse struct {
	Items      []*model.Sandbox `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
	ExternalIP string           `json:"external_ip,omitempty"`
}

type DeleteSandboxResponse struct {
	ID         string `json:"id"`
	Phase      string `json:"phase"`
	ExternalIP string `json:"external_ip,omitempty"`
}

type ReconcileResponse struct {
	OK         bool   `json:"ok"`
	ExternalIP string `json:"external_ip,omitempty"`
}

type ContainerLogsResponse struct {
	SandboxID  string            `json:"sandbox_id"`
	Container  string            `json:"container"`
	Logs       *sandbox.LogsPage `json:"logs"`
	ExternalIP string            `json:"external_ip,omitempty"`
}
