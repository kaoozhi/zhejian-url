package service

import (
	"context"
	"errors"
	"log/slog"
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
	logger           *slog.Logger
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
func NewURLService(repo *repository.CachedURLRepository,
	logger *slog.Logger,
	baseURL string,
	shortCodeLen int,
	shortCodeRetries int,
) *URLService {
	return &URLService{
		repo:             repo,
		logger:           logger,
		baseURL:          baseURL,
		shortCodeLen:     shortCodeLen,
		shortCodeRetries: shortCodeRetries,
	}
}

// CreateShortURL creates a new shortened URL
func (s *URLService) CreateShortURL(ctx context.Context, req *model.CreateURLRequest) (*model.CreateURLResponse, error) {
	// Log incoming request
	s.logger.InfoContext(ctx, "creating short URL",
		slog.String("url", req.URL),
		slog.String("custom_alias", req.CustomAlias),
		slog.Int("expires_in_days", req.ExpiresIn))

	var shortCode string
	var err error

	var expiresAt *time.Time
	if req.ExpiresIn > 0 {
		t := time.Now().AddDate(0, 0, req.ExpiresIn)
		expiresAt = &t
	}

	if req.CustomAlias != "" {
		s.logger.InfoContext(ctx, "using custom alias",
			slog.String("alias", req.CustomAlias))

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
				s.logger.WarnContext(ctx, "custom alias already exists",
					slog.String("alias", req.CustomAlias))
				return nil, ErrCodeExists
			}
			s.logger.ErrorContext(ctx, "failed to create URL with custom alias",
				slog.String("error", err.Error()),
				slog.String("alias", req.CustomAlias))
			return nil, err
		}
		shortCode = url.ShortCode
	} else {
		s.logger.InfoContext(ctx, "generating short code",
			slog.Int("max_retries", s.shortCodeRetries))

		g := NewShortCodeGenerator(s.shortCodeLen, s.shortCodeRetries, s.repo)
		created := false
		for attemp := 0; attemp < s.shortCodeRetries; attemp++ {
			candidate, genErr := g.Generate(req.URL + strconv.Itoa(attemp))
			if genErr != nil {
				s.logger.ErrorContext(ctx, "short code generation failed",
					slog.String("error", genErr.Error()),
					slog.Int("attempt", attemp+1))
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
					s.logger.WarnContext(ctx, "short code collision detected, retrying",
						slog.String("code", candidate),
						slog.Int("attempt", attemp+1),
						slog.Int("max_retries", s.shortCodeRetries))
					continue
				}
				s.logger.ErrorContext(ctx, "failed to create URL",
					slog.String("error", err.Error()),
					slog.String("code", candidate),
					slog.Int("attempt", attemp+1))
				return nil, err
			}
			shortCode = candidate
			created = true
			break
		}
		if !created {
			s.logger.ErrorContext(ctx, "failed to generate unique short code after max retries",
				slog.Int("max_retries", s.shortCodeRetries))
			return nil, ErrShortCodeGeneration
		}
	}

	// Format expiry for response
	var expiresAtStr string
	if expiresAt != nil {
		expiresAtStr = expiresAt.Format(time.RFC3339)
	}

	// Log success
	s.logger.InfoContext(ctx, "short URL created",
		slog.String("short_code", shortCode),
		slog.String("url", req.URL))

	return &model.CreateURLResponse{
		ShortCode: shortCode,
		ShortURL:  s.baseURL + "/" + shortCode,
		ExpiresAt: expiresAtStr,
	}, nil
}

// GetURL retrieves URL metadata by short code
func (s *URLService) GetURL(ctx context.Context, code string) (*model.URLResponse, error) {
	s.logger.DebugContext(ctx, "fetching URL metadata",
		slog.String("code", code))

	url, err := s.getAndValidateURL(ctx, code)
	if err != nil {
		s.logger.WarnContext(ctx, "URL not found or invalid",
			slog.String("code", code),
			slog.String("error", err.Error()))
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
	s.logger.InfoContext(ctx, "redirecting",
		slog.String("code", code))

	url, err := s.getAndValidateURL(ctx, code)
	if err != nil {
		s.logger.WarnContext(ctx, "redirect failed, URL not found or invalid",
			slog.String("code", code),
			slog.String("error", err.Error()))
		return "", err
	}

	s.logger.InfoContext(ctx, "redirect successful",
		slog.String("code", code),
		slog.String("target_url", url.OriginalURL))

	return url.OriginalURL, nil
}

// DeleteURL removes a shortened URL
func (s *URLService) DeleteURL(ctx context.Context, code string) error {
	s.logger.InfoContext(ctx, "deleting URL",
		slog.String("code", code))

	if err := s.repo.Delete(ctx, code); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			s.logger.WarnContext(ctx, "URL not found for deletion",
				slog.String("code", code))
			return ErrURLNotFound
		}
		s.logger.ErrorContext(ctx, "failed to delete URL",
			slog.String("code", code),
			slog.String("error", err.Error()))
		return err
	}

	s.logger.InfoContext(ctx, "URL deleted successfully",
		slog.String("code", code))

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
