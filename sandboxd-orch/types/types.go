package types

import "time"

type NodeState string
type SandboxPhase string

const (
	NodeStateUnknown  NodeState = "Unknown"
	NodeStateReady    NodeState = "Ready"
	NodeStateNotReady NodeState = "NotReady"
)

const (
	SandboxPhasePending   SandboxPhase = "Pending"
	SandboxPhaseScheduled SandboxPhase = "Scheduled"
	SandboxPhaseRunning   SandboxPhase = "Running"
	SandboxPhaseFailed    SandboxPhase = "Failed"
	SandboxPhaseDeleting  SandboxPhase = "Deleting"
)

type Node struct {
	Name          string        `json:"name"`
	IP            string        `json:"ip"`
	Port          int           `json:"port"`
	State         NodeState     `json:"state"`
	Source        string        `json:"source"`
	LastError     string        `json:"last_error,omitempty"`
	SuccessStreak int           `json:"success_streak"`
	FailureStreak int           `json:"failure_streak"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
	LastHeartbeat *time.Time    `json:"last_heartbeat,omitempty"`
	Resources     NodeResources `json:"resources"`
	SbxletBaseURL string        `json:"sbxlet_base_url"`
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
	ExternalIP          string     `json:"external_ip,omitempty"`
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

type Sandbox struct {
	ID        string        `json:"id"`
	Spec      SandboxSpec   `json:"spec"`
	Status    SandboxStatus `json:"status"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	DeletedAt *time.Time    `json:"deleted_at,omitempty"`
}

type SandboxSpec struct {
	Egress     bool                   `json:"egress" yaml:"egress"`
	TTLSeconds int64                  `json:"ttl_seconds,omitempty" yaml:"ttl_seconds,omitempty"`
	Ports      []SandboxPortSpec      `json:"ports,omitempty" yaml:"ports,omitempty"`
	Containers []SandboxContainerSpec `json:"containers" yaml:"containers"`
}

type SandboxContainerSpec struct {
	Name     string          `json:"name" yaml:"name"`
	Image    string          `json:"image" yaml:"image"`
	Args     []string        `json:"args,omitempty" yaml:"args,omitempty"`
	Env      []string        `json:"env,omitempty" yaml:"env,omitempty"`
	WorkDir  string          `json:"work_dir,omitempty" yaml:"work_dir,omitempty"`
	Resource SandboxResource `json:"resource" yaml:"resource"`
}

type SandboxResource struct {
	CPU    string `json:"cpu" yaml:"cpu"`
	Memory string `json:"memory" yaml:"memory"`
}

type SandboxPortSpec struct {
	HostPort      int    `json:"host_port,omitempty" yaml:"host_port,omitempty"`
	ContainerPort int    `json:"container_port" yaml:"container_port"`
	Protocol      string `json:"protocol,omitempty" yaml:"protocol,omitempty"`
}

type SandboxStatus struct {
	Phase         SandboxPhase        `json:"phase"`
	NodeName      string              `json:"node_name,omitempty"`
	ExternalIP    string              `json:"external_ip,omitempty"`
	AssignedPorts []SandboxPortAssign `json:"assigned_ports,omitempty"`
	SandboxdID    string              `json:"sandboxd_id,omitempty"`
	ExpireAt      *time.Time          `json:"expire_at,omitempty"`
	LastError     string              `json:"last_error,omitempty"`
}

type SandboxPortAssign struct {
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
}

type CreateSandboxObjectRequest struct {
	ID   string      `json:"id" yaml:"id"`
	Spec SandboxSpec `json:"spec" yaml:"spec"`
}
