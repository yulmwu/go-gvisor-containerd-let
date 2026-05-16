package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRespondJSONAndError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/ok", func(c *gin.Context) {
		respondJSON(c, http.StatusOK, gin.H{"ok": true}, "1.2.3.4")
	})

	r.GET("/err", func(c *gin.Context) {
		respondErrorMessage(c, 0, "x")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	r.ServeHTTP(w, req)
	if w.Code != 200 || w.Body.String() == "" {
		t.Fatalf("ok code=%d body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/err", nil)
	r.ServeHTTP(w, req)
	if w.Code != 500 {
		t.Fatalf("err code=%d", w.Code)
	}
}
