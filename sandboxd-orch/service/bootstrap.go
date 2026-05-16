package service

import (
	"context"
	"log/slog"
)

func (s *Service) BootstrapNodes(ctx context.Context) error {
	seen := make(map[string]int)
	for _, n := range s.cfg.Bootstrap.Nodes {
		if err := validateNodeInput(n.Name, n.IP, n.Port); err != nil {
			slog.Warn("skip invalid bootstrap node",
				slog.String("name", n.Name),
				slog.String("ip", n.IP),
				slog.Int("port", n.Port),
				slog.Any("error", err),
			)
			continue
		}

		seen[n.Name]++
		if seen[n.Name] > 1 {
			slog.Warn("duplicate bootstrap node name detected; last entry wins",
				slog.String("name", n.Name),
				slog.String("ip", n.IP),
				slog.Int("port", n.Port),
			)
		}

		if err := s.repo.UpsertNode(ctx, n.Name, n.IP, n.Port, "config"); err != nil {
			return err
		}
	}

	return nil
}
