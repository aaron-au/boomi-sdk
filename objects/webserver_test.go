package objects_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aaron-au/boomi-sdk/objects"
)

func TestSharedWebServerAcceptsBothSpellings(t *testing.T) {
	requireDo(t)

	// A cloud attachment answers "users" and "cloudTennantGeneral"; a
	// local atom answers "user" and "generalSettings".
	cloud := `{
		"atomId":"atom-1",
		"cloudTennantGeneral":{"apiType":"advanced"},
		"userManagement":{"users":[{"username":"svc","token":"secret-1","usingIPFilters":false}]},
		"corsConfiguration":{"origins":[{"domain":"https://a.example"}]}
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, cloud)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	out, err := objects.NewWebServers(c).Get(context.Background(), "atom-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if len(out.Users()) != 1 || out.Users()[0].Username != "svc" {
		t.Fatalf("Users() = %+v, want the plural-spelling user", out.Users())
	}

	if len(out.Origins()) != 1 || out.Origins()[0].Domain != "https://a.example" {
		t.Fatalf("Origins() = %+v, want the plural-spelling origin", out.Origins())
	}

	raw, group := out.Settings()
	if group != "sharedWebServer.cloudTenant" || !strings.Contains(string(raw), "advanced") {
		t.Fatalf("Settings() = %s (%s), want the cloud block", raw, group)
	}
}

func TestSharedWebServerUserFilterSwitches(t *testing.T) {
	// A dormant filter list (switch off) must read as unrestricted.
	u := objects.SharedWebServerUser{
		UsingComponentFilters: false,
		ComponentFilters:      json.RawMessage(`["comp-1","comp-2"]`),
		UsingIPFilters:        true,
		IPFilters:             json.RawMessage(`["10.0.0.1"]`),
	}

	if got := u.Components(); got != nil {
		t.Fatalf("Components() = %v, want nil while filtering is off", got)
	}

	if got := u.IPs(); len(got) != 1 || got[0] != "10.0.0.1" {
		t.Fatalf("IPs() = %v, want the active allow-list", got)
	}
}

func TestRedactWebServerTokens(t *testing.T) {
	payload := json.RawMessage(`{
		"atomId":"atom-1",
		"userManagement":{
			"user":[{"username":"a","token":"hunter2"}],
			"users":[{"username":"b","token":"pass"},{"username":"c"}]
		}
	}`)

	out, err := objects.RedactWebServerTokens(payload)
	if err != nil {
		t.Fatalf("RedactWebServerTokens: %v", err)
	}

	s := string(out)
	if strings.Contains(s, "hunter2") || strings.Contains(s, `"pass"`) {
		t.Fatalf("redacted payload still carries a token: %s", s)
	}

	if !strings.Contains(s, `"*******"`) || !strings.Contains(s, `"****"`) {
		t.Fatalf("redacted payload lost the token masks: %s", s)
	}

	if !strings.Contains(s, `"username":"c"`) {
		t.Fatalf("redaction dropped a user without a token: %s", s)
	}
}

func TestWebServerUpdateRequiresConfirmation(t *testing.T) {
	c := newClient(t, "http://unused.invalid")

	_, err := objects.NewWebServers(c).UpdateRaw(
		context.Background(), "atom-1", json.RawMessage(`{}`), false,
	)
	if !errors.Is(err, objects.ErrWebServerWriteUnconfirmed) {
		t.Fatalf("err = %v, want ErrWebServerWriteUnconfirmed", err)
	}
}
