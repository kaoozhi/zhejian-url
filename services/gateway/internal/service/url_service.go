package service

import (
	"context"
	"errors"

	"github.com/zhejian/url-shortener/gateway/internal/model"
	"github.com/zhejian/url-shortener/gateway/internal/repository"
)

var (
	ErrInvalidURL   = errors.New("invalid URL format")
	ErrURLNotFound  = errors.New("URL not found")
	ErrURLExpired   = errors.New("URL has expired")
	ErrCodeExists   = errors.New("custom alias already exists")
	ErrInvalidAlias = errors.New("invalid custom alias format")
)

// URLService handles business logic for URL operations
type URLService struct {
	repo    *repository.URLRepository
	baseURL string
}

// NewURLService creates a new URL service
func NewURLService(repo *repository.URLRepository, baseURL string) *URLService {
	return &URLService{
		repo:    repo,
		baseURL: baseURL,
	}
}

// CreateShortURL creates a new shortened URL
func (s *URLService) CreateShortURL(ctx context.Context, req *model.CreateURLRequest) (*model.CreateURLResponse, error) {
	// TODO: Implement URL creation logic
	// 1. Validate the original URL format
	// 2. If custom alias provided:
	//    - Validate alias format (alphanumeric, length limits)
	//    - Check if alias already exists
	// 3. If no custom alias:
	//    - Generate short code using Base62 encoding
	//    - Check for collisions, regenerate if needed
	// 4. Calculate expiration time if expires_in provided
	// 5. Create URL record in database
	// 6. Return response with short URL
	return nil, nil
}

// GetURL retrieves URL metadata by short code
func (s *URLService) GetURL(ctx context.Context, code string) (*model.URLResponse, error) {
	// TODO: Implement URL retrieval logic
	// 1. Fetch URL from repository
	// 2. Check if URL has expired
	// 3. Return URL metadata (don't increment click count)
	return nil, nil
}

// Redirect retrieves the original URL for redirection
func (s *URLService) Redirect(ctx context.Context, code string) (string, error) {
	// TODO: Implement redirect logic
	// 1. Fetch URL from repository
	// 2. Check if URL has expired
	// 3. Increment click count (async or sync)
	// 4. Return original URL
	return "", nil
}

// DeleteURL removes a shortened URL
func (s *URLService) DeleteURL(ctx context.Context, code string) error {
	// TODO: Implement URL deletion logic
	// 1. Delete URL from repository
	// 2. Handle not found error
	return nil
}

// generateShortCode generates a unique short code
func (s *URLService) generateShortCode(ctx context.Context) (string, error) {
	// TODO: Implement short code generation
	// 1. Generate random bytes or use counter-based approach
	// 2. Encode using Base62 (a-z, A-Z, 0-9)
	// 3. Check for collision in database
	// 4. Retry with new code if collision detected
	// 5. Return unique code
	return "", nil
}

// validateURL checks if the URL is valid
func (s *URLService) validateURL(rawURL string) error {
	// TODO: Implement URL validation
	// 1. Parse URL
	// 2. Check scheme (http/https)
	// 3. Check host is present
	// 4. Optionally: check URL is reachable
	return nil
}

// validateAlias checks if the custom alias is valid
func (s *URLService) validateAlias(alias string) error {
	// TODO: Implement alias validation
	// 1. Check length (min/max)
	// 2. Check characters (alphanumeric, hyphen, underscore)
	// 3. Check for reserved words
	return nil
}
