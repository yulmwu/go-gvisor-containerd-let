package network

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// EnsureBridgeNetfilter makes Linux bridge traffic traverse iptables filter
// so sandbox-to-sandbox packets cannot bypass FORWARD policy.
func EnsureBridgeNetfilter() error {
	if err := ensureProcFlag("/proc/sys/net/bridge/bridge-nf-call-iptables", "1"); err != nil {
		return err
	}

	if err := ensureProcFlag("/proc/sys/net/bridge/bridge-nf-call-ip6tables", "1"); err != nil {
		return err
	}

	return nil
}

func ensureProcFlag(path, want string) error {
	if _, err := os.Stat(path); err != nil {
		// bridge sysctl nodes are unavailable until br_netfilter is loaded.
		if modErr := exec.Command("modprobe", "br_netfilter").Run(); modErr != nil {
			return fmt.Errorf("load br_netfilter for %s: %w", path, modErr)
		}

		if _, err2 := os.Stat(path); err2 != nil {
			return fmt.Errorf("missing %s after modprobe: %w", path, err2)
		}
	}

	b, err := os.ReadFile(path)
	if err == nil && strings.TrimSpace(string(b)) == want {
		return nil
	}

	// Write desired runtime value directly so sandboxd can self-heal hosts
	// where bridge netfilter flags were not pre-configured.
	if err := os.WriteFile(path, []byte(want+"\n"), 0o644); err != nil {
		return fmt.Errorf("set %s=%s: %w", path, want, err)
	}

	return nil
}
