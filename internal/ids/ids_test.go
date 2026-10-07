package ids

import (
	"regexp"
	"testing"
)

func TestNewEnvironmentID(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9a-z]{26}$`)
	seen := map[string]bool{}
	for range 1000 {
		id := NewEnvironmentID()
		if !pattern.MatchString(id) {
			t.Fatalf("id %q is not a 26-char lowercase ULID", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}
