package httpserver

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func respondJSON(c *gin.Context, code int, payload gin.H, externalIP string) {
	if externalIP != "" {
		payload["external_ip"] = externalIP
	}

	c.JSON(code, payload)
}

func respondError(c *gin.Context, code int, err error) {
	respondErrorMessage(c, code, err.Error())
}

func respondErrorMessage(c *gin.Context, code int, msg string) {
	if code == 0 {
		code = http.StatusInternalServerError
	}

	c.JSON(code, gin.H{"error": msg})
}
