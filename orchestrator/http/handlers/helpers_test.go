package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHelperResponders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	r.GET("/node-404", func(c *gin.Context) { respondNodeErr(c, sql.ErrNoRows) })
	r.GET("/node-500", func(c *gin.Context) { respondNodeErr(c, errors.New("x")) })
	r.GET("/proxy-ok", func(c *gin.Context) { respondProxy(c, map[string]any{"ok": true}, nil) })
	r.GET("/proxy-bad", func(c *gin.Context) { respondProxy(c, nil, errors.New("upstream")) })

	cases := []struct {
		path string
		code int
	}{
		{"/node-404", http.StatusNotFound},
		{"/node-500", http.StatusInternalServerError},
		{"/proxy-ok", http.StatusOK},
		{"/proxy-bad", http.StatusBadGateway},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		r.ServeHTTP(w, req)
		if w.Code != tc.code {
			t.Fatalf("%s code=%d want=%d", tc.path, w.Code, tc.code)
		}
	}

	if errString(nil) != "" {
		t.Fatal("expected empty string")
	}

	if errString(errors.New("e")) == "" {
		t.Fatal("expected non-empty string")
	}
}
