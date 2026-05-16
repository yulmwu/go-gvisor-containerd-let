package httpserver

import (
	"net/http"

	"sandboxd-o/pkg/logging"
	"sandboxd-o/sandboxd-let/sandbox"
)

type Server struct {
	svc   *sandbox.Service
	ipSvc externalIPService
	log   *logging.Logger
}

func New(svc *sandbox.Service, logger *logging.Logger) *Server {
	return &Server{svc: svc, ipSvc: commandExternalIPService{}, log: logger}
}

func (s *Server) Handler() http.Handler {
	return newRouter(s)
}
