package httplog

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"sandboxd-o/pkg/logging"

	"github.com/gin-gonic/gin"
)

func RequestLogger(logger *logging.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		start := time.Now().UTC()
		ctx.Next()

		status := ctx.Writer.Status()
		latency := time.Since(start)
		attrs := []slog.Attr{
			slog.String("method", ctx.Request.Method),
			slog.String("path", ctx.Request.URL.Path),
			slog.Int("status", status),
			slog.Duration("latency", latency),
			slog.String("ip", ctx.ClientIP()),
		}

		if q := ctx.Request.URL.RawQuery; q != "" {
			attrs = append(attrs, slog.String("query", q))
		}
		if ua := ctx.Request.UserAgent(); ua != "" {
			attrs = append(attrs, slog.String("user_agent", ua))
		}

		anyAttrs := make([]any, 0, len(attrs))
		for _, a := range attrs {
			anyAttrs = append(anyAttrs, a)
		}
		logger.Info("http request", slog.Group("http", anyAttrs...))
	}
}

func RecoveryLogger(logger *logging.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("panic recovered",
					slog.Any("error", recovered),
					slog.String("path", ctx.Request.URL.Path),
					slog.String("method", ctx.Request.Method),
					slog.String("stack", string(debug.Stack())),
				)
				ctx.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		ctx.Next()
	}
}
