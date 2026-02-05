package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zhejian/url-shortener/gateway/internal/config"
	"github.com/zhejian/url-shortener/gateway/internal/observability"
	"github.com/zhejian/url-shortener/gateway/internal/server"
	"github.com/zhejian/url-shortener/gateway/internal/testutil"
)

var (
	testDB    *testutil.TestDB
	testCache *testutil.TestCache
	testCfg   *config.Config
	testObs   *observability.Observability
)

// TestMain sets up the test environment once for all tests
func TestMain(m *testing.M) {
	ctx := context.Background()

	// Setup test database
	var err error
	testDB, err = testutil.SetupTestDB(ctx)
	if err != nil {
		panic("failed to setup test database: " + err.Error())
	}

	// Setup test cache
	testCache, err = testutil.SetupTestCache(ctx)
	if err != nil {
		panic("failed to setup test cache: " + err.Error())
	}

	// Load test configuration
	testCfg = config.Load()

	// testCfg
	testCfg.Server.Port = "0"

	// test observability
	testObs, err = observability.Setup(ctx, observability.Config{
		ServiceName: "testURLService",
		Environment: "development",
	})

	// Run tests
	code := m.Run()

	// Cleanup
	testCache.Teardown(ctx)
	testDB.Teardown(ctx)
	os.Exit(code)
}

func setupTestServer(t *testing.T) (*http.Server, string) {
	gin.SetMode(gin.TestMode)
	srv := server.NewServer(testCfg, testDB.Pool, testCache.Client, testObs)

	// Create listener on localhost
	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)

	// Get the actual port
	actualAddr := listener.Addr().String()
	baseURL := "http://" + actualAddr

	// Start server in goroutine
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			t.Logf("Server error: %v", err)
		}
	}()
	// Wait for server to be ready
	waitForServer(t, baseURL+"/health", 3*time.Second)

	return srv, baseURL
}

func waitForServer(t *testing.T, url string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			t.Logf("Health check returned %d:", resp.StatusCode)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("Server did not become ready within %v", timeout)
}

// TestHealthCheck verifies the health check endpoint
func TestHealthCheck(t *testing.T) {
	ctx := context.Background()
	testDB.Cleanup(ctx)
	testCache.Cleanup(ctx)
	srv, baseURL := setupTestServer(t)
	defer srv.Shutdown(ctx)

	resp, err := http.Get(baseURL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]any
	json.NewDecoder(resp.Body).Decode(&response)
	assert.Equal(t, "ok", response["status"])
}

