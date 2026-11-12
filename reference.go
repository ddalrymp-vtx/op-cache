package main

import (
	"net/url"
	"strings"
)

// SecretRef is a parsed op:// secret reference.
type SecretRef struct {
	Raw   string
	Query url.Values
}

// ParseRef parses a secret reference. ok is false for anything that is not a
// well-formed op:// reference, which sends the caller down the passthrough path.
func ParseRef(s string) (SecretRef, bool) {
	if !strings.HasPrefix(s, "op://") {
		return SecretRef{}, false
	}
	rest := strings.TrimPrefix(s, "op://")
	q := url.Values{}
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		parsed, err := url.ParseQuery(rest[i+1:])
		if err != nil {
			return SecretRef{}, false
		}
		q = parsed
		rest = rest[:i]
	}
	// vault/item/field, optionally vault/item/section/field. Require at least
	// three non-empty path segments.
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 3 {
		return SecretRef{}, false
	}
	for _, p := range parts {
		if p == "" {
			return SecretRef{}, false
		}
	}
	return SecretRef{Raw: s, Query: q}, true
}

// Cacheable reports whether a reference may be cached.
//
// One-time passwords are time-based: a cached TOTP is wrong within 30 seconds,
// so those always go to the real op.
func (r SecretRef) Cacheable() bool {
	return !strings.EqualFold(r.Query.Get("attribute"), "otp")
}
