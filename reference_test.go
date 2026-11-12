package main

import "testing"

func TestParseRef(t *testing.T) {
	cases := []struct {
		in        string
		wantOK    bool
		cacheable bool
	}{
		{"op://vault/item/field", true, true},
		{"op://vault/item/section/field", true, true},
		{"op://app-prod/db/password", true, true},
		{"op://v/i/one-time password?attribute=otp", true, false},
		{"op://v/i/f?attribute=OTP", true, false},
		{"op://v/i/f?ssh-format=openssh", true, true},
		{"op://vault/item", false, false},
		{"op://vault//field", false, false},
		{"", false, false},
		{"not-a-ref", false, false},
		{"https://example.com/a/b/c", false, false},
	}
	for _, c := range cases {
		ref, ok := ParseRef(c.in)
		if ok != c.wantOK {
			t.Errorf("ParseRef(%q) ok = %v, want %v", c.in, ok, c.wantOK)
			continue
		}
		if ok && ref.Cacheable() != c.cacheable {
			t.Errorf("ParseRef(%q).Cacheable() = %v, want %v", c.in, ref.Cacheable(), c.cacheable)
		}
	}
}
