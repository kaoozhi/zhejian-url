package config

import (
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all application configuration
type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	App      AppConfig
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

// AppConfig holds application-specific configuration
type AppConfig struct {
	BaseURL       string // Base URL for generating short links
	DefaultExpiry time.Duration
	ShortCodeLen  int
	MaxAliasLen   int
	MinAliasLen   int
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	// TODO: Implement configuration loading
	// 1. Read from environment variables
	// 2. Apply defaults for missing values
	// 3. Validate required fields
	// 4. Return config or error

	// Example environment variables to read:
	// - SERVER_PORT (default: "8080")
	// - DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME
	// - BASE_URL (e.g., "https://short.url")
	// - DEFAULT_EXPIRY (default: 0 for no expiry)
	// - SHORT_CODE_LENGTH (default: 7)
	_ = godotenv.Load()
	return &Config{
		Server: ServerConfig{
			Port: getEnv("PORT", "8080"),
			// ReadTimeout:  10 * time.Second,
			// WriteTimeout: 10 * time.Second,
		},
		Database: DatabaseConfig{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     getEnv("DB_PORT", "5434"),
			User:     getEnv("DB_USER", "zhejian"),
			Password: getEnv("DB_PASSWORD", "zhejian_secret"),
			DBName:   getEnv("DB_NAME", "urlshortener"),
			SSLMode:  getEnv("DB_SSLMODE", "disable"),
			// MaxConns:    10,
			// MinConns:    2,
			// MaxConnLife: time.Hour,
			// MaxConnIdle: 30 * time.Minute,
		},
		App: AppConfig{
			BaseURL:      "http://localhost:8080",
			ShortCodeLen: 7,
			MaxAliasLen:  20,
			MinAliasLen:  3,
		},
	}, nil
}

// ConnectionString returns the PostgreSQL connection string
func (d *DatabaseConfig) ConnectionString() string {
	// TODO: Build connection string
	// Format: postgres://user:password@host:port/dbname?sslmode=disable
	connectionString := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", d.User, d.Password, d.Host, d.Port, d.DBName, d.SSLMode)
	// fmt.Printf("%s\n", connectionString)
	return connectionString
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return defaultVal
}
