package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all application configuration
type Config struct {
	Server      ServerConfig
	Database    DatabaseConfig
	App         AppConfig
	Cache       CacheConfig
	RateLimiter RateLimiterConfig
	Analytics   AnalyticsConfig
}

// ServerConfig holds HTTP server configuration
type ServerConfig struct {
	Port string
	// ReadTimeout  time.Duration
	// WriteTimeout time.Duration
}

// DatabaseConfig holds database connection configuration
type DatabaseConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
	SSLMode  string
	// MaxConns    int32
	// MinConns    int32
	// MaxConnLife time.Duration
	// MaxConnIdle time.Duration
}

// Redis Caching Layer configuration
type CacheConfig struct {
	Host             string
	Port             string
	TTL              time.Duration
	ReadTimeout      time.Duration // per-operation read deadline; 0 = go-redis default (3 s)
	WriteTimeout     time.Duration // per-operation write deadline; 0 = go-redis default (3 s)
	OperationTimeout time.Duration // context deadline for each cache call
	PoolSize         int           // go-redis connection pool size per node (CACHE_POOL_SIZE)
	Nodes            []string

	// Circuit breaker tuning (see repository.CBSettings for semantics)
	CBMinRequests        uint32        // CACHE_CB_MIN_REQUESTS
	CBFailureRate        float64       // CACHE_CB_FAILURE_RATE
	CBConsecutiveFailures uint32       // CACHE_CB_CONSECUTIVE_FAILURES
	CBTimeout            time.Duration // CACHE_CB_TIMEOUT — CB recovery window
}

// AppConfig holds application-specific configuration
type AppConfig struct {
	BaseURL          string // Base URL for generating short links
	DefaultExpiry    time.Duration
	ShortCodeLen     int
	ShortCodeRetries int
	MaxAliasLen      int
	MinAliasLen      int
}

type RateLimiterConfig struct {
	Addr    string
	Timeout time.Duration // per-call gRPC timeout, default 100ms
	Enabled bool
}

type AnalyticsConfig struct {
	AMQPURL string // e.g. "amqp://guest:guest@rabbitmq:5672/" — empty means disabled
	Enabled bool
}

// Load loads configuration from environment variables
func Load() *Config {
	_ = godotenv.Load("../../../../.env")
	rateLimiterAddr := getEnv("RATE_LIMITER_ADDR", "")
	amqpURL := getEnv("AMQP_URL", "")
	return &Config{
		Server: ServerConfig{
			Port: getEnv("PORT", "8080"),
			// ReadTimeout:  10 * time.Second,
			// WriteTimeout: 10 * time.Second,
		},
		Database: DatabaseConfig{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     getEnv("DB_PORT", "5432"),
			User:     getEnv("DB_USER", "zhejian"),
			Password: getEnv("DB_PASSWORD", "zhejian_secret"),
			DBName:   getEnv("DB_NAME", "urlshortener"),
			SSLMode:  getEnv("DB_SSLMODE", "disable"),
			// MaxConns:    10,
			// MinConns:    2,
			// MaxConnLife: time.Hour,
			// MaxConnIdle: 30 * time.Minute,
		},
		Cache: CacheConfig{
			Host:             getEnv("CACHE_HOST", "localhost"),
			Port:             getEnv("CACHE_PORT", "6379"),
			TTL:              getEnvDuration("CACHE_TTL", 5*time.Minute),
			ReadTimeout:      getEnvDuration("CACHE_READ_TIMEOUT", 500*time.Millisecond),
			WriteTimeout:     getEnvDuration("CACHE_WRITE_TIMEOUT", 500*time.Millisecond),
			OperationTimeout: getEnvDuration("CACHE_OPERATION_TIMEOUT", 50*time.Millisecond),
			PoolSize:         getEnvInt("CACHE_POOL_SIZE", 50),
			Nodes: getCacheNodes(
				getEnv("CACHE_HOST", "localhost"),
				getEnv("CACHE_PORT", "6379"),
			),
			CBMinRequests:         uint32(getEnvInt("CACHE_CB_MIN_REQUESTS", 50)),
			CBFailureRate:         getEnvFloat64("CACHE_CB_FAILURE_RATE", 0.2),
			CBConsecutiveFailures: uint32(getEnvInt("CACHE_CB_CONSECUTIVE_FAILURES", 5)),
			CBTimeout:             getEnvDuration("CACHE_CB_TIMEOUT", 30*time.Second),
		},
		App: AppConfig{
			BaseURL:          getEnv("BASE_URL", "http://localhost:8080"),
			ShortCodeLen:     getEnvInt("SHORT_CODE_LENGTH", 6),
			ShortCodeRetries: getEnvInt("SHORT_CODE_MAX_RETRIES", 3),
			MaxAliasLen:      20,
			MinAliasLen:      3,
		},
		RateLimiter: RateLimiterConfig{
			Addr:    rateLimiterAddr,
			Timeout: getEnvDuration("RATE_LIMITER_TIMEOUT", 100*time.Millisecond),
			Enabled: rateLimiterAddr != "",
		},
		Analytics: AnalyticsConfig{
			AMQPURL: amqpURL,
			Enabled: amqpURL != "",
		},
	}
}

type ConnectionInterface interface {
	ConnectionString() string
}

// ConnectionString returns the PostgreSQL connection string
func (d *DatabaseConfig) ConnectionString() string {
	connectionString := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", d.User, d.Password, d.Host, d.Port, d.DBName, d.SSLMode)
	return connectionString
}

func (c *CacheConfig) ConnectionString() string {
	return fmt.Sprintf("redis://%s:%s/0", c.Host, c.Port)
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}

func getEnvFloat64(key string, defaultVal float64) float64 {
	if val := os.Getenv(key); val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f
		}
	}
	return defaultVal
}

func getEnvDuration(key string, defaultVal time.Duration) time.Duration {
	if val := os.Getenv(key); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			return d
		}
	}
	return defaultVal
}

func getCacheNodes(defaultHost, defaultPort string) []string {
	cacheNodesEnv := getEnv("CACHE_NODES", "")
	if cacheNodesEnv == "" {
		return []string{fmt.Sprintf("%s:%s", defaultHost, defaultPort)}
	}
	var cacheNodes []string
	for _, node := range strings.Split(cacheNodesEnv, ",") {
		if node = strings.TrimSpace(node); node != "" {
			cacheNodes = append(cacheNodes, node)
		}
	}
	return cacheNodes
}
