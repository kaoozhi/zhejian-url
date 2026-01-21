package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zhejian/url-shortener/gateway/internal/model"
	"github.com/zhejian/url-shortener/gateway/internal/service"
)

// Handler holds HTTP handlers and dependencies.
// It follows the dependency injection pattern, receiving
// interfaces rather than concrete implementations for testability.
type Handler struct {
	urlService service.URLServiceInterface // URL shortening business logic
	db         DBInterface                 // Database connection for health checks
}

// DBInterface defines the database operations needed by the handler.
// This interface allows for easy mocking in unit tests without
// requiring a real database connection.
type DBInterface interface {
	Ping(ctx context.Context) error // Check database connectivity
	Close()                         // Close database connection
}

// NewHandler creates a new handler instance with the provided dependencies.
// It accepts interfaces to enable dependency injection and facilitate testing.
func NewHandler(urlService service.URLServiceInterface, db DBInterface) *Handler {
	return &Handler{
		urlService: urlService,
		db:         db,
	}
}

// SetupRouter configures and returns the Gin router with all route definitions.
// Routes are organized into:
//   - Health check endpoint for monitoring
//   - API v1 endpoints for URL management (grouped under /api/v1)
//   - Public redirect endpoint for short URL resolution
func (h *Handler) SetupRouter() *gin.Engine {
	r := gin.Default()

	// Health check endpoint
	r.GET("/health", h.healthCheck)

	// API v1 routes - grouped for versioning
	v1 := r.Group("/api/v1")
	{
		v1.POST("/shorten", h.createShortURL) // Create short URL
		v1.GET("/urls/:code", h.getURL)       // Get URL metadata
		v1.DELETE("/urls/:code", h.deleteURL) // Delete URL
	}

	// Redirect route (public) - must be last to avoid conflicts
	r.GET("/:code", h.redirect)

	return r
}

// healthCheck handles GET /health
// Returns the health status of the service and its dependencies.
// Response codes:
//   - 200 OK: Service and database are healthy
//   - 503 Service Unavailable: Database is unreachable
func (h *Handler) healthCheck(c *gin.Context) {
	ctx := c.Request.Context()

	// Check database connectivity
	if err := h.db.Ping(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":  "degraded",
			"message": "database unavailable",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// createShortURL handles POST /api/v1/shorten
// Creates a new short URL from the provided original URL.
// Request body: CreateURLRequest (JSON)
// Response codes:
//   - 201 Created: Short URL successfully created
//   - 400 Bad Request: Invalid request body, URL, or custom alias
//   - 409 Conflict: Custom alias already exists
//   - 500 Internal Server Error: Unexpected error
func (h *Handler) createShortURL(c *gin.Context) {
	ctx := c.Request.Context()
	var req model.CreateURLRequest

	// Bind and validate JSON request body
	if err := c.ShouldBindJSON(&req); err != nil {
		h.errorResponse(c, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Call service layer to create short URL
	resp, err := h.urlService.CreateShortURL(ctx, &req)
	if err != nil {
		// Map service errors to appropriate HTTP status codes
		switch {
		case errors.Is(err, service.ErrInvalidURL):
			h.errorResponse(c, http.StatusBadRequest, "Invalid URL")
		case errors.Is(err, service.ErrCodeExists):
			h.errorResponse(c, http.StatusConflict, "Custom alias already exists")
		case errors.Is(err, service.ErrInvalidAlias):
			h.errorResponse(c, http.StatusBadRequest, "Invalid custom alias")
		default:
			h.errorResponse(c, http.StatusInternalServerError, "Internal server error")
		}
		return
	}

	// Return created short URL
	c.JSON(http.StatusCreated, resp)
}

// getURL handles GET /api/v1/urls/:code
// Retrieves metadata for a short URL without incrementing click count.
// Path parameter: code - the short code to look up
// Response codes:
//   - 200 OK: URL metadata retrieved successfully
//   - 404 Not Found: Short code does not exist
//   - 410 Gone: URL has expired
//   - 500 Internal Server Error: Unexpected error
func (h *Handler) getURL(c *gin.Context) {
	ctx := c.Request.Context()

	// Extract short code from URL path parameter
	code := c.Param("code")

	// Retrieve URL metadata from service layer
	resp, err := h.urlService.GetURL(ctx, code)
	if err != nil {
		// Map service errors to appropriate HTTP status codes
		switch {
		case errors.Is(err, service.ErrURLNotFound):
			h.errorResponse(c, http.StatusNotFound, "URL not found")
		case errors.Is(err, service.ErrURLExpired):
			h.errorResponse(c, http.StatusGone, "URL has expired")
		default:
			h.errorResponse(c, http.StatusInternalServerError, "Internal server error")
		}
		return
	}

	c.JSON(http.StatusOK, resp)
}

// deleteURL handles DELETE /api/v1/urls/:code
// Permanently deletes a short URL.
// Path parameter: code - the short code to delete
// Response codes:
//   - 204 No Content: URL successfully deleted
//   - 404 Not Found: Short code does not exist
//   - 500 Internal Server Error: Unexpected error
func (h *Handler) deleteURL(c *gin.Context) {
	ctx := c.Request.Context()

	// Extract short code from URL path parameter
	code := c.Param("code")

	// Delete URL via service layer
	err := h.urlService.DeleteURL(ctx, code)
	if err != nil {
		// Map service errors to appropriate HTTP status codes
		switch {
		case errors.Is(err, service.ErrURLNotFound):
			h.errorResponse(c, http.StatusNotFound, "URL not found")
		default:
			h.errorResponse(c, http.StatusInternalServerError, "Internal server error")
		}
		return
	}

	// Return 204 No Content on successful deletion
	c.Status(http.StatusNoContent)
}

// redirect handles GET /:code
// Redirects the user to the original URL associated with the short code.
// Also increments the click count for analytics.
// Path parameter: code - the short code to resolve
// Response codes:
//   - 301 Moved Permanently: Redirects to original URL
//   - 404 Not Found: Short code does not exist
//   - 410 Gone: URL has expired
//   - 500 Internal Server Error: Unexpected error
func (h *Handler) redirect(c *gin.Context) {
	ctx := c.Request.Context()

	// Extract short code from URL path parameter
	code := c.Param("code")

	// Resolve short code to original URL (also increments click count)
	url, err := h.urlService.Redirect(ctx, code)
	if err != nil {
		// Map service errors to appropriate HTTP status codes
		switch {
		case errors.Is(err, service.ErrURLNotFound):
			h.errorResponse(c, http.StatusNotFound, "URL not found")
		case errors.Is(err, service.ErrURLExpired):
			h.errorResponse(c, http.StatusGone, "URL has expired")
		default:
			h.errorResponse(c, http.StatusInternalServerError, "Internal server error")
		}
		return
	}

	// Perform HTTP 301 redirect to original URL
	c.Redirect(http.StatusMovedPermanently, url)
}

// errorResponse sends a standardized JSON error response.
// It uses the HTTP status code to determine the error type
// and includes a custom message for additional context.
func (h *Handler) errorResponse(c *gin.Context, status int, message string) {
	c.JSON(status, model.ErrorResponse{
		Error:   http.StatusText(status), // e.g., "Bad Request", "Not Found"
		Message: message,                 // Custom error message
	})
}
