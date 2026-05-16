package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type criClient struct {
	conn    *grpc.ClientConn
	runtime runtimeapi.RuntimeServiceClient
	image   runtimeapi.ImageServiceClient
}

type criContainerDetails struct {
	ID    string
	State runtimeapi.ContainerState
	PID   uint32
}

func newCRIClient(ctx context.Context, endpoint string) (*criClient, error) {
	conn, err := grpc.NewClient(
		"unix://"+normalizeCRIEndpoint(endpoint),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("dial cri endpoint %q: %w", endpoint, err)
	}

	return &criClient{
		conn:    conn,
		runtime: runtimeapi.NewRuntimeServiceClient(conn),
		image:   runtimeapi.NewImageServiceClient(conn),
	}, nil
}

func (c *criClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}

	return c.conn.Close()
}

func (c *criClient) pullImage(ctx context.Context, image string) error {
	_, err := c.image.PullImage(ctx, &runtimeapi.PullImageRequest{
		Image: &runtimeapi.ImageSpec{Image: image},
	})
	return err
}

func (c *criClient) runPodSandbox(ctx context.Context, cfg *runtimeapi.PodSandboxConfig, runtimeHandler string) (string, error) {
	resp, err := c.runtime.RunPodSandbox(ctx, &runtimeapi.RunPodSandboxRequest{
		Config:         cfg,
		RuntimeHandler: runtimeHandler,
	})
	if err != nil {
		return "", err
	}

	return resp.GetPodSandboxId(), nil
}

func (c *criClient) podSandboxStatus(ctx context.Context, podID string) (*runtimeapi.PodSandboxStatus, error) {
	resp, err := c.runtime.PodSandboxStatus(ctx, &runtimeapi.PodSandboxStatusRequest{PodSandboxId: podID})
	if err != nil {
		return nil, err
	}

	return resp.GetStatus(), nil
}

func (c *criClient) createContainer(ctx context.Context, podID string, container *runtimeapi.ContainerConfig, sbxCfg *runtimeapi.PodSandboxConfig) (string, error) {
	resp, err := c.runtime.CreateContainer(ctx, &runtimeapi.CreateContainerRequest{
		PodSandboxId:  podID,
		Config:        container,
		SandboxConfig: sbxCfg,
	})
	if err != nil {
		return "", err
	}

	return resp.GetContainerId(), nil
}

func (c *criClient) startContainer(ctx context.Context, containerID string) error {
	_, err := c.runtime.StartContainer(ctx, &runtimeapi.StartContainerRequest{
		ContainerId: containerID,
	})
	return err
}

func (c *criClient) containerStatus(ctx context.Context, containerID string) (*criContainerDetails, error) {
	resp, err := c.runtime.ContainerStatus(ctx, &runtimeapi.ContainerStatusRequest{
		ContainerId: containerID,
		Verbose:     true,
	})
	if err != nil {
		return nil, err
	}

	st := resp.GetStatus()
	d := &criContainerDetails{
		ID:    st.GetId(),
		State: st.GetState(),
		PID:   parseContainerPID(resp.GetInfo()),
	}
	return d, nil
}

func (c *criClient) stopAndRemovePodSandbox(ctx context.Context, podID string) {
	if podID == "" {
		return
	}

	_, _ = c.runtime.StopPodSandbox(ctx, &runtimeapi.StopPodSandboxRequest{PodSandboxId: podID})
	_, _ = c.runtime.RemovePodSandbox(ctx, &runtimeapi.RemovePodSandboxRequest{PodSandboxId: podID})
}

func (c *criClient) listPodSandboxes(ctx context.Context) ([]*runtimeapi.PodSandbox, error) {
	resp, err := c.runtime.ListPodSandbox(ctx, &runtimeapi.ListPodSandboxRequest{})
	if err != nil {
		return nil, err
	}

	return resp.GetItems(), nil
}

func normalizeCRIEndpoint(addr string) string {
	s := strings.TrimSpace(addr)
	s = strings.TrimPrefix(s, "unix://")
	s = strings.TrimPrefix(s, "unix:")
	return s
}

func parseContainerPID(info map[string]string) uint32 {
	for _, raw := range info {
		var payload struct {
			Pid int `json:"pid"`
		}

		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			continue
		}

		if payload.Pid > 0 {
			return uint32(payload.Pid)
		}
	}

	return 0
}
