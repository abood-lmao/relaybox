// Package validate provides a field-level validation error shared by the
// packages that check user input.
package validate

import (
	"sort"
	"strings"
)

// Errors maps a field name to a human-readable problem description.
type Errors map[string]string

// Add records a problem for field. The first problem per field wins.
func (e Errors) Add(field, msg string) {
	if _, exists := e[field]; !exists {
		e[field] = msg
	}
}

// Err returns e as an error, or nil when no problems were recorded.
func (e Errors) Err() error {
	if len(e) == 0 {
		return nil
	}
	return e
}

func (e Errors) Error() string {
	keys := make([]string, 0, len(e))
	for k := range e {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+e[k])
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

// IsToken reports whether s is a valid HTTP token (RFC 9110), the grammar used
// for header field names and methods.
func IsToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// IsHeaderValue reports whether s is safe to send as an HTTP header value.
// Control characters other than horizontal tab are rejected, which blocks
// CR/LF header injection.
func IsHeaderValue(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 0x20 && c != '\t') || c == 0x7f {
			return false
		}
	}
	return true
}
