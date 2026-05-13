package httpserver

import "github.com/gin-gonic/gin"

func newRouter(s *Server) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/", func(c *gin.Context) {
		c.File("assets/rest-ui.html")
	})
	r.GET("/healthz", s.healthz)
	r.GET("/v1/sandboxes", s.listSandboxes)
	r.GET("/v1/sandboxes/:id", s.getSandbox)
	r.GET("/v1/sandboxes/:id/containers/:name/logs", s.getContainerLogs)
	r.POST("/v1/sandboxes", s.createSandbox)
	r.DELETE("/v1/sandboxes/:id", s.deleteSandbox)
	r.POST("/v1/reconcile", s.reconcile)

	return r
}
