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
	OK         bool                       `json:"ok"`
	Resources  model.NodeResourceSnapshot `json:"resources"`
	ExternalIP string                     `json:"external_ip,omitempty"`
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

type SandboxStatusesRequest struct {
	IDs []string `json:"ids"`
}

type ContainerSyncStatus struct {
	Name       string `json:"name"`
	Phase      string `json:"phase"`
	Error      string `json:"error,omitempty"`
	TaskStatus string `json:"task_status,omitempty"`
}

type SandboxSyncStatus struct {
	ID                  string                `json:"id"`
	Phase               string                `json:"phase"`
	Error               string                `json:"error,omitempty"`
	UnhealthyContainers []ContainerSyncStatus `json:"unhealthy_containers,omitempty"`
}

type SandboxStatusesResponse struct {
	Items      []SandboxSyncStatus `json:"items"`
	Missing    []string            `json:"missing,omitempty"`
	ExternalIP string              `json:"external_ip,omitempty"`
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
