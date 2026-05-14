package types

import "time"

type NodeState string

const (
	NodeStateUnknown  NodeState = "Unknown"
	NodeStateReady    NodeState = "Ready"
	NodeStateNotReady NodeState = "NotReady"
)

type Node struct {
	Name            string        `json:"name"`
	IP              string        `json:"ip"`
	Port            int           `json:"port"`
	State           NodeState     `json:"state"`
	Source          string        `json:"source"`
	LastError       string        `json:"last_error,omitempty"`
	SuccessStreak   int           `json:"success_streak"`
	FailureStreak   int           `json:"failure_streak"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
	LastHeartbeat   *time.Time    `json:"last_heartbeat,omitempty"`
	Resources       NodeResources `json:"resources"`
	SandboxdBaseURL string        `json:"sandboxd_base_url"`
}

type NodeResources struct {
	CapacityCPUMilli    int64      `json:"capacity_cpu_milli"`
	CapacityMemoryBytes int64      `json:"capacity_memory_bytes"`
	AllocatableCPUMilli int64      `json:"allocatable_cpu_milli"`
	AllocatableMemory   int64      `json:"allocatable_memory_bytes"`
	UsedCPUMilli        int64      `json:"used_cpu_milli"`
	UsedMemoryBytes     int64      `json:"used_memory_bytes"`
	AvailableCPUMilli   int64      `json:"available_cpu_milli"`
	AvailableMemory     int64      `json:"available_memory_bytes"`
	MaxAllocPercent     int        `json:"max_alloc_percent"`
	UpdatedAt           *time.Time `json:"updated_at,omitempty"`
}

type RegisterNodeRequest struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

type APIServerConfig struct {
	ListenAddress string       `yaml:"listenAddress"`
	Nodes         []StaticNode `yaml:"nodes"`
}

type StaticNode struct {
	Name string `yaml:"name"`
	IP   string `yaml:"ip"`
	Port int    `yaml:"port"`
}
