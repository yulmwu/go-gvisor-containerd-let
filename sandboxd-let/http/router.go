package httpserver

import (
	"sandboxd-o/pkg/httplog"
	docs "sandboxd-o/sandboxd-let/docs"

	"github.com/gin-gonic/gin"
	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

func newRouter(s *Server) *gin.Engine {
	docs.SwaggerInfosandboxd.BasePath = "/"

	r := gin.New()
	r.Use(httplog.RecoveryLogger(s.log))
	r.Use(httplog.RequestLogger(s.log))

	r.GET("/", func(c *gin.Context) {
		c.File("assets/rest-ui.html")
	})
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerfiles.Handler, ginSwagger.InstanceName("sandboxd")))
	r.GET("/healthz", s.healthz)
	r.GET("/v1/node/status", s.nodeStatus)
	r.GET("/v1/sandboxes", s.listSandboxes)
	r.GET("/v1/sandboxes/:id", s.getSandbox)
	r.GET("/v1/sandboxes/:id/containers/:name/logs", s.getContainerLogs)
	r.POST("/v1/sandboxes/statuses", s.sandboxStatuses)
	r.POST("/v1/sandboxes", s.createSandbox)
	r.DELETE("/v1/sandboxes/:id", s.deleteSandbox)
	r.POST("/v1/reconcile", s.reconcile)

	return r
}
