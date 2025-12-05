package engine

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"
)

// TestSanitizeDBString tests the actual sanitizeDBString function
// This tests REAL code that handles malicious/corrupted input
func TestSanitizeDBString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{
			name:     "clean string",
			input:    "normal text",
			expected: "normal text",
		},
		{
			name:     "string with null byte",
			input:    "text\x00with\x00nulls",
			expected: "textwithnulls",
		},
		{
			name:     "invalid UTF-8",
			input:    "text\xffwith\xfebad\xfdUTF8",
			expected: "", // Will be replaced with valid UTF-8
		},
		{
			name:     "SQL injection attempt with nulls",
			input:    "'; DROP TABLE\x00 users--",
			expected: "'; DROP TABLE users--",
		},
		{
			name:     "multiple consecutive nulls",
			input:    "a\x00\x00\x00b",
			expected: "ab",
		},
		{
			name:     "only null bytes",
			input:    "\x00\x00\x00",
			expected: "",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "unicode characters",
			input:    "Hello 世界 🌍",
			expected: "Hello 世界 🌍",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeDBString(tt.input)

			// For invalid UTF-8, just verify it becomes valid
			if tt.name == "invalid UTF-8" {
				assert.True(t, utf8.ValidString(result), "output should be valid UTF-8")
			} else {
				assert.Equal(t, tt.expected, result)
			}

			// All outputs should be valid UTF-8
			assert.True(t, utf8.ValidString(result), "output must be valid UTF-8")

			// All outputs should have no null bytes
			assert.NotContains(t, result, "\x00", "output should not contain null bytes")
		})
	}
}

// TestSanitizeDBStringDoesNotRemoveLegitimateData tests that sanitization
// doesn't accidentally remove legitimate data
func TestSanitizeDBStringDoesNotRemoveLegitimateData(t *testing.T) {
	legitimateInputs := []string{
		"Team01",
		"web-service-01",
		"Error: Connection timeout after 30s",
		"Debug: Connected to 10.100.11.2:80",
		"Service check passed with response: 200 OK",
		"Failed to connect: connection refused",
	}

	for _, input := range legitimateInputs {
		t.Run(input, func(t *testing.T) {
			result := sanitizeDBString(input)
			assert.Equal(t, input, result, "legitimate data should not be modified")
		})
	}
}

// TestSanitizeDBStringWithActualMaliciousInput tests with real attack vectors
func TestSanitizeDBStringWithActualMaliciousInput(t *testing.T) {
	maliciousInputs := []struct {
		name  string
		input string
	}{
		{
			name:  "embedded null to bypass filters",
			input: "admin\x00' OR '1'='1",
		},
		{
			name:  "null-terminated XSS",
			input: "<script>alert('XSS')</script>\x00",
		},
		{
			name:  "command injection with null",
			input: "test\x00; rm -rf /",
		},
	}

	for _, tt := range maliciousInputs {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeDBString(tt.input)

			// Should remove null bytes
			assert.NotContains(t, result, "\x00")

			// Should still be valid UTF-8
			assert.True(t, utf8.ValidString(result))

			// Result should be different from input (sanitized)
			assert.NotEqual(t, tt.input, result, "malicious input should be sanitized")
		})
	}
}

// TestPropertySanitizeRemovesNullBytes verifies null bytes are always removed
func TestPropertySanitizeRemovesNullBytes(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate string with random null byte positions
		baseString := rapid.StringN(1, 100, -1).Draw(t, "baseString")
		numNulls := rapid.IntRange(1, 20).Draw(t, "numNulls")

		// Insert null bytes at random positions
		dirtyString := baseString
		for i := 0; i < numNulls; i++ {
			pos := rapid.IntRange(0, len(dirtyString)).Draw(t, "pos")
			dirtyString = dirtyString[:pos] + "\x00" + dirtyString[pos:]
		}

		result := sanitizeDBString(dirtyString)

		// Property: Result must never contain null bytes
		assert.NotContains(t, result, "\x00", "sanitized string must not contain null bytes")
		assert.True(t, utf8.ValidString(result), "result should be valid UTF-8")

		// Property: Length should be equal or shorter (nulls removed)
		assert.LessOrEqual(t, len(result), len(dirtyString), "sanitized string should not be longer")
	})
}

// TestPropertySanitizeIsIdempotent verifies sanitization is idempotent
func TestPropertySanitizeIsIdempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate any random string
		input := rapid.String().Draw(t, "input")

		result1 := sanitizeDBString(input)
		result2 := sanitizeDBString(result1)

		// Property: Sanitizing twice should give same result
		assert.Equal(t, result1, result2, "sanitization should be idempotent")
	})
}
