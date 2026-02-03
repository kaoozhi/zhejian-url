package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/zhejian/url-shortener/gateway/internal/api"
	"github.com/zhejian/url-shortener/gateway/internal/model"
	"github.com/zhejian/url-shortener/gateway/internal/service"
)

// MockURLService mocks the service layer
type MockURLService struct {
	mock.Mock
}

func (m *MockURLService) CreateShortURL(ctx context.Context, req *model.CreateURLRequest) (*model.CreateURLResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.CreateURLResponse), args.Error(1)
}

func (m *MockURLService) GetURL(ctx context.Context, code string) (*model.URLResponse, error) {
	args := m.Called(ctx, code)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.URLResponse), args.Error(1)
}

func (m *MockURLService) DeleteURL(ctx context.Context, code string) error {
	args := m.Called(ctx, code)
	return args.Error(0)
}

func (m *MockURLService) Redirect(ctx context.Context, code string) (string, error) {
	args := m.Called(ctx, code)
	return args.String(0), args.Error(1)
}

// MockDB for health check
type MockDB struct {
	shouldFail bool
}

func (m *MockDB) Ping(ctx context.Context) error {
	if m.shouldFail {
		return assert.AnError
	}
	return nil
}

func (m *MockDB) Close() {}

// MockCache for health check
type MockCache struct {
	shouldFail bool
}

func (m *MockCache) Ping(ctx context.Context) error {
	if m.shouldFail {
		return assert.AnError
	}
	return nil
}

func TestHandler_HealthCheck(t *testing.T) {
	t.Run("returns ok when all dependencies are healthy", func(t *testing.T) {
		mockService := new(MockURLService)
		mockDB := &MockDB{shouldFail: false}
		mockCache := &MockCache{shouldFail: false}
		handler := api.NewHandler(mockService, mockDB, mockCache)
		router := handler.SetupRouter()

		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]interface{}
		json.NewDecoder(w.Body).Decode(&response)
		assert.Equal(t, "ok", response["status"])
		deps := response["dependencies"].(map[string]interface{})
		assert.Equal(t, "up", deps["cache"])
		assert.Equal(t, "up", deps["database"])
	})

	t.Run("returns degraded when cache is down", func(t *testing.T) {
		mockService := new(MockURLService)
		mockDB := &MockDB{shouldFail: false}
		mockCache := &MockCache{shouldFail: true}
		handler := api.NewHandler(mockService, mockDB, mockCache)
		router := handler.SetupRouter()

		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
		var response map[string]interface{}
		json.NewDecoder(w.Body).Decode(&response)
		assert.Equal(t, "degraded", response["status"])
		deps := response["dependencies"].(map[string]interface{})
		assert.Equal(t, "down", deps["cache"])
		assert.Equal(t, "up", deps["database"])
	})

	t.Run("returns degraded when database is down", func(t *testing.T) {
		mockService := new(MockURLService)
		mockDB := &MockDB{shouldFail: true}
		mockCache := &MockCache{shouldFail: false}
		handler := api.NewHandler(mockService, mockDB, mockCache)
		router := handler.SetupRouter()

		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
		var response map[string]interface{}
		json.NewDecoder(w.Body).Decode(&response)
		assert.Equal(t, "degraded", response["status"])
		deps := response["dependencies"].(map[string]interface{})
		assert.Equal(t, "up", deps["cache"])
		assert.Equal(t, "down", deps["database"])
	})

	t.Run("returns degraded when both dependencies are down", func(t *testing.T) {
		mockService := new(MockURLService)
		mockDB := &MockDB{shouldFail: true}
		mockCache := &MockCache{shouldFail: true}
		handler := api.NewHandler(mockService, mockDB, mockCache)
		router := handler.SetupRouter()

		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
		var response map[string]interface{}
		json.NewDecoder(w.Body).Decode(&response)
		assert.Equal(t, "degraded", response["status"])
		deps := response["dependencies"].(map[string]interface{})
		assert.Equal(t, "down", deps["cache"])
		assert.Equal(t, "down", deps["database"])
	})
}

