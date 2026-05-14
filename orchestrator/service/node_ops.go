package service

import (
	"context"
	"fmt"
	"strings"

	"sandboxd-o/orchestrator/client"
	"sandboxd-o/orchestrator/types"
)

func (s *Service) RegisterNode(ctx context.Context, req types.RegisterNodeRequest, source string) (*types.Node, error) {
	if err := validateNodeInput(req.Name, req.IP, req.Port); err != nil {
		return nil, err
	}

	if source == "" {
		source = "api"
	}

	if err := s.repo.UpsertNode(ctx, req.Name, req.IP, req.Port, source); err != nil {
		return nil, err
	}

	n, err := s.repo.GetNode(ctx, req.Name)
	if err != nil {
		return nil, err
	}

	s.probeNode(ctx, *n)
	s.syncNodeResources(ctx, *n)
	return s.repo.GetNode(ctx, req.Name)
}

func (s *Service) DeleteNode(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidInput)
	}

	if err := s.repo.DeleteNode(ctx, name); err != nil {
		return err
	}

	s.resources.Delete(name)
	return nil
}

func (s *Service) ListNodes(ctx context.Context) ([]types.Node, error) {
	return s.repo.ListNodes(ctx)
}

func (s *Service) GetNode(ctx context.Context, name string) (*types.Node, error) {
	return s.repo.GetNode(ctx, name)
}

func (s *Service) SandboxClientForNode(ctx context.Context, name string) (*client.Client, *types.Node, error) {
	n, err := s.repo.GetNode(ctx, name)
	if err != nil {
		return nil, nil, err
	}

	c := client.New(n.SandboxdBaseURL, s.cfg.ProbeTimeout)
	return c, n, nil
}
