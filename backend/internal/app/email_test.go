package app

import "testing"

func TestValidEmail(t *testing.T) {
	ok := []string{"operator@localhost", "operator@example.com"}
	bad := []string{"", "operator", "a@b", "Name <operator@example.com>"}
	for _, email := range ok {
		if !validEmail(email) {
			t.Fatalf("rejected %s", email)
		}
	}
	for _, email := range bad {
		if validEmail(email) {
			t.Fatalf("accepted %s", email)
		}
	}
}
