package types

import (
	"encoding/json"
	"strings"
	"time"
)

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
	ID            string        `json:"id"`
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
	External            string     `json:"-"`
	UpdatedAt           *time.Time `json:"updated_at,omitempty"`
}

type RegisterNodeRequest struct {
	ID   string `json:"id"`
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

type APIServerConfig struct {
	ListenAddress string `yaml:"listenAddress"`
}

type StaticNode struct {
	ID   string `yaml:"id"`
	IP   string `yaml:"ip"`
	Port int    `yaml:"port"`
}

type CreateNodeObjectRequest struct {
	ID   string         `json:"id" yaml:"id"`
	Spec NodeObjectSpec `json:"spec" yaml:"spec"`
}

type NodeObjectSpec struct {
	IP   string `json:"ip" yaml:"ip"`
	Port int    `json:"port" yaml:"port"`
}

type CreateExternalObjectRequest struct {
	ID   string             `json:"id" yaml:"id"`
	Spec ExternalObjectSpec `json:"spec" yaml:"spec"`
}

type ExternalObjectSpec struct {
	NodeID   string `json:"node_id" yaml:"node_id"`
	External string `json:"external" yaml:"external"`
}

type External struct {
	ID        string    `json:"id"`
	NodeID    string    `json:"node_id"`
	External  string    `json:"external"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
	External      string              `json:"-"`
	IP            string              `json:"ip,omitempty"`
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

func (r NodeResources) MarshalJSON() ([]byte, error) {
	type alias NodeResources
	ext := strings.TrimSpace(r.External)
	return json.Marshal(struct {
		alias
		External string `json:"external,omitempty"`
	}{
		alias:    alias(r),
		External: ext,
	})
}

func (r *NodeResources) UnmarshalJSON(b []byte) error {
	type alias NodeResources
	aux := struct {
		alias
		External string `json:"external,omitempty"`
	}{}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	*r = NodeResources(aux.alias)
	r.External = strings.TrimSpace(aux.External)
	return nil
}

func (s SandboxStatus) MarshalJSON() ([]byte, error) {
	type alias SandboxStatus
	ext := strings.TrimSpace(s.External)
	return json.Marshal(struct {
		alias
		External string `json:"external,omitempty"`
	}{
		alias:    alias(s),
		External: ext,
	})
}

func (s *SandboxStatus) UnmarshalJSON(b []byte) error {
	type alias SandboxStatus
	aux := struct {
		alias
		External string `json:"external,omitempty"`
	}{}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	*s = SandboxStatus(aux.alias)
	s.External = strings.TrimSpace(aux.External)
	return nil
}
