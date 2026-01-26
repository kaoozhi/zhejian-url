package server

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/zhejian/url-shortener/gateway/internal/api"
	"github.com/zhejian/url-shortener/gateway/internal/config"
	"github.com/zhejian/url-shortener/gateway/internal/repository"
	"github.com/zhejian/url-shortener/gateway/internal/service"
)

// NewRouter initializes all dependencies and returns a configured Gin router.
// This is useful for testing where you don't need the full HTTP server.
func NewRouter(cfg *config.Config, db *pgxpool.Pool, cache *redis.Client) *gin.Engine {
	baseRepo := repository.NewURLRepository(db)
	urlRepo := repository.NewCachedURLRepository(baseRepo, cache, cfg.Cache.TTL)
	urlService := service.NewURLService(urlRepo, cfg.App.BaseURL, cfg.App.ShortCodeLen, cfg.App.ShortCodeRetries)
	handler := api.NewHandler(urlService, db)
	return handler.SetupRouter()
}

// NewServer initializes all dependencies and returns a configured HTTP server.
// This includes the router plus HTTP server settings (timeouts, address, etc.).
func NewServer(cfg *config.Config, db *pgxpool.Pool, cache *redis.Client) *http.Server {
	router := NewRouter(cfg, db, cache)

	return &http.Server{
		Addr:         ":" + cfg.Server.Port,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}
