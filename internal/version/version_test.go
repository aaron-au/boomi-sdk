package version

import "testing"

func TestUserAgent(t *testing.T) {
	if got, want := UserAgent(""), "boomi-sdk-go/0.1.0-dev"; got != want {
		t.Errorf("UserAgent(\"\") = %q, want %q", got, want)
	}

	if got, want := UserAgent("companion/0.1.0"), "boomi-sdk-go/0.1.0-dev companion/0.1.0"; got != want {
		t.Errorf("UserAgent(extra) = %q, want %q", got, want)
	}
}
