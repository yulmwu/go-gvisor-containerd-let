package httplog

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"sandboxd-o/pkg/logging"

	"github.com/gin-gonic/gin"
)

func TestRequestLoggerAndRecovery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lg, _ := logging.New(logging.Config{}, logging.Options{Service: "test"})
	r := gin.New()
	r.Use(RecoveryLogger(lg), RequestLogger(lg))
	r.GET("/ok", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	r.GET("/panic", func(c *gin.Context) { panic("x") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ok?q=1", nil)
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("ok code=%d", w.Code)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/panic", nil)
	r.ServeHTTP(w, req)
	if w.Code != 500 {
		t.Fatalf("panic code=%d", w.Code)
	}
}
