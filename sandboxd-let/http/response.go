package httpserver

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func respondError(c *gin.Context, code int, err error) {
	if err == nil {
		respondErrorMessage(c, code, http.StatusText(code))
		return
	}

	respondErrorMessage(c, code, err.Error())
}

func respondErrorMessage(c *gin.Context, code int, msg string) {
	c.JSON(code, ErrorResponse{Error: msg})
}
