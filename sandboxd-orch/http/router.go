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
		api.POST("/sandboxes", h.CreateSandbox)
		api.GET("/sandboxes", h.ListSandboxes)
		api.GET("/sandboxes/:id", h.GetSandbox)
		api.DELETE("/sandboxes/:id", h.DeleteSandbox)

		api.POST("/nodes", h.CreateNodeObject)
		api.GET("/nodes", h.ListNodes)
		api.GET("/nodes/:id", h.GetNode)
		api.DELETE("/nodes/:id", h.DeleteNode)

		api.POST("/externals", h.UpsertExternalObject)
		api.GET("/externals", h.ListExternals)
		api.GET("/externals/:id", h.GetExternal)
		api.DELETE("/externals/:id", h.DeleteExternal)

		api.POST("/nodes/:id/heartbeat", h.HeartbeatNode)
		api.GET("/nodes/:id/sandboxes", h.NodeListSandboxes)
		api.GET("/nodes/:id/sandboxes/:sandboxId", h.NodeGetSandbox)
		api.POST("/nodes/:id/sandboxes", h.NodeCreateSandbox)
		api.DELETE("/nodes/:id/sandboxes/:sandboxId", h.NodeDeleteSandbox)
		api.GET("/nodes/:id/sandboxes/:sandboxId/containers/:container/logs", h.NodeContainerLogs)
		api.POST("/nodes/:id/reconcile", h.NodeReconcile)
	}

	return r
}
