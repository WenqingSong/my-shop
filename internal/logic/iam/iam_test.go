package iam

import (
	"strings"
	"testing"
)

func TestValidateUsername(t *testing.T) {
	cases := []struct {
		name  string
		value string
		ok    bool
	}{
		{"min length", "abc", true},
		{"max length", strings.Repeat("a", 24), true},
		{"digits", "123", true},
		{"mixed alphanumeric", "abc123", true},
		{"too short", "ab", false},
		{"too long", strings.Repeat("a", 25), false},
		{"empty", "", false},
		{"space", "abc def", false},
		{"hyphen", "abc-def", false},
		{"underscore", "abc_def", false},
		{"unicode", "用户名", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateUsername(c.value)
			if c.ok && err != nil {
				t.Fatalf("expected valid %q, got error %v", c.value, err)
			}
			if !c.ok && err == nil {
				t.Fatalf("expected error for %q, got nil", c.value)
			}
		})
	}
}

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name  string
		value string
		ok    bool
	}{
		{"min length", strings.Repeat("a", 8), true},
		{"max length", strings.Repeat("a", 24), true},
		{"too short", strings.Repeat("a", 7), false},
		{"too long", strings.Repeat("a", 25), false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validatePassword(c.value)
			if c.ok && err != nil {
				t.Fatalf("expected valid %q, got error %v", c.value, err)
			}
			if !c.ok && err == nil {
				t.Fatalf("expected error for %q, got nil", c.value)
			}
		})
	}
}
