package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sandboxd-o/orchestrator/types"
	"sandboxd-o/pkg/envutil"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

const (
	defaultHTTPAddr              = ":8082"
	defaultConfigPath            = "configs/apiserver.yaml"
	defaultSQLitePath            = "/var/lib/sandboxd/orchestrator.db"
	defaultHeartbeatInterval     = 10 * time.Second
	defaultProbeTimeout          = 3 * time.Second
	defaultHeartbeatParallel     = false
	defaultHeartbeatMaxParallel  = 4
	defaultResourcePersistMinInt = 30 * time.Second
	defaultResourcePersistMaxInt = 5 * time.Minute
	defaultReadySuccessThreshold = 2
	defaultNotReadyFailThreshold = 2
	defaultShutdownTimeout       = 5 * time.Second
)

type Config struct {
	HTTPAddr                 string
	ConfigPath               string
	SQLitePath               string
	HeartbeatInterval        time.Duration
	ProbeTimeout             time.Duration
	HeartbeatParallel        bool
	HeartbeatMaxParallel     int
	ResourcePersistMinInt    time.Duration
	ResourcePersistMaxInt    time.Duration
	ReadySuccessThreshold    int
	NotReadyFailureThreshold int
	ShutdownTimeout          time.Duration
	Bootstrap                types.APIServerConfig
}

func Load() (Config, error) {
	_ = godotenv.Load()

	cfgPath := envutil.Get("ORCH_CONFIG_PATH", defaultConfigPath)
	boot, err := loadBootstrap(cfgPath)
	if err != nil {
		return Config{}, err
	}

	addr := strings.TrimSpace(envutil.Get("ORCH_HTTP_ADDR", ""))
	if addr == "" {
		addr = strings.TrimSpace(boot.ListenAddress)
	}

	if addr == "" {
		addr = defaultHTTPAddr
	}

	cfg := Config{
		HTTPAddr:                 addr,
		ConfigPath:               cfgPath,
		SQLitePath:               envutil.Get("ORCH_SQLITE_PATH", defaultSQLitePath),
		HeartbeatInterval:        envutil.GetDuration("ORCH_HEARTBEAT_INTERVAL", defaultHeartbeatInterval),
		ProbeTimeout:             envutil.GetDuration("ORCH_NODE_PROBE_TIMEOUT", defaultProbeTimeout),
		HeartbeatParallel:        envutil.GetBool("ORCH_HEARTBEAT_PARALLEL", defaultHeartbeatParallel),
		HeartbeatMaxParallel:     envutil.GetInt("ORCH_HEARTBEAT_MAX_PARALLEL", defaultHeartbeatMaxParallel),
		ResourcePersistMinInt:    envutil.GetDuration("ORCH_RESOURCE_PERSIST_MIN_INTERVAL", defaultResourcePersistMinInt),
		ResourcePersistMaxInt:    envutil.GetDuration("ORCH_RESOURCE_PERSIST_MAX_INTERVAL", defaultResourcePersistMaxInt),
		ReadySuccessThreshold:    envutil.GetInt("ORCH_READY_SUCCESS_THRESHOLD", defaultReadySuccessThreshold),
		NotReadyFailureThreshold: envutil.GetInt("ORCH_NOTREADY_FAILURE_THRESHOLD", defaultNotReadyFailThreshold),
		ShutdownTimeout:          envutil.GetDuration("ORCH_SHUTDOWN_TIMEOUT", defaultShutdownTimeout),
		Bootstrap:                boot,
	}

	if cfg.ReadySuccessThreshold < 1 {
		cfg.ReadySuccessThreshold = 1
	}

	if cfg.NotReadyFailureThreshold < 1 {
		cfg.NotReadyFailureThreshold = 1
	}

	if cfg.HeartbeatMaxParallel < 1 {
		cfg.HeartbeatMaxParallel = 1
	}

	if cfg.ResourcePersistMinInt <= 0 {
		cfg.ResourcePersistMinInt = defaultResourcePersistMinInt
	}

	if cfg.ResourcePersistMaxInt <= 0 {
		cfg.ResourcePersistMaxInt = defaultResourcePersistMaxInt
	}

	if cfg.SQLitePath != ":memory:" {
		dir := filepath.Dir(cfg.SQLitePath)
		if dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return Config{}, fmt.Errorf("create sqlite dir: %w", err)
			}
		}
	}

	return cfg, nil
}

func loadBootstrap(path string) (types.APIServerConfig, error) {
	b := types.APIServerConfig{}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return b, nil
		}

		return b, fmt.Errorf("read config file %q: %w", path, err)
	}

	if err := yaml.Unmarshal(raw, &b); err != nil {
		return b, fmt.Errorf("parse config file %q: %w", path, err)
	}

	return b, nil
}
