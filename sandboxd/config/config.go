package config

import (
	"os"
	"time"

	"sandboxd-o/pkg/envutil"
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
		CNIConfPath:            envutil.Get("SANDBOX_CNI_CONF_PATH", DefaultCNIConfPath),
		ReconcileInterval:      DefaultReconcileEvery,
		ReconcileGrace:         DefaultReconcileGrace,
		ReconcileHits:          DefaultReconcileHits,
		RuntimeBinary:          envutil.Get("SANDBOX_RUNTIME_BINARY", DefaultRuntimeBinary),
		MaxAllocPercent:        envutil.GetInt("SANDBOX_MAX_ALLOC_PERCENT", 90),
		ProvisionTimeout:       envutil.GetDuration("SANDBOX_PROVISION_TIMEOUT", DefaultProvisionTimeout),
		ContainerCreateTimeout: envutil.GetDuration("SANDBOX_CONTAINER_CREATE_TIMEOUT", DefaultContainerCreateTimeout),
		ImagePullTimeout:       envutil.GetDuration("SANDBOX_IMAGE_PULL_TIMEOUT", DefaultImagePullTimeout),
		Debug:                  envutil.GetBool("SANDBOX_DEBUG", false),
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
