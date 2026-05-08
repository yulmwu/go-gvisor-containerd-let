package sandbox

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"example.com/sandbox-demo/internal/network"
	"example.com/sandbox-demo/internal/store"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/coreos/go-iptables/iptables"
)

const (
	DefaultNamespace       = "sandbox-demo"
	DefaultContainerdAddr  = "/run/containerd/containerd.sock"
	DefaultStateBaseDir    = "/var/lib/sandboxd/sandboxes"
	DefaultLockDir         = "/var/lib/sandboxd/locks"
	DefaultBridgeInterface = "sbx-br0"
	DefaultSubnetCIDR      = "10.89.0.0/16"
	DefaultCNIConfPath     = "/etc/cni/net.d/20-sbxnet.conflist"
	DefaultRuntimeBinary   = "runc"
	DefaultReadyTimeout    = 20 * time.Second
	DefaultReconcileEvery  = 15 * time.Second
	DefaultReconcileGrace  = 5 * time.Second
	DefaultReconcileHits   = 2
	DefaultLockWaitTimeout = 20 * time.Second
)

type Config struct {
	ContainerdAddress string
	Namespace         string
	StateBaseDir      string
	LockDir           string
	BridgeInterface   string
	SubnetCIDR        string
	CNIConfPath       string
	ReconcileInterval time.Duration
	ReconcileGrace    time.Duration
	ReconcileHits     int
	RuntimeBinary     string
	MaxAllocPercent   int
	Debug             bool
}

type Service struct {
	client         *containerd.Client
	ipt            *iptables.IPTables
	store          *store.FileStore
	portMu         sync.Mutex
	reservedPorts  map[string]string
	reconcileMu    sync.Mutex
	unhealthySince map[string]time.Time
	unhealthyHits  map[string]int
	cfg            Config
	namespace      string
	bridgeIF       string
	cidr           string
	containerdAddr string
	cniConfPath    string
	runtimeBinary  string
	lockDir        string
}

func DefaultConfig() Config {
	addr := os.Getenv("SANDBOX_CONTAINERD_ADDRESS")
	if addr == "" {
		addr = DefaultContainerdAddr
	}

	return Config{
		ContainerdAddress: addr,
		Namespace:         DefaultNamespace,
		StateBaseDir:      DefaultStateBaseDir,
		LockDir:           DefaultLockDir,
		BridgeInterface:   DefaultBridgeInterface,
		SubnetCIDR:        DefaultSubnetCIDR,
		ReconcileInterval: DefaultReconcileEvery,
		ReconcileGrace:    DefaultReconcileGrace,
		ReconcileHits:     DefaultReconcileHits,
		RuntimeBinary:     getenv("SANDBOX_RUNTIME_BINARY", DefaultRuntimeBinary),
		MaxAllocPercent:   getenvInt("SANDBOX_MAX_ALLOC_PERCENT", 90),
		Debug:             strings.EqualFold(os.Getenv("SANDBOX_DEBUG"), "true") || os.Getenv("SANDBOX_DEBUG") == "1",
	}
}

func New(ctx context.Context, cfg Config) (*Service, error) {
	_ = ctx
	cfg = withConfigDefaults(cfg)

	client, err := containerd.New(cfg.ContainerdAddress)
	if err != nil {
		return nil, err
	}

	ipt, err := iptables.New(iptables.Timeout(5))
	if err != nil {
		_ = client.Close()
		return nil, err
	}

	if err := network.EnsureBridgeNetfilter(); err != nil {
		_ = client.Close()
		return nil, err
	}

	st, err := store.NewFileStore(cfg.StateBaseDir)
	if err != nil {
		_ = client.Close()
		return nil, err
	}

	if err := network.EnsureGlobalChains(ipt, csvEnv("SANDBOX_FORWARD_HOOK_CHAINS", []string{"FORWARD", "DOCKER-USER"})); err != nil {
		_ = client.Close()
		return nil, err
	}

	s := &Service{
		client:         client,
		ipt:            ipt,
		store:          st,
		reservedPorts:  map[string]string{},
		unhealthySince: map[string]time.Time{},
		unhealthyHits:  map[string]int{},
		cfg:            cfg,
		namespace:      cfg.Namespace,
		bridgeIF:       cfg.BridgeInterface,
		cidr:           cfg.SubnetCIDR,
		containerdAddr: cfg.ContainerdAddress,
		cniConfPath:    cfg.CNIConfPath,
		runtimeBinary:  cfg.RuntimeBinary,
		lockDir:        cfg.LockDir,
	}
	network.SetDebugLogger(s.dbg)

	if err := os.MkdirAll(s.lockDir, 0o755); err != nil {
		_ = client.Close()
		return nil, err
	}

	return s, nil
}

func (s *Service) Close() error { return s.client.Close() }

func withConfigDefaults(cfg Config) Config {
	if cfg.ContainerdAddress == "" {
		cfg.ContainerdAddress = DefaultContainerdAddr
	}

	if cfg.Namespace == "" {
		cfg.Namespace = DefaultNamespace
	}

	if cfg.StateBaseDir == "" {
		cfg.StateBaseDir = DefaultStateBaseDir
	}

	if cfg.LockDir == "" {
		cfg.LockDir = DefaultLockDir
	}

	if cfg.BridgeInterface == "" {
		cfg.BridgeInterface = DefaultBridgeInterface
	}

	if cfg.SubnetCIDR == "" {
		cfg.SubnetCIDR = DefaultSubnetCIDR
	}

	if cfg.CNIConfPath == "" {
		cfg.CNIConfPath = DefaultCNIConfPath
	}

	if cfg.ReconcileInterval <= 0 {
		cfg.ReconcileInterval = DefaultReconcileEvery
	}

	if cfg.ReconcileGrace <= 0 {
		cfg.ReconcileGrace = DefaultReconcileGrace
	}

	if cfg.ReconcileHits < 1 {
		cfg.ReconcileHits = DefaultReconcileHits
	}
	if cfg.RuntimeBinary == "" {
		cfg.RuntimeBinary = DefaultRuntimeBinary
	}
	if cfg.MaxAllocPercent <= 0 || cfg.MaxAllocPercent > 100 {
		cfg.MaxAllocPercent = 90
	}

	return cfg
}

func getenv(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}

func getenvInt(k string, def int) int {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}

	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}

	return n
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.ToUpper(strings.TrimSpace(p)))
	}

	return out
}

func csvEnv(key string, def []string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return append([]string(nil), def...)
	}

	out := make([]string, 0)
	for _, p := range splitCSV(raw) {
		if p != "" {
			out = append(out, p)
		}
	}

	if len(out) == 0 {
		return append([]string(nil), def...)
	}

	return out
}

func (s *Service) dbg(format string, args ...any) {
	if !s.cfg.Debug {
		return
	}

	log.Printf("[sandbox-debug] "+format, args...)
}
