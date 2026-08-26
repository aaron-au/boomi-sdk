// Package version holds the SDK version string and builds the User-Agent
// value sent on every request.
package version

// Version is the SDK version. Bumped on release.
const Version = "0.1.0-dev"

// UserAgent returns the User-Agent string for requests:
// "boomi-sdk-go/{Version}", followed by a space and extra when extra is
// non-empty (the caller-supplied Config.UserAgent).
func UserAgent(extra string) string {
	ua := "boomi-sdk-go/" + Version
	if extra != "" {
		ua += " " + extra
	}

	return ua
}
