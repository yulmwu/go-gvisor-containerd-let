package service

import (
	"time"

	"sandboxd-o/orchestrator/cache"
	"sandboxd-o/orchestrator/config"
	"sandboxd-o/orchestrator/repo"
)

type Service struct {
	cfg       config.Config
	repo      repo.NodeRepo
	resources *cache.ResourceCache
}

func New(cfg config.Config) (*Service, error) {
	st, err := repo.NewSQLite(cfg.SQLitePath)
	if err != nil {
		return nil, err
	}

	return &Service{cfg: cfg, repo: st, resources: cache.NewResourceCache()}, nil
}

func (s *Service) Close() error                   { return s.repo.Close() }
func (s *Service) HTTPAddr() string               { return s.cfg.HTTPAddr }
func (s *Service) ShutdownTimeout() time.Duration { return s.cfg.ShutdownTimeout }
