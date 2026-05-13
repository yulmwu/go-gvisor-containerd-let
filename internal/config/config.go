package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultContainerdAddr         = "/run/containerd/containerd.sock"
	DefaultStateBaseDir           = "/var/lib/sandboxd/sandboxes"
	DefaultLockDir                = "/var/lib/sandboxd/locks"
	DefaultBridgeInterface        = "sbx-br0"
	DefaultSubnetCIDR             = "10.89.0.0/16"
	DefaultCNIConfPath            = "/etc/cni/sandboxd.d/20-sbxnet.conflist"
	DefaultRuntimeBinary          = "runsc"
	DefaultReadyTimeout           = 20 * time.Second
	DefaultReconcileEvery         = 15 * time.Second
	DefaultReconcileGrace         = 5 * time.Second
	DefaultReconcileHits          = 2
	DefaultLockWaitTimeout        = 20 * time.Second
	DefaultProvisionTimeout       = 4 * time.Minute
	DefaultContainerCreateTimeout = 2 * time.Minute
	DefaultImagePullTimeout       = 8 * time.Minute
)

type Config struct {
	ContainerdAddress      string
	StateBaseDir           string
	LockDir                string
	BridgeInterface        string
	SubnetCIDR             string
	CNIConfPath            string
	ReconcileInterval      time.Duration
	ReconcileGrace         time.Duration
	ReconcileHits          int
	RuntimeBinary          string
	MaxAllocPercent        int
	ProvisionTimeout       time.Duration
	ContainerCreateTimeout time.Duration
	ImagePullTimeout       time.Duration
	Debug                  bool
}

func DefaultConfig() Config {
	addr := os.Getenv("SANDBOX_CONTAINERD_ADDRESS")
	if addr == "" {
		addr = DefaultContainerdAddr
	}

	return Config{
		ContainerdAddress:      addr,
		StateBaseDir:           DefaultStateBaseDir,
		LockDir:                DefaultLockDir,
		BridgeInterface:        DefaultBridgeInterface,
		SubnetCIDR:             DefaultSubnetCIDR,
		CNIConfPath:            Getenv("SANDBOX_CNI_CONF_PATH", DefaultCNIConfPath),
		ReconcileInterval:      DefaultReconcileEvery,
		ReconcileGrace:         DefaultReconcileGrace,
		ReconcileHits:          DefaultReconcileHits,
		RuntimeBinary:          Getenv("SANDBOX_RUNTIME_BINARY", DefaultRuntimeBinary),
		MaxAllocPercent:        GetenvInt("SANDBOX_MAX_ALLOC_PERCENT", 90),
		ProvisionTimeout:       GetenvDuration("SANDBOX_PROVISION_TIMEOUT", DefaultProvisionTimeout),
		ContainerCreateTimeout: GetenvDuration("SANDBOX_CONTAINER_CREATE_TIMEOUT", DefaultContainerCreateTimeout),
		ImagePullTimeout:       GetenvDuration("SANDBOX_IMAGE_PULL_TIMEOUT", DefaultImagePullTimeout),
		Debug:                  strings.EqualFold(os.Getenv("SANDBOX_DEBUG"), "true") || os.Getenv("SANDBOX_DEBUG") == "1",
	}
}

func WithConfigDefaults(cfg Config) Config {
	if cfg.ContainerdAddress == "" {
		cfg.ContainerdAddress = DefaultContainerdAddr
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

	if cfg.ProvisionTimeout <= 0 {
		cfg.ProvisionTimeout = DefaultProvisionTimeout
	}

	if cfg.ContainerCreateTimeout <= 0 {
		cfg.ContainerCreateTimeout = DefaultContainerCreateTimeout
	}

	if cfg.ImagePullTimeout <= 0 {
		cfg.ImagePullTimeout = DefaultImagePullTimeout
	}

	return cfg
}

func Getenv(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}

func GetenvInt(k string, def int) int {
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

func GetenvDuration(k string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}

	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}

	return d
}

func SplitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.ToUpper(strings.TrimSpace(p)))
	}

	return out
}

func CsvEnv(key string, def []string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return append([]string(nil), def...)
	}

	out := make([]string, 0)
	for _, p := range SplitCSV(raw) {
		if p != "" {
			out = append(out, p)
		}
	}

	if len(out) == 0 {
		return append([]string(nil), def...)
	}

	return out
}