func TestHandler_CreateShortURL(t *testing.T) {
	t.Run("returns 201 when URL is successfully created", func(t *testing.T) {
		mockService := new(MockURLService)
		mockDB := &MockDB{shouldFail: false}
		mockCache := &MockCache{shouldFail: false}

		// Setup mock expectation
		mockService.On("CreateShortURL", mock.Anything, mock.Anything).Return(
			&model.CreateURLResponse{
				ShortCode: "abc123",
				ShortURL:  "http://localhost:8081/abc123",
			},
			nil,
		)

		handler := api.NewHandler(mockService, mockDB, mockCache)
		router := handler.SetupRouter()

		// Create request with JSON body
		reqBody := `{"url": "https://example.com"}`
		req := httptest.NewRequest("POST", "/api/v1/shorten", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		// Verify status code
		assert.Equal(t, http.StatusCreated, w.Code)

		// Verify response body
		var response model.CreateURLResponse
		err := json.NewDecoder(w.Body).Decode(&response)
		assert.NoError(t, err)
		assert.Equal(t, "abc123", response.ShortCode)
		assert.Equal(t, "http://localhost:8081/abc123", response.ShortURL)

		// Verify mock was called
		mockService.AssertExpectations(t)
	})

	t.Run("returns 400 when request body is invalid JSON", func(t *testing.T) {
		mockService := new(MockURLService)
		mockDB := &MockDB{shouldFail: false}
		mockCache := &MockCache{shouldFail: false}

		handler := api.NewHandler(mockService, mockDB, mockCache)
		router := handler.SetupRouter()

		reqBody := `{invalid json}`
		req := httptest.NewRequest("POST", "/api/v1/shorten", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var response model.ErrorResponse
		json.NewDecoder(w.Body).Decode(&response)
		assert.Equal(t, "Bad Request", response.Error)
	})

	t.Run("returns 400 when URL is invalid", func(t *testing.T) {
		mockService := new(MockURLService)
		mockDB := &MockDB{shouldFail: false}
		mockCache := &MockCache{shouldFail: false}

		// Setup mock to return invalid URL error
		mockService.On("CreateShortURL", mock.Anything, mock.Anything).Return(
			nil,
			service.ErrInvalidURL,
		)

		handler := api.NewHandler(mockService, mockDB, mockCache)
		router := handler.SetupRouter()

		// Use a string that looks like URL but is invalid (service will validate)
		reqBody := `{"url": "https://example.com"}`
		req := httptest.NewRequest("POST", "/api/v1/shorten", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var response model.ErrorResponse
		json.NewDecoder(w.Body).Decode(&response)
		assert.Equal(t, "Bad Request", response.Error)
		assert.Equal(t, "Invalid URL", response.Message)

		mockService.AssertExpectations(t)
	})

	t.Run("returns 409 when custom alias already exists", func(t *testing.T) {
		mockService := new(MockURLService)
		mockDB := &MockDB{shouldFail: false}
		mockCache := &MockCache{shouldFail: false}

		// Setup mock to return code exists error
		mockService.On("CreateShortURL", mock.Anything, mock.Anything).Return(
			nil,
			service.ErrCodeExists,
		)

		handler := api.NewHandler(mockService, mockDB, mockCache)
		router := handler.SetupRouter()

		reqBody := `{"url": "https://example.com", "custom_alias": "taken"}`
		req := httptest.NewRequest("POST", "/api/v1/shorten", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusConflict, w.Code)

		var response model.ErrorResponse
		json.NewDecoder(w.Body).Decode(&response)
		assert.Equal(t, "Conflict", response.Error)
		assert.Equal(t, "Custom alias already exists", response.Message)

		mockService.AssertExpectations(t)
	})

	t.Run("returns 400 when custom alias is invalid", func(t *testing.T) {
		mockService := new(MockURLService)
		mockDB := &MockDB{shouldFail: false}
		mockCache := &MockCache{shouldFail: false}

		// Setup mock to return invalid alias error
		mockService.On("CreateShortURL", mock.Anything, mock.Anything).Return(
			nil,
			service.ErrInvalidAlias,
		)

		handler := api.NewHandler(mockService, mockDB, mockCache)
		router := handler.SetupRouter()

		reqBody := `{"url": "https://example.com", "custom_alias": "ab"}`
		req := httptest.NewRequest("POST", "/api/v1/shorten", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var response model.ErrorResponse
		json.NewDecoder(w.Body).Decode(&response)
		assert.Equal(t, "Bad Request", response.Error)
		assert.Equal(t, "Invalid custom alias", response.Message)

		mockService.AssertExpectations(t)
	})
}

func TestHandler_GetURL(t *testing.T) {
	t.Run("returns 200 with URL metadata when code exists", func(t *testing.T) {
		mockService := new(MockURLService)
		mockDB := &MockDB{shouldFail: false}
		mockCache := &MockCache{shouldFail: false}

		// Setup mock expectation
		mockService.On("GetURL", mock.Anything, "abc123").Return(
			&model.URLResponse{
				ShortCode:   "abc123",
				OriginalURL: "https://example.com",
				ShortURL:    "http://localhost:8081/abc123",
				ClickCount:  5,
				CreatedAt:   "2026-01-20T10:00:00Z",
			},
			nil,
		)

		handler := api.NewHandler(mockService, mockDB, mockCache)
		router := handler.SetupRouter()

		req := httptest.NewRequest("GET", "/api/v1/urls/abc123", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response model.URLResponse
		err := json.NewDecoder(w.Body).Decode(&response)
		assert.NoError(t, err)
		assert.Equal(t, "abc123", response.ShortCode)
		assert.Equal(t, "https://example.com", response.OriginalURL)
		assert.Equal(t, int64(5), response.ClickCount)

		mockService.AssertExpectations(t)
	})

	t.Run("returns 404 when URL not found", func(t *testing.T) {
		mockService := new(MockURLService)
		mockDB := &MockDB{shouldFail: false}
		mockCache := &MockCache{shouldFail: false}

		// Setup mock to return not found error
		mockService.On("GetURL", mock.Anything, "notfound").Return(
			nil,
			service.ErrURLNotFound,
		)

		handler := api.NewHandler(mockService, mockDB, mockCache)
		router := handler.SetupRouter()

		req := httptest.NewRequest("GET", "/api/v1/urls/notfound", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)

		var response model.ErrorResponse
		json.NewDecoder(w.Body).Decode(&response)
		assert.Equal(t, "Not Found", response.Error)
		assert.Equal(t, "URL not found", response.Message)

		mockService.AssertExpectations(t)
	})

	t.Run("returns 410 when URL has expired", func(t *testing.T) {
		mockService := new(MockURLService)
		mockDB := &MockDB{shouldFail: false}
		mockCache := &MockCache{shouldFail: false}

		// Setup mock to return expired error
		mockService.On("GetURL", mock.Anything, "expired").Return(
			nil,
			service.ErrURLExpired,
		)

		handler := api.NewHandler(mockService, mockDB, mockCache)
		router := handler.SetupRouter()

		req := httptest.NewRequest("GET", "/api/v1/urls/expired", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusGone, w.Code)

		var response model.ErrorResponse
		json.NewDecoder(w.Body).Decode(&response)
		assert.Equal(t, "Gone", response.Error)
		assert.Equal(t, "URL has expired", response.Message)

		mockService.AssertExpectations(t)
	})
}

func TestHandler_DeleteURL(t *testing.T) {
	t.Run("returns 204 when URL is successfully deleted", func(t *testing.T) {
		mockService := new(MockURLService)
		mockDB := &MockDB{shouldFail: false}
		mockCache := &MockCache{shouldFail: false}

		// Setup mock expectation
		mockService.On("DeleteURL", mock.Anything, "abc123").Return(nil)

		handler := api.NewHandler(mockService, mockDB, mockCache)
		router := handler.SetupRouter()

		req := httptest.NewRequest("DELETE", "/api/v1/urls/abc123", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Empty(t, w.Body.String())

		mockService.AssertExpectations(t)
	})

	t.Run("returns 404 when URL not found", func(t *testing.T) {
		mockService := new(MockURLService)
		mockDB := &MockDB{shouldFail: false}
		mockCache := &MockCache{shouldFail: false}

		// Setup mock to return not found error
		mockService.On("DeleteURL", mock.Anything, "notfound").Return(
			service.ErrURLNotFound,
		)

		handler := api.NewHandler(mockService, mockDB, mockCache)
		router := handler.SetupRouter()

		req := httptest.NewRequest("DELETE", "/api/v1/urls/notfound", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)

		var response model.ErrorResponse
		json.NewDecoder(w.Body).Decode(&response)
		assert.Equal(t, "Not Found", response.Error)
		assert.Equal(t, "URL not found", response.Message)

		mockService.AssertExpectations(t)
	})
}

func TestHandler_Redirect(t *testing.T) {
	t.Run("returns 301 redirect when URL exists", func(t *testing.T) {
		mockService := new(MockURLService)
		mockDB := &MockDB{shouldFail: false}
		mockCache := &MockCache{shouldFail: false}

		// Setup mock expectation
		mockService.On("Redirect", mock.Anything, "abc123").Return(
			"https://example.com",
			nil,
		)

		handler := api.NewHandler(mockService, mockDB, mockCache)
		router := handler.SetupRouter()

		req := httptest.NewRequest("GET", "/abc123", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusMovedPermanently, w.Code)
		assert.Equal(t, "https://example.com", w.Header().Get("Location"))

		mockService.AssertExpectations(t)
	})

	t.Run("returns 404 when URL not found", func(t *testing.T) {
		mockService := new(MockURLService)
		mockDB := &MockDB{shouldFail: false}
		mockCache := &MockCache{shouldFail: false}

		// Setup mock to return not found error
		mockService.On("Redirect", mock.Anything, "notfound").Return(
			"",
			service.ErrURLNotFound,
		)

		handler := api.NewHandler(mockService, mockDB, mockCache)
		router := handler.SetupRouter()

		req := httptest.NewRequest("GET", "/notfound", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)

		var response model.ErrorResponse
		json.NewDecoder(w.Body).Decode(&response)
		assert.Equal(t, "Not Found", response.Error)
		assert.Equal(t, "URL not found", response.Message)

		mockService.AssertExpectations(t)
	})

	t.Run("returns 410 when URL has expired", func(t *testing.T) {
		mockService := new(MockURLService)
		mockDB := &MockDB{shouldFail: false}
		mockCache := &MockCache{shouldFail: false}

		// Setup mock to return expired error
		mockService.On("Redirect", mock.Anything, "expired").Return(
			"",
			service.ErrURLExpired,
		)

		handler := api.NewHandler(mockService, mockDB, mockCache)
		router := handler.SetupRouter()

		req := httptest.NewRequest("GET", "/expired", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusGone, w.Code)

		var response model.ErrorResponse
		json.NewDecoder(w.Body).Decode(&response)
		assert.Equal(t, "Gone", response.Error)
		assert.Equal(t, "URL has expired", response.Message)

		mockService.AssertExpectations(t)
	})
}