// TestCreateShortURL_Success verifies successful URL shortening
func TestCreateShortURL_Success(t *testing.T) {
	ctx := context.Background()
	testDB.Cleanup(ctx)
	testCache.Cleanup(ctx)
	srv, baseURL := setupTestServer(t)
	defer srv.Shutdown(ctx)

	// Create request body with long URL
	reqBody := map[string]string{"url": "https://www.example.com/very/long/url"}
	body, _ := json.Marshal(reqBody)

	// Make POST request to /api/v1/urls
	resp, err := http.Post(baseURL+"/api/v1/shorten", "application/json", bytes.NewBuffer(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	// Assert status code is 201 Created
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	// Parse response and verify short_code and short_url are returned
	var response map[string]any
	json.NewDecoder(resp.Body).Decode(&response)
	assert.NotEmpty(t, response["short_code"])
	// short_url should end with the short code
	shortCode := jsonValueToString(response["short_code"])
	assert.True(t, strings.HasSuffix(jsonValueToString(response["short_url"]), "/"+shortCode))

	// Verify URL was saved in database by querying directly
	var count int
	err = testDB.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM urls WHERE short_code = $1", shortCode).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// TestGetURL_Success verifies retrieving URL details
func TestGetURL_Success(t *testing.T) {
	ctx := context.Background()
	testDB.Cleanup(ctx)
	testCache.Cleanup(ctx)

	srv, baseURL := setupTestServer(t)
	defer srv.Shutdown(ctx)

	// Create a short URL first
	reqBody := map[string]string{"url": "https://www.example.org"}
	body, _ := json.Marshal(reqBody)
	resp, err := http.Post(baseURL+"/api/v1/shorten", "application/json", bytes.NewBuffer(body))
	require.NoError(t, err)
	var createResp map[string]any
	json.NewDecoder(resp.Body).Decode(&createResp)
	resp.Body.Close()
	shortCode := jsonValueToString(createResp["short_code"])

	// Get URL metadata
	resp, err = http.Get(baseURL + "/api/v1/urls/" + shortCode)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var getResp map[string]any
	json.NewDecoder(resp.Body).Decode(&getResp)
	assert.Equal(t, shortCode, jsonValueToString(getResp["short_code"]))
	assert.Equal(t, "https://www.example.org", jsonValueToString(getResp["original_url"]))
}

// TestDeleteURL_Success verifies successful URL deletion
func TestDeleteURL_Success(t *testing.T) {
	ctx := context.Background()
	testDB.Cleanup(ctx)
	testCache.Cleanup(ctx)

	srv, baseURL := setupTestServer(t)
	defer srv.Shutdown(ctx)

	// Create a short URL
	reqBody := map[string]string{"url": "https://delete.example"}
	body, _ := json.Marshal(reqBody)
	resp, err := http.Post(baseURL+"/api/v1/shorten", "application/json", bytes.NewBuffer(body))
	require.NoError(t, err)
	var createResp map[string]any
	json.NewDecoder(resp.Body).Decode(&createResp)
	resp.Body.Close()
	shortCode := jsonValueToString(createResp["short_code"])

	// Delete the URL
	req, _ := http.NewRequest(http.MethodDelete, baseURL+"/api/v1/urls/"+shortCode, nil)
	delResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer delResp.Body.Close()
	assert.Equal(t, http.StatusNoContent, delResp.StatusCode)

	// Verify GET now returns 404
	resp, err = http.Get(baseURL + "/api/v1/urls/" + shortCode)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestFullFlow_CreateGetRedirectDelete verifies the complete workflow
func TestFullFlow_CreateGetRedirectDelete(t *testing.T) {
	ctx := context.Background()
	testDB.Cleanup(ctx)
	testCache.Cleanup(ctx)

	srv, baseURL := setupTestServer(t)
	defer srv.Shutdown(ctx)

	// Create
	reqBody := map[string]string{"url": "https://fullflow.example"}
	body, _ := json.Marshal(reqBody)
	resp, err := http.Post(baseURL+"/api/v1/shorten", "application/json", bytes.NewBuffer(body))
	require.NoError(t, err)
	var createResp map[string]any
	json.NewDecoder(resp.Body).Decode(&createResp)
	resp.Body.Close()
	shortCode := jsonValueToString(createResp["short_code"])

	// Get
	resp, err = http.Get(baseURL + "/api/v1/urls/" + shortCode)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Redirect (no follow)
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err = client.Get(baseURL + "/" + shortCode)
	require.NoError(t, err)
	assert.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	resp.Body.Close()

	// Delete
	req, _ := http.NewRequest(http.MethodDelete, baseURL+"/api/v1/urls/"+shortCode, nil)
	delResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, delResp.StatusCode)
	delResp.Body.Close()

	// Verify gone
	resp, err = http.Get(baseURL + "/api/v1/urls/" + shortCode)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

// TestCreateShortURL_InvalidRequest tests error handling
func TestCreateShortURL_InvalidRequest(t *testing.T) {
	ctx := context.Background()
	testDB.Cleanup(ctx)
	testCache.Cleanup(ctx)

	srv, baseURL := setupTestServer(t)
	defer srv.Shutdown(ctx)

	tests := []struct {
		name           string
		requestBody    string
		expectedStatus int
	}{
		{
			name:           "empty body",
			requestBody:    "",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "missing url field",
			requestBody:    `{"invalid": "field"}`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "empty url value",
			requestBody:    `{"url": ""}`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "invalid url format",
			requestBody:    `{"url": "not-a-valid-url"}`,
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var bodyReader *bytes.Reader
			if tt.requestBody == "" {
				bodyReader = bytes.NewReader(nil)
			} else {
				bodyReader = bytes.NewReader([]byte(tt.requestBody))
			}
			resp, err := http.Post(baseURL+"/api/v1/shorten", "application/json", bodyReader)
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, tt.expectedStatus, resp.StatusCode)
		})
	}
}

// helper to convert JSON-decoded any to string
func jsonValueToString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	// fallback
	b, _ := json.Marshal(v)
	return string(b)
}

// TestRedirect_Success verifies short URL redirect works
func TestRedirect_Success(t *testing.T) {
	ctx := context.Background()
	testDB.Cleanup(ctx)
	testCache.Cleanup(ctx)

	srv, baseURL := setupTestServer(t)
	defer srv.Shutdown(ctx)

	// First, create a short URL
	reqBody := map[string]string{"url": "https://www.google.com"}
	body, _ := json.Marshal(reqBody)
	resp, err := http.Post(baseURL+"/api/v1/shorten", "application/json", bytes.NewBuffer(body))
	require.NoError(t, err)
	var createResp map[string]any
	json.NewDecoder(resp.Body).Decode(&createResp)
	resp.Body.Close()
	shortCode := jsonValueToString(createResp["short_code"])

	// Make GET request to /{short_code} with redirect disabled
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse // Don't follow redirects
	}}
	resp, err = client.Get(baseURL + "/" + shortCode)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Assert status code is 301 Moved Permanently
	assert.Equal(t, http.StatusMovedPermanently, resp.StatusCode)

	// Assert Location header contains the original URL
	assert.Equal(t, "https://www.google.com", resp.Header.Get("Location"))
}

