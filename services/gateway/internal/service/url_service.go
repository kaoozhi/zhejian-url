package service

import (
	"context"
	"errors"
	"strconv"
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
	repo             *repository.CachedURLRepository
	baseURL          string
	shortCodeLen     int
	shortCodeRetries int
}

// URLServiceInterface defines the contract for URL shortening operations
type URLServiceInterface interface {
	CreateShortURL(ctx context.Context, req *model.CreateURLRequest) (*model.CreateURLResponse, error)
	GetURL(ctx context.Context, code string) (*model.URLResponse, error)
	DeleteURL(ctx context.Context, code string) error
	Redirect(ctx context.Context, code string) (string, error)
}

// NewURLService creates a new URL service
func NewURLService(repo *repository.CachedURLRepository, baseURL string, shortCodeLen int, shortCodeRetries int) *URLService {
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

	var expiresAt *time.Time
	if req.ExpiresIn > 0 {
		t := time.Now().AddDate(0, 0, req.ExpiresIn)
		expiresAt = &t
	}

	if req.CustomAlias != "" {
		url := &model.URL{
			ID:          uuid.New(),
			ShortCode:   req.CustomAlias,
			OriginalURL: req.URL,
			CreatedAt:   time.Now(),
			ExpiresAt:   expiresAt,
			ClickCount:  0,
		}
		if err := s.repo.Create(ctx, url); err != nil {
			if errors.Is(err, repository.ErrCodeConflict) {
				return nil, ErrCodeExists
			}
			return nil, err
		}
		shortCode = url.ShortCode
	} else {
		g := NewShortCodeGenerator(s.shortCodeLen, s.shortCodeRetries, s.repo)
		created := false
		for attemp := 0; attemp < s.shortCodeRetries; attemp++ {
			candidate, genErr := g.Generate(req.URL + strconv.Itoa(attemp))
			if genErr != nil {
				return nil, genErr
			}
			url := &model.URL{
				ID:          uuid.New(),
				ShortCode:   candidate,
				OriginalURL: req.URL,
				CreatedAt:   time.Now(),
				ExpiresAt:   expiresAt,
				ClickCount:  0,
			}
			if err = s.repo.Create(ctx, url); err != nil {
				if errors.Is(err, repository.ErrCodeConflict) {
					continue
				}
				return nil, err
			}
			shortCode = candidate
			created = true
			break
		}
		if !created {
			return nil, ErrShortCodeGeneration
		}
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

// Helper methods such as short-code generation, URL validation and
// alias validation can be added here. The current service uses the
// `ShortCodeGenerator` for producing codes and relies on repository
// uniqueness checks to detect collisions.

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

// Ensure URLService implements URLServiceInterface at compile time
var _ URLServiceInterface = (*URLService)(nil)
