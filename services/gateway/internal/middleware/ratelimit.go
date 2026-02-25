package middleware

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zhejian/url-shortener/gateway/internal/ratelimit"
)

func RateLimit(client *ratelimit.Client, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		allowed, remaining, retryAfterMs, err := client.Check(c.Request.Context(), ip)

		if err != nil {
			logger.WarnContext(c.Request.Context(), "rate limiter unavailable, failing open",
				slog.String("error", err.Error()),
				slog.String("ip", ip))
			c.Next()
			return
		}

		if !allowed {
			c.Header("X-RateLimit-Remaining", "0")
			c.Header("Retry-After", fmt.Sprintf("%d", retryAfterMs/1000))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":   "Too Many Requests",
				"message": "rate limit exceeded",
			})
			return
		}

		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		c.Next()
	}
}
