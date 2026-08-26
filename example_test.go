package boomi_test

import (
	"fmt"
	"net/http"

	boomi "github.com/aaron-au/boomi-sdk"
)

// ExampleNew constructs a client from a Config the caller assembled.
// New validates and applies defaults; it performs no network I/O, and
// constructing a client never resets the account's pacing.
func ExampleNew() {
	client, err := boomi.New(boomi.Config{
		Host:      "https://api.boomi.com",
		AccountID: "myaccount-AB1CD2",
		Username:  "user@example.com",
		Token:     "api-token",
	})
	if err != nil {
		fmt.Println("config rejected:", err)
		return
	}

	fmt.Println("client ready:", client != nil)
	// Output: client ready: true
}

// ExampleKindOf maps an error onto the exit-code contract. The SDK never
// calls os.Exit; the caller owns the mapping.
func ExampleKindOf() {
	err := fmt.Errorf("fetching component: %w", &boomi.APIError{
		StatusCode: http.StatusUnauthorized,
		Method:     http.MethodGet,
		Path:       "Component/abc",
	})

	var exit int

	switch boomi.KindOf(err) {
	case boomi.KindValidation:
		exit = 2
	case boomi.KindAuth:
		exit = 3 // stop; auth failures are never retried
	case boomi.KindConflict:
		exit = 4
	case boomi.KindTransport:
		exit = 5
	case boomi.KindUnknown:
		exit = 1
	}

	fmt.Println(boomi.KindOf(err), exit)
	// Output: auth 3
}

// ExampleIsBranchUnlicensed detects the platform's refusal of a branch
// operation on an account without the branching feature, which hides
// behind a generic 400/403.
func ExampleIsBranchUnlicensed() {
	var err error = &boomi.APIError{
		StatusCode: http.StatusForbidden,
		Method:     http.MethodPost,
		Path:       "Branch/query",
		Body:       []byte("Account myaccount-AB1CD2 does not have access rights to Branch"),
	}

	fmt.Println(boomi.IsBranchUnlicensed(err))
	// Output: true
}
