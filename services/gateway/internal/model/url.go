package model

import "time"

// URL represents a shortened URL entity
type URL struct {
	ID          int64      `json:"id"`
	ShortCode   string     `json:"short_code"`
	OriginalURL string     `json:"original_url"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	ClickCount  int64      `json:"click_count"`
}

// CreateURLRequest represents the request body for creating a short URL
type CreateURLRequest struct {
	URL         string `json:"url" binding:"required,url"`
	CustomAlias string `json:"custom_alias,omitempty"`
	ExpiresIn   int    `json:"expires_in,omitempty"` // Duration in seconds
}

// CreateURLResponse represents the response for a created short URL
type CreateURLResponse struct {
	ShortCode string `json:"short_code"`
	ShortURL  string `json:"short_url"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// URLResponse represents the full URL metadata response
type URLResponse struct {
	ShortCode   string `json:"short_code"`
	OriginalURL string `json:"original_url"`
	ShortURL    string `json:"short_url"`
	CreatedAt   string `json:"created_at"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	ClickCount  int64  `json:"click_count"`
}

// ErrorResponse represents an API error response
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}
