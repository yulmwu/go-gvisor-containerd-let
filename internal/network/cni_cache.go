package network

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

type cniCacheFile struct {
	Result struct {
		IPs []struct {
			Address string `json:"address"`
		} `json:"ips"`
	} `json:"result"`
}

// LookupSandboxIPv4FromResultCache reads CNI result cache for a sandbox id
// and returns the assigned IPv4 address.
func LookupSandboxIPv4FromResultCache(sandboxID string) (string, error) {
	if sandboxID == "" {
		return "", fmt.Errorf("sandbox id is required")
	}

	candidates := make([]string, 0)
	resultDir := filepath.Join("/var/lib/cni", sandboxID, "results")
	if entries, err := os.ReadDir(resultDir); err == nil {
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}

			candidates = append(candidates, filepath.Join(resultDir, ent.Name()))
		}
	}

	globalDir := "/var/lib/cni/results"
	if entries, err := os.ReadDir(globalDir); err == nil {
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}

			if !containsSandboxID(ent.Name(), sandboxID) {
				continue
			}

			candidates = append(candidates, filepath.Join(globalDir, ent.Name()))
		}
	}

	for _, cachePath := range candidates {
		ip, err := parseIPv4FromCNIResultFile(cachePath)
		if err == nil {
			return ip, nil
		}
	}

	return "", fmt.Errorf("no ipv4 cni cache found for sandbox: %s", sandboxID)
}

func containsSandboxID(name, sandboxID string) bool {
	return strings.Contains(name, sandboxID)
}

func parseIPv4FromCNIResultFile(cachePath string) (string, error) {
	b, err := os.ReadFile(cachePath)
	if err != nil {
		return "", err
	}

	var cf cniCacheFile
	if err := json.Unmarshal(b, &cf); err != nil {
		return "", fmt.Errorf("parse cni cache %s: %w", cachePath, err)
	}

	for _, ip := range cf.Result.IPs {
		pfx, err := netip.ParsePrefix(ip.Address)
		if err != nil {
			continue
		}

		addr := pfx.Addr()
		if addr.Is4() {
			return addr.String(), nil
		}
	}

	return "", fmt.Errorf("no ipv4 in %s", cachePath)
}
