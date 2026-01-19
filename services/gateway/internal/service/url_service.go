package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/zhejian/url-shortener/gateway/internal/model"
	"github.com/zhejian/url-shortener/gateway/internal/repository"
)

var (
	ErrInvalidURL          = errors.New("invalid URL format")
	ErrURLNotFound         = errors.New("URL not found")
	ErrURLExpired          = errors.New("URL has expired")
	ErrCodeExists          = errors.New("custom alias already exists")
	ErrInvalidAlias        = errors.New("invalid custom alias format")
	ErrShortCodeGeneration = errors.New("failed to generate short URL")
)

// URLService handles business logic for URL operations
type URLService struct {
	repo             *repository.URLRepository
	baseURL          string
	shortCodeLen     int
	shortCodeRetries int
}

// NewURLService creates a new URL service
func NewURLService(repo *repository.URLRepository, baseURL string, shortCodeLen int, shortCodeRetries int) *URLService {
	return &URLService{
		repo:             repo,
		baseURL:          baseURL,
		shortCodeLen:     shortCodeLen,
		shortCodeRetries: shortCodeRetries,
	}
}

// CreateShortURL creates a new shortened URL
func (s *URLService) CreateShortURL(ctx context.Context, req *model.CreateURLRequest) (*model.CreateURLResponse, error) {
	var shortCode string
	var err error

	if req.CustomAlias != "" {
		// Use custom alias
		// TODO: validate alias and check exists
		shortCode = req.CustomAlias
	} else {
		g := NewShortCodeGenerator(s.shortCodeLen, s.shortCodeRetries, s.repo)
		shortCode, err = g.Generate(req.URL)
		if err != nil {
			return nil, err
		}
	}

	var expiresAt *time.Time
	if req.ExpiresIn > 0 {
		t := time.Now().AddDate(0, 0, req.ExpiresIn)
		expiresAt = &t
	}

	url := &model.URL{
		ID:          uuid.New(),
		ShortCode:   shortCode,
		OriginalURL: req.URL,
		CreatedAt:   time.Now(),
		ExpiresAt:   expiresAt,
		ClickCount:  0,
	}

	err = s.repo.Create(ctx, url)
	if err != nil {
		return nil, err
	}

	// Format expiry for response
	var expiresAtStr string
	if expiresAt != nil {
		expiresAtStr = expiresAt.Format(time.RFC3339)
	}

	return &model.CreateURLResponse{
		ShortCode: shortCode,
		ShortURL:  s.baseURL + "/" + shortCode,
		ExpiresAt: expiresAtStr,
	}, nil
}

// GetURL retrieves URL metadata by short code
func (s *URLService) GetURL(ctx context.Context, code string) (*model.URLResponse, error) {
	url, err := s.getAndValidateURL(ctx, code)
	if err != nil {
		return nil, err
	}

	var expiresAtStr string
	if url.ExpiresAt != nil {
		expiresAtStr = url.ExpiresAt.Format(time.RFC3339)
	}

	return &model.URLResponse{
		ShortCode:   url.ShortCode,
		OriginalURL: url.OriginalURL,
		ShortURL:    s.baseURL + "/" + url.ShortCode,
		CreatedAt:   url.CreatedAt.Format(time.RFC3339),
		ExpiresAt:   expiresAtStr,
		ClickCount:  url.ClickCount,
	}, nil
}

// Redirect retrieves the original URL for redirection
func (s *URLService) Redirect(ctx context.Context, code string) (string, error) {
	url, err := s.getAndValidateURL(ctx, code)
	if err != nil {
		return "", err
	}

	return url.OriginalURL, nil
}

// DeleteURL removes a shortened URL
func (s *URLService) DeleteURL(ctx context.Context, code string) error {
	if err := s.repo.Delete(ctx, code); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return ErrURLNotFound
		}
		return err
	}
	return nil
}

// // generateShortCode generates a unique short code
// func (s *URLService) generateShortCode(ctx context.Context) (string, error) {
// 	// TODO: Implement short code generation
// 	// 1. Generate random bytes or use counter-based approach
// 	// 2. Encode using Base62 (a-z, A-Z, 0-9)
// 	// 3. Check for collision in database
// 	// 4. Retry with new code if collision detected
// 	// 5. Return unique code
// 	return "", nil
// }

// // validateURL checks if the URL is valid
// func (s *URLService) validateURL(rawURL string) error {
// 	// TODO: Implement URL validation
// 	// 1. Parse URL
// 	// 2. Check scheme (http/https)
// 	// 3. Check host is present
// 	// 4. Optionally: check URL is reachable
// 	return nil
// }

// // validateAlias checks if the custom alias is valid
// func (s *URLService) validateAlias(alias string) error {
// 	// TODO: Implement alias validation
// 	// 1. Check length (min/max)
// 	// 2. Check characters (alphanumeric, hyphen, underscore)
// 	// 3. Check for reserved words
// 	return nil
// }

// getAndValidateURL is a helper that fetches URL and checks expiration
func (s *URLService) getAndValidateURL(ctx context.Context, code string) (*model.URL, error) {
	// 1. Fetch URL from repository
	url, err := s.repo.GetByCode(ctx, code)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrURLNotFound
		}
		return nil, err
	}

	// 2. Check if URL has expired
	if url.ExpiresAt != nil && url.ExpiresAt.Before(time.Now()) {
		return nil, ErrURLExpired
	}

	return url, nil
}
