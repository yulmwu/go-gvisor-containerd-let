package sandbox

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"sandboxd-o/sandboxd-let/model"

	"k8s.io/apimachinery/pkg/api/resource"
)

type parsedResource struct {
	CPUMilli    int64
	MemoryBytes int64
}

func parseContainerResource(in model.ResourceSpec) (parsedResource, error) {
	cpuMilli, err := parseCPUMilli(in.CPU)
	if err != nil {
		return parsedResource{}, err
	}

	memBytes, err := parseMemoryBytes(in.Memory)
	if err != nil {
		return parsedResource{}, err
	}

	return parsedResource{CPUMilli: cpuMilli, MemoryBytes: memBytes}, nil
}

func parseCPUMilli(raw string) (int64, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, fmt.Errorf("cpu is required")
	}

	q, err := resource.ParseQuantity(v)
	if err != nil {
		return 0, fmt.Errorf("invalid cpu: %s", raw)
	}

	milli := q.MilliValue()
	if milli <= 0 {
		return 0, fmt.Errorf("invalid cpu: %s", raw)
	}

	return milli, nil
}

func parseMemoryBytes(raw string) (int64, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, fmt.Errorf("memory is required")
	}

	q, err := resource.ParseQuantity(v)
	if err != nil {
		return 0, fmt.Errorf("invalid memory: %s", raw)
	}

	bytes := q.Value()
	if bytes <= 0 {
		return 0, fmt.Errorf("invalid memory: %s", raw)
	}

	return bytes, nil
}

func parsedResourceToLimits(r parsedResource) model.ResourceLimits {
	const period = uint64(100000)
	quota := max((r.CPUMilli*int64(period))/1000, 1000)

	return model.ResourceLimits{
		MemoryBytes: r.MemoryBytes,
		CPUQuota:    quota,
		CPUPeriod:   period,
		PidsLimit:   128,
	}
}

func (s *Service) enforceAdmission(req model.CreateSandboxRequest) error {
	requestCPU := int64(0)
	requestMem := int64(0)
	for _, c := range req.Containers {
		r, err := parseContainerResource(c.Resource)
		if err != nil {
			return fmt.Errorf("container %s resource parse failed: %w", c.Name, err)
		}

		requestCPU += r.CPUMilli
		requestMem += r.MemoryBytes
	}

	usedCPU, usedMem, err := s.currentAllocatedResources(req.ID)
	if err != nil {
		return err
	}

	hostCPU := int64(runtime.NumCPU() * 1000)
	hostMem, err := hostTotalMemoryBytes()
	if err != nil {
		return err
	}

	maxCPU := hostCPU * int64(s.cfg.MaxAllocPercent) / 100
	maxMem := hostMem * int64(s.cfg.MaxAllocPercent) / 100

	if usedCPU+requestCPU > maxCPU {
		return fmt.Errorf("insufficient cpu allocatable: requested=%dm used=%dm limit=%dm (%d%% of host)", requestCPU, usedCPU, maxCPU, s.cfg.MaxAllocPercent)
	}

	if usedMem+requestMem > maxMem {
		return fmt.Errorf("insufficient memory allocatable: requested=%d used=%d limit=%d (%d%% of host)", requestMem, usedMem, maxMem, s.cfg.MaxAllocPercent)
	}

	return nil
}

func (s *Service) NodeResourceSnapshot(ctx context.Context) (model.NodeResourceSnapshot, error) {
	_ = ctx

	hostCPU := int64(runtime.NumCPU() * 1000)
	hostMem, err := hostTotalMemoryBytes()
	if err != nil {
		return model.NodeResourceSnapshot{}, err
	}

	usedCPU, usedMem, err := s.currentAllocatedResources("")
	if err != nil {
		return model.NodeResourceSnapshot{}, err
	}

	allocCPU := hostCPU * int64(s.cfg.MaxAllocPercent) / 100
	allocMem := hostMem * int64(s.cfg.MaxAllocPercent) / 100

	availCPU := max(allocCPU-usedCPU, 0)

	availMem := max(allocMem-usedMem, 0)

	return model.NodeResourceSnapshot{
		CapacityCPUMilli:    hostCPU,
		CapacityMemoryBytes: hostMem,
		AllocatableCPUMilli: allocCPU,
		AllocatableMemory:   allocMem,
		UsedCPUMilli:        usedCPU,
		UsedMemoryBytes:     usedMem,
		AvailableCPUMilli:   availCPU,
		AvailableMemory:     availMem,
		MaxAllocPercent:     s.cfg.MaxAllocPercent,
		UpdatedAt:           time.Now().UTC(),
	}, nil
}

func (s *Service) currentAllocatedResources(exceptSandboxID string) (int64, int64, error) {
	all, err := s.store.List()
	if err != nil {
		return 0, 0, err
	}

	totalCPU := int64(0)
	totalMem := int64(0)
	for _, sbx := range all {
		if sbx.ID == exceptSandboxID || sbx.Phase == SandboxPhaseDeleting || sbx.Phase == SandboxPhaseError {
			continue
		}

		for _, c := range sbx.Containers {
			r, err := parseContainerResource(c.Resource)
			if err != nil {
				continue
			}

			totalCPU += r.CPUMilli
			totalMem += r.MemoryBytes
		}
	}

	return totalCPU, totalMem, nil
}

func hostTotalMemoryBytes() (int64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 2 {
			break
		}

		v, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0, err
		}

		return v * 1024, nil
	}

	if err := sc.Err(); err != nil {
		return 0, err
	}

	return 0, fmt.Errorf("MemTotal not found")
}

func (s *Service) ensureIPCapacity(nextSandboxID string) error {
	_, ipnet, err := net.ParseCIDR(s.cidr)
	if err != nil {
		return fmt.Errorf("invalid subnet cidr: %w", err)
	}

	ones, bits := ipnet.Mask.Size()
	if bits != 32 {
		return fmt.Errorf("only ipv4 subnet is supported")
	}

	hostBits := bits - ones
	if hostBits <= 2 {
		return fmt.Errorf("subnet too small: %s", s.cidr)
	}

	usable := (uint64(1) << hostBits) - 2
	if usable <= 0 {
		return fmt.Errorf("subnet has no usable addresses: %s", s.cidr)
	}

	all, err := s.store.List()
	if err != nil {
		return err
	}
	active := 0

	for _, sbx := range all {
		if sbx.ID == nextSandboxID || sbx.Phase == SandboxPhaseDeleting || sbx.Phase == SandboxPhaseError {
			continue
		}

		active++
	}

	if uint64(active) >= usable {
		return fmt.Errorf("insufficient ip addresses in subnet %s: in_use=%d usable=%d", s.cidr, active, usable)
	}

	return nil
}
