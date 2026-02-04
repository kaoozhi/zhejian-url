package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all application configuration
type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	App      AppConfig
	Cache    CacheConfig
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
	Host string
	Port string
	TTL  time.Duration
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

// Load loads configuration from environment variables
func Load() *Config {
	_ = godotenv.Load("../../../../.env")

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
			Host: getEnv("CACHE_HOST", "localhost"),
			Port: getEnv("CACHE_PORT", "6379"),
			TTL:  getEnvDuration("CACHE_TTL", 5*time.Minute),
		},
		App: AppConfig{
			BaseURL:          getEnv("BASE_URL", "http://localhost:8080"),
			ShortCodeLen:     getEnvInt("SHORT_CODE_LENGTH", 6),
			ShortCodeRetries: getEnvInt("SHORT_CODE_MAX_RETRIES", 3),
			MaxAliasLen:      20,
			MinAliasLen:      3,
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

func getEnvDuration(key string, defaultVal time.Duration) time.Duration {
	if val := os.Getenv(key); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			return d
		}
	}
	return defaultVal
}
