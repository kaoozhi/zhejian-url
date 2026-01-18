package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zhejian/url-shortener/gateway/internal/model"
	"github.com/zhejian/url-shortener/gateway/internal/service"
)

// Handler holds HTTP handlers and dependencies
type Handler struct {
	urlService *service.URLService
}

// NewHandler creates a new handler instance
func NewHandler(urlService *service.URLService) *Handler {
	return &Handler{urlService: urlService}
}

// SetupRouter configures and returns the Gin router
func (h *Handler) SetupRouter() *gin.Engine {
	r := gin.Default()

	// Health check
	r.GET("/health", h.healthCheck)

	// API v1 routes
	v1 := r.Group("/api/v1")
	{
		v1.POST("/shorten", h.createShortURL)
		v1.GET("/urls/:code", h.getURL)
		v1.DELETE("/urls/:code", h.deleteURL)
	}

	// Redirect route (public)
	r.GET("/:code", h.redirect)

	return r
}

// healthCheck handles GET /health
func (h *Handler) healthCheck(c *gin.Context) {
	// TODO: Implement health check
	// - Check database connectivity
	// - Return status "ok" or "degraded"
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// createShortURL handles POST /api/v1/shorten
func (h *Handler) createShortURL(c *gin.Context) {
	var req model.CreateURLRequest

	// TODO: Implement URL creation handler
	// 1. Bind and validate JSON request body
	//    if err := c.ShouldBindJSON(&req); err != nil { ... }
	// 2. Call urlService.CreateShortURL
	// 3. Handle errors:
	//    - ErrInvalidURL -> 400 Bad Request
	//    - ErrCodeExists -> 409 Conflict
	//    - ErrInvalidAlias -> 400 Bad Request
	//    - Other errors -> 500 Internal Server Error
	// 4. Return 201 Created with response body

	_ = req // Remove after implementation
	c.JSON(http.StatusNotImplemented, gin.H{"error": "not implemented"})
}

// getURL handles GET /api/v1/urls/:code
func (h *Handler) getURL(c *gin.Context) {
	// TODO: Implement get URL handler
	// 1. Get code from URL parameter: c.Param("code")
	// 2. Call urlService.GetURL
	// 3. Handle errors:
	//    - ErrURLNotFound -> 404 Not Found
	//    - ErrURLExpired -> 410 Gone
	//    - Other errors -> 500 Internal Server Error
	// 4. Return 200 OK with URL metadata

	c.JSON(http.StatusNotImplemented, gin.H{"error": "not implemented"})
}

// deleteURL handles DELETE /api/v1/urls/:code
func (h *Handler) deleteURL(c *gin.Context) {
	// TODO: Implement delete URL handler
	// 1. Get code from URL parameter: c.Param("code")
	// 2. Call urlService.DeleteURL
	// 3. Handle errors:
	//    - ErrURLNotFound -> 404 Not Found
	//    - Other errors -> 500 Internal Server Error
	// 4. Return 204 No Content on success

	c.JSON(http.StatusNotImplemented, gin.H{"error": "not implemented"})
}

// redirect handles GET /:code
func (h *Handler) redirect(c *gin.Context) {
	// TODO: Implement redirect handler
	// 1. Get code from URL parameter: c.Param("code")
	// 2. Call urlService.Redirect
	// 3. Handle errors:
	//    - ErrURLNotFound -> 404 Not Found
	//    - ErrURLExpired -> 410 Gone
	//    - Other errors -> 500 Internal Server Error
	// 4. Redirect to original URL: c.Redirect(http.StatusMovedPermanently, originalURL)

	c.JSON(http.StatusNotImplemented, gin.H{"error": "not implemented"})
}

// errorResponse sends a JSON error response
func (h *Handler) errorResponse(c *gin.Context, status int, message string) {
	c.JSON(status, model.ErrorResponse{
		Error:   http.StatusText(status),
		Message: message,
	})
}