func TestGetURL_NotFound(t *testing.T) {
	ctx := context.Background()
	testDB.Cleanup(ctx)
	testCache.Cleanup(ctx)

	srv, baseURL := setupTestServer(t)
	defer srv.Shutdown(ctx)

	resp, err := http.Get(baseURL + "/api/v1/urls/nonexistent")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestCreateShortURL_ServerCollisionRetry verifies that when the server
// generates the same candidate short code for the same long URL, the
// service will retry and produce a different short code (server-level).
func TestCreateShortURL_ServerCollisionRetry(t *testing.T) {
	ctx := context.Background()
	testDB.Cleanup(ctx)
	testCache.Cleanup(ctx)

	srv, baseURL := setupTestServer(t)
	defer srv.Shutdown(ctx)

	longURL := "https://collision-server.example"
	reqBody := map[string]string{"url": longURL}
	body, _ := json.Marshal(reqBody)

	// First create
	resp1, err := http.Post(baseURL+"/api/v1/shorten", "application/json", bytes.NewBuffer(body))
	require.NoError(t, err)
	var create1 map[string]any
	json.NewDecoder(resp1.Body).Decode(&create1)
	resp1.Body.Close()
	code1 := jsonValueToString(create1["short_code"])
	require.NotEmpty(t, code1)

	// Second create with same long URL — service should retry on collision
	resp2, err := http.Post(baseURL+"/api/v1/shorten", "application/json", bytes.NewBuffer(body))
	require.NoError(t, err)
	var create2 map[string]any
	json.NewDecoder(resp2.Body).Decode(&create2)
	resp2.Body.Close()
	code2 := jsonValueToString(create2["short_code"])
	require.NotEmpty(t, code2)

	require.NotEqual(t, code1, code2, "expected different short codes after retry, got same %s", code1)

	// verify both codes resolve to the original URL
	redirectClient := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	resp, err := redirectClient.Get(baseURL + "/" + code1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	assert.Equal(t, longURL, resp.Header.Get("Location"))
	resp.Body.Close()

	resp, err = redirectClient.Get(baseURL + "/" + code2)
	require.NoError(t, err)
	assert.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	assert.Equal(t, longURL, resp.Header.Get("Location"))
	resp.Body.Close()
}

// TestCache_URLIsCachedAfterCreate verifies URL is cached after creation
func TestCache_URLIsCachedAfterCreate(t *testing.T) {
	ctx := context.Background()
	testDB.Cleanup(ctx)
	testCache.Cleanup(ctx)

	srv, baseURL := setupTestServer(t)
	defer srv.Shutdown(ctx)

	// Create a short URL
	reqBody := map[string]string{"url": "https://cache-create.example"}
	body, _ := json.Marshal(reqBody)
	resp, err := http.Post(baseURL+"/api/v1/shorten", "application/json", bytes.NewBuffer(body))
	require.NoError(t, err)
	var createResp map[string]any
	json.NewDecoder(resp.Body).Decode(&createResp)
	resp.Body.Close()
	shortCode := jsonValueToString(createResp["short_code"])

	// Verify URL is cached
	cacheKey := "url:" + shortCode
	exists, err := testCache.Client.Exists(ctx, cacheKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists, "URL should be cached after creation")
}

// TestCache_ServedFromCacheAfterGet verifies subsequent reads use cache
func TestCache_ServedFromCacheAfterGet(t *testing.T) {
	ctx := context.Background()
	testDB.Cleanup(ctx)
	testCache.Cleanup(ctx)

	srv, baseURL := setupTestServer(t)
	defer srv.Shutdown(ctx)

	// Create a short URL
	reqBody := map[string]string{"url": "https://cache-get.example"}
	body, _ := json.Marshal(reqBody)
	resp, err := http.Post(baseURL+"/api/v1/shorten", "application/json", bytes.NewBuffer(body))
	require.NoError(t, err)
	var createResp map[string]any
	json.NewDecoder(resp.Body).Decode(&createResp)
	resp.Body.Close()
	shortCode := jsonValueToString(createResp["short_code"])

	// First GET to ensure cached
	resp, err = http.Get(baseURL + "/api/v1/urls/" + shortCode)
	require.NoError(t, err)
	resp.Body.Close()

	// Delete from DB directly (bypass cache)
	_, err = testDB.Pool.Exec(ctx, "DELETE FROM urls WHERE short_code = $1", shortCode)
	require.NoError(t, err)

	// Second GET should still succeed (served from cache)
	resp, err = http.Get(baseURL + "/api/v1/urls/" + shortCode)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "Should be served from cache even though DB record deleted")
}

// TestCache_InvalidatedOnDelete verifies cache is cleared after deletion
func TestCache_InvalidatedOnDelete(t *testing.T) {
	ctx := context.Background()
	testDB.Cleanup(ctx)
	testCache.Cleanup(ctx)

	srv, baseURL := setupTestServer(t)
	defer srv.Shutdown(ctx)

	// Create a short URL
	reqBody := map[string]string{"url": "https://cache-delete.example"}
	body, _ := json.Marshal(reqBody)
	resp, err := http.Post(baseURL+"/api/v1/shorten", "application/json", bytes.NewBuffer(body))
	require.NoError(t, err)
	var createResp map[string]any
	json.NewDecoder(resp.Body).Decode(&createResp)
	resp.Body.Close()
	shortCode := jsonValueToString(createResp["short_code"])

	// Verify cached
	cacheKey := "url:" + shortCode
	exists, _ := testCache.Client.Exists(ctx, cacheKey).Result()
	require.Equal(t, int64(1), exists, "URL should be cached before delete")

	// Delete via API
	req, _ := http.NewRequest(http.MethodDelete, baseURL+"/api/v1/urls/"+shortCode, nil)
	delResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	delResp.Body.Close()

	// Verify cache invalidated
	exists, _ = testCache.Client.Exists(ctx, cacheKey).Result()
	assert.Equal(t, int64(0), exists, "Cache should be invalidated after delete")
}

// TestCache_NegativeCaching verifies non-existent URLs are negatively cached
func TestCache_NegativeCaching(t *testing.T) {
	ctx := context.Background()
	testDB.Cleanup(ctx)
	testCache.Cleanup(ctx)

	srv, baseURL := setupTestServer(t)
	defer srv.Shutdown(ctx)

	// Request non-existent URL
	resp, err := http.Get(baseURL + "/api/v1/urls/nonexistent123")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	// Verify negative cache entry
	cacheKey := "url:nonexistent123"
	cached, err := testCache.Client.Get(ctx, cacheKey).Result()
	require.NoError(t, err)
	assert.Equal(t, "__NOT_FOUND__", cached, "Non-existent URL should be negatively cached")
}
