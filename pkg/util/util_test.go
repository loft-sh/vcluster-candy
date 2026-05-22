package util

import (
	"reflect"
	"testing"
)

func TestNormalizeSuffixes(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "Single valid suffix",
			input:    []string{"example.com"},
			expected: []string{".example.com"},
		},
		{
			name:     "Multiple valid suffixes",
			input:    []string{"example.com", "example.org"},
			expected: []string{".example.com", ".example.org"},
		},
		{
			name:     "Duplicate suffixes",
			input:    []string{"example.com", "example.com", ".example.com"},
			expected: []string{".example.com"},
		},
		{
			name:     "Mixed capitalization",
			input:    []string{"Example.COM", "EXAMPLE.org"},
			expected: []string{".example.com", ".example.org"},
		},
		{
			name:     "With trailing periods",
			input:    []string{"example.com.", ".example.org."},
			expected: []string{".example.com", ".example.org"},
		},
		{
			name:     "Empty and whitespace",
			input:    []string{"   ", "", "example.com", "   EXAMPLE.ORG   "},
			expected: []string{".example.com", ".example.org"},
		},
		{
			name:     "Leading dot preserved",
			input:    []string{".example.com"},
			expected: []string{".example.com"},
		},
		{
			name:     "Empty input list",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "Only empty and whitespace",
			input:    []string{"", "   "},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeSuffixes(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("NormalizeSuffixes() = %v, expected %v", got, tt.expected)
			}
		})
	}
}
