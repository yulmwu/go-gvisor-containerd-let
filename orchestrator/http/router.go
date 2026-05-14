package http

import (
	"sandboxd-o/orchestrator/http/handlers"
	"sandboxd-o/orchestrator/service"
	"sandboxd-o/pkg/httplog"
	"sandboxd-o/pkg/logging"

	"github.com/gin-gonic/gin"
)

func NewRouter(svc *service.Service, logger *logging.Logger) *gin.Engine {
	r := gin.New()
	r.Use(httplog.RecoveryLogger(logger))
	r.Use(httplog.RequestLogger(logger))

	h := handlers.New(svc)

	r.GET("/healthz", h.Healthz)

	api := r.Group("/api/v1")
	{
		api.GET("/nodes", h.ListNodes)
		api.GET("/nodes/:name", h.GetNode)
		api.POST("/nodes/register", h.RegisterNode)
		api.DELETE("/nodes/:name", h.DeleteNode)
		api.POST("/nodes/:name/heartbeat", h.HeartbeatNode)

		api.GET("/nodes/:name/sandboxes", h.NodeListSandboxes)
		api.GET("/nodes/:name/sandboxes/:id", h.NodeGetSandbox)
		api.POST("/nodes/:name/sandboxes", h.NodeCreateSandbox)
		api.DELETE("/nodes/:name/sandboxes/:id", h.NodeDeleteSandbox)
		api.GET("/nodes/:name/sandboxes/:id/containers/:container/logs", h.NodeContainerLogs)
		api.POST("/nodes/:name/reconcile", h.NodeReconcile)
	}

	return r
}
