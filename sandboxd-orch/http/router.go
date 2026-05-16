package http

import (
	"sandboxd-o/pkg/httplog"
	"sandboxd-o/pkg/logging"
	"sandboxd-o/sandboxd-orch/config"
	docs "sandboxd-o/sandboxd-orch/docs"
	"sandboxd-o/sandboxd-orch/http/handlers"
	"sandboxd-o/sandboxd-orch/service"

	"github.com/gin-gonic/gin"
	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

func NewRouter(svc *service.Service, cfg config.Config, logger *logging.Logger) *gin.Engine {
	docs.SwaggerInfoorchestrator.BasePath = "/"

	r := gin.New()
	r.Use(httplog.RecoveryLogger(logger))
	r.Use(httplog.RequestLogger(logger))

	h := handlers.New(svc, cfg)

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerfiles.Handler, ginSwagger.InstanceName("orchestrator")))
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

		api.POST("/sandboxes", h.CreateSandbox)
		api.GET("/sandboxes", h.ListSandboxes)
		api.GET("/sandboxes/:id", h.GetSandbox)
		api.DELETE("/sandboxes/:id", h.DeleteSandbox)
	}

	return r
}
