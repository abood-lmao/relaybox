// Package ids generates unguessable identifiers.
//
// Public endpoint identifiers act as bearer capabilities: anyone who knows
// one can send requests to that endpoint. They carry 128 bits of entropy from
// crypto/rand and are never derived from database row IDs.
package ids

import (
	"crypto/rand"
	"strings"
)

// PublicIDLength is the length of identifiers returned by NewPublicID.
const PublicIDLength = 26

const alphabet = "abcdefghijklmnopqrstuvwxyz234567"

// NewPublicID returns a random 26-character lowercase base32 identifier.
func NewPublicID() string {
	// rand.Text returns 26 characters of standard base32 (128 bits).
	return strings.ToLower(rand.Text())
}

// NewToken returns a random secret suitable for RELAYBOX_DASHBOARD_TOKEN.
func NewToken() string {
	return strings.ToLower(rand.Text() + rand.Text())
}

// IsPublicID reports whether s has the shape of an identifier produced by
// NewPublicID. It does not check that the identifier exists.
func IsPublicID(s string) bool {
	if len(s) != PublicIDLength {
		return false
	}
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(alphabet, s[i]) < 0 {
			return false
		}
	}
	return true
}
