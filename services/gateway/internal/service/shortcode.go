package service

import (
	"crypto/sha256"
	"encoding/binary"
	"net/url"
	"strings"

	"github.com/zhejian/url-shortener/gateway/internal/repository"
)

// Base62 character set for short code generation
const base62Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// ShortCodeGenerator handles generation of unique short codes
type ShortCodeGenerator struct {
	repo       *repository.URLRepository // to check collisions later
	codeLength int
	maxRetries int
}

// NewShortCodeGenerator creates a new short code generator
func NewShortCodeGenerator(codeLength int, maxRetries int, repo *repository.URLRepository) *ShortCodeGenerator {
	return &ShortCodeGenerator{
		repo:       repo,
		codeLength: codeLength,
		maxRetries: maxRetries,
	}
}

// Canonicalize normalizes a long URL for hashing and comparison.
// It lowercases the host, removes default ports, strips a trailing slash
// and removes URL fragments.
func Canonicalize(longURL string) (string, error) {
	u, err := url.Parse(longURL)
	if err != nil {
		return "", err
	}
	// Lowercase the host
	u.Host = strings.ToLower(u.Host)

	// Remove default ports
	// u.Host might be "example.com:443" → "example.com"
	if u.Scheme == "https" && strings.HasSuffix(u.Host, ":443") {
		u.Host = strings.TrimSuffix(u.Host, ":443")
	}
	if u.Scheme == "http" && strings.HasSuffix(u.Host, ":80") {
		u.Host = strings.TrimSuffix(u.Host, ":80")
	}

	// Remove trailing slash from path (optional)
	u.Path = strings.TrimSuffix(u.Path, "/")

	// Remove fragment (#section) - usually not sent to server anyway
	u.Fragment = ""

	// Return the full normalized URL
	return u.String(), nil
}

// Hash function using sha256
func HashURL(s string) uint64 {
	h := sha256.Sum256([]byte(s))

	return binary.BigEndian.Uint64(h[:8])
}

// Generate creates a short code from the given long URL.
// Current implementation hashes the canonicalized URL and takes the
// first `codeLength` characters of its Base62 encoding. Collision
// detection and retry logic (checking the repository) should be
// implemented externally or added here in the future.
func (g *ShortCodeGenerator) Generate(longURL string) (string, error) {
	c, err := Canonicalize(longURL)
	if err != nil {
		return "", ErrInvalidURL
	}
	h := HashURL(c)
	s := EncodeBase62(h)
	if len(s) < g.codeLength {
		return "", ErrShortCodeGeneration
	}
	return s[:g.codeLength], nil
}

// EncodeBase62 encodes a number to Base62 string
func EncodeBase62(num uint64) string {
	if num == 0 {
		return string(base62Chars[0]) // "0"
	}
	encoded := ""
	for num > 0 {
		remainder := num % 62
		encoded = string(base62Chars[remainder]) + encoded
		num = num / 62
	}
	return encoded
}
