package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeBase62(t *testing.T) {
	tests := []struct {
		name     string
		input    uint64
		expected string
	}{
		{"zero", 0, "0"},
		{"single digit max", 61, "z"},
		{"two digits", 62, "10"},
		{"larger number", 12345, "3D7"},
		{"max uint64", 18446744073709551615, "LygHa16AHYF"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EncodeBase62(tt.input)
			assert.Equal(t, tt.expected, result, "EncodeBase62(%d)", tt.input)
		})
	}
}

func TestCanonicalize(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{
			name:     "lowercase host",
			input:    "https://EXAMPLE.COM/page",
			expected: "https://example.com/page",
			wantErr:  false,
		},
		{
			name:     "remove https default port",
			input:    "https://example.com:443/page",
			expected: "https://example.com/page",
			wantErr:  false,
		},
		{
			name:     "remove http default port",
			input:    "http://example.com:80/page",
			expected: "http://example.com/page",
			wantErr:  false,
		},
		{
			name:     "keep non-default port",
			input:    "https://example.com:8080/page",
			expected: "https://example.com:8080/page",
			wantErr:  false,
		},
		{
			name:     "remove trailing slash",
			input:    "https://example.com/page/",
			expected: "https://example.com/page",
			wantErr:  false,
		},
		{
			name:     "remove fragment",
			input:    "https://example.com/page#section",
			expected: "https://example.com/page",
			wantErr:  false,
		},
		{
			name:     "keep query params",
			input:    "https://example.com/page?foo=bar",
			expected: "https://example.com/page?foo=bar",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Canonicalize(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result, "Canonicalize(%s)", tt.input)
		})
	}
}

func TestHashURL(t *testing.T) {
	// Same input should produce same output
	url := "https://example.com/page"
	hash1 := HashURL(url)
	hash2 := HashURL(url)

	assert.Equal(t, hash1, hash2, "HashURL should be deterministic")

	// Different inputs should produce different outputs
	hash3 := HashURL("https://example.com/other")
	assert.NotEqual(t, hash1, hash3, "HashURL should produce different hashes for different URLs")

	// Should not be zero for valid URL
	assert.NotZero(t, hash1, "HashURL should not return 0 for valid URL")
}

func TestShortCodeGenerator_Generate(t *testing.T) {
	generator := NewShortCodeGenerator(8, 5, nil)

	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name:    "valid https URL",
			url:     "https://example.com/very/long/path/to/page",
			wantErr: false,
		},
		{
			name:    "valid http URL",
			url:     "http://example.com/page",
			wantErr: false,
		},
		{
			name:    "URL with query params",
			url:     "https://example.com/page?foo=bar&baz=qux",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, err := generator.Generate(tt.url)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			// Check code length
			assert.Len(t, code, 8, "code length should be 8")

			// Check code contains only base62 characters
			for _, c := range code {
				assert.True(t, strings.ContainsRune(base62Chars, c), "code contains invalid character: %c", c)
			}
		})
	}
}

func TestShortCodeGenerator_Generate_Deterministic(t *testing.T) {
	generator := NewShortCodeGenerator(8, 5, nil)

	url := "https://example.com/page"
	code1, _ := generator.Generate(url)
	code2, _ := generator.Generate(url)

	assert.Equal(t, code1, code2, "Generate should be deterministic")
}

func TestShortCodeGenerator_Generate_DifferentURLs(t *testing.T) {
	generator := NewShortCodeGenerator(8, 5, nil)

	code1, _ := generator.Generate("https://example.com/page1")
	code2, _ := generator.Generate("https://example.com/page2")

	assert.NotEqual(t, code1, code2, "Generate should produce different codes for different URLs")
}

func TestShortCodeGenerator_Generate_NormalizedURLs(t *testing.T) {
	generator := NewShortCodeGenerator(8, 5, nil)

	// These should produce the same code after canonicalization
	code1, _ := generator.Generate("https://EXAMPLE.COM/page")
	code2, _ := generator.Generate("https://example.com/page")

	assert.Equal(t, code1, code2, "Generate should normalize URLs")
}
