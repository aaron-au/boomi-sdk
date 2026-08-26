package objects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	boomi "github.com/aaron-au/boomi-sdk"
)

// SharedServerInformation carries the runtime's shared web server basics.
// AuthToken is a live credential — redact before persisting or
// displaying.
type SharedServerInformation struct {
	AtomID            string `json:"atomId"`
	URL               string `json:"url"`
	OverrideURL       bool   `json:"overrideUrl"`
	AuthToken         string `json:"authToken"`
	HTTPPort          int    `json:"httpPort"`
	MinAuth           string `json:"minAuth"`
	Auth              string `json:"auth"`
	InternalHost      string `json:"internalHost"`
	ExternalHost      string `json:"externalHost"`
	ExternalHTTPPort  int    `json:"externalHttpPort"`
	ExternalHTTPSPort int    `json:"externalHttpsPort"`
	MaxThreads        int    `json:"maxThreads"`
	SSLCertificateID  string `json:"sslCertificateId"`
	APIType           string `json:"apiType"`
}

// SharedWebServerUser is one account allowed to call the runtime's web
// server. Token is that user's API password — the credential every
// consumer of the runtime's web services authenticates with.
//
// The nested filter lists are kept raw: the platform expresses them
// differently depending on the field, and guessing a shape would silently
// drop values rather than fail loudly. The Using* switches decide whether
// the lists apply at all — a user can carry a component list with
// filtering turned OFF, which means they may call everything.
type SharedWebServerUser struct {
	Username         string `json:"username"`
	Token            string `json:"token"`
	ExternalUsername string `json:"externalUsername"`

	// Role is the older single-role field; a live runtime sends
	// roleAssociations instead.
	Role             string          `json:"role"`
	RoleAssociations json.RawMessage `json:"roleAssociations"`

	UsingComponentFilters bool `json:"usingComponentFilters"`
	UsingIPFilters        bool `json:"usingIPFilters"`

	IPFilter         json.RawMessage `json:"ipFilter"`
	IPFilters        json.RawMessage `json:"ipFilters"`
	ComponentFilter  json.RawMessage `json:"componentFilter"`
	ComponentFilters json.RawMessage `json:"componentFilters"`
}

// Roles returns the user's role names, from whichever shape the runtime
// sent.
func (u SharedWebServerUser) Roles() []string {
	if u.Role != "" {
		return []string{u.Role}
	}

	return flattenStrings(u.RoleAssociations)
}

// IPs returns the user's IP allow-list, empty when IP filtering is
// switched off.
func (u SharedWebServerUser) IPs() []string {
	if !u.UsingIPFilters {
		return nil
	}

	return append(flattenStrings(u.IPFilter), flattenStrings(u.IPFilters)...)
}

// Components returns the component ids this user is restricted to. Empty
// means no restriction — including a user carrying a filter list with
// filtering disabled: the list is dormant, and reporting it would say the
// user is confined when they can call everything.
func (u SharedWebServerUser) Components() []string {
	if !u.UsingComponentFilters {
		return nil
	}

	return append(flattenStrings(u.ComponentFilter), flattenStrings(u.ComponentFilters)...)
}

// flattenStrings pulls a list of strings out of a value the platform
// expresses inconsistently: a bare array, a single string, or an object
// wrapping either.
func flattenStrings(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}

	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}

	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		if single == "" {
			return nil
		}

		return []string{single}
	}

	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil
	}

	var out []string
	for _, inner := range wrapper {
		out = append(out, flattenStrings(inner)...)
	}

	sort.Strings(out)

	return out
}

// SharedWebServerCORSOrigin is one allowed origin and what it may do.
type SharedWebServerCORSOrigin struct {
	Domain      string   `json:"domain"`
	Ports       []int    `json:"ports"`
	Methods     []string `json:"methods"`
	Headers     []string `json:"requestHeaders"`
	Credentials bool     `json:"allowCredentials"`
}

// SharedWebServer is the runtime's full web server configuration — auth
// mode, TLS, ports, the user list with its filters, and CORS. Richer than
// SharedServerInformation.
//
// The platform names the same things differently depending on the
// runtime, and the differences are not documented: a local atom answers
// "generalSettings" where a cloud attachment answers "cloudTennantGeneral"
// (Boomi's own spelling); users arrive under "user" on one and "users" on
// the other. Both spellings are accepted for each — picking one would
// report a cloud attachment as having no web server and no users, an
// empty result that reads as a fact.
type SharedWebServer struct {
	AtomID string `json:"atomId"`

	// The settings blocks are kept raw so every field the platform
	// sends is captured, including ones not modelled here.
	GeneralRaw      json.RawMessage `json:"generalSettings"`
	CloudGeneralRaw json.RawMessage `json:"cloudTennantGeneral"`

	UserManagement struct {
		EnableAPIMInternalRoles bool                  `json:"enableAPIMInternalRoles"`
		User                    []SharedWebServerUser `json:"user"`
		Users                   []SharedWebServerUser `json:"users"`
	} `json:"userManagement"`

	CORS struct {
		Origin  []SharedWebServerCORSOrigin `json:"origin"`
		Origins []SharedWebServerCORSOrigin `json:"origins"`
	} `json:"corsConfiguration"`
}

// Users returns the accounts, whichever spelling the runtime used.
func (s SharedWebServer) Users() []SharedWebServerUser {
	if len(s.UserManagement.Users) > 0 {
		return s.UserManagement.Users
	}

	return s.UserManagement.User
}

// Origins returns the CORS origins, whichever spelling the runtime used.
func (s SharedWebServer) Origins() []SharedWebServerCORSOrigin {
	if len(s.CORS.Origins) > 0 {
		return s.CORS.Origins
	}

	return s.CORS.Origin
}

// Settings returns the general settings block, whichever the runtime
// populated, and a group name so the two shapes stay distinguishable.
func (s SharedWebServer) Settings() (raw json.RawMessage, group string) {
	// A populated block is longer than the two-byte "{}" empty object.
	const emptyObjectLen = 2

	if len(s.CloudGeneralRaw) > emptyObjectLen {
		return s.CloudGeneralRaw, "sharedWebServer.cloudTenant"
	}

	return s.GeneralRaw, "sharedWebServer.general"
}

// WebServers accesses the shared web server objects.
type WebServers struct {
	c *boomi.Client
}

// NewWebServers returns a WebServers service over c.
func NewWebServers(c *boomi.Client) WebServers {
	return WebServers{c: c}
}

// ServerInformation returns the runtime's shared web server basics:
// GET SharedServerInformation/{runtimeId}.
func (w WebServers) ServerInformation(ctx context.Context, runtimeID string) (SharedServerInformation, error) {
	if runtimeID == "" {
		return SharedServerInformation{}, errEmptyID("runtime")
	}

	return getJSON[SharedServerInformation](ctx, w.c, "SharedServerInformation", runtimeID)
}

// Get returns the runtime's full web server configuration:
// GET SharedWebServer/{runtimeId}. A runtime without one answers
// not-found — treat that as "no shared web server", not a failure.
func (w WebServers) Get(ctx context.Context, runtimeID string) (SharedWebServer, error) {
	if runtimeID == "" {
		return SharedWebServer{}, errEmptyID("runtime")
	}

	return getJSON[SharedWebServer](ctx, w.c, "SharedWebServer", runtimeID)
}

// GetRaw returns the configuration undecoded, for callers that need
// fields the typed model does not carry or that will write the payload
// back with UpdateRaw.
func (w WebServers) GetRaw(ctx context.Context, runtimeID string) (json.RawMessage, error) {
	if runtimeID == "" {
		return nil, errEmptyID("runtime")
	}

	return getJSON[json.RawMessage](ctx, w.c, "SharedWebServer", runtimeID)
}

// ErrWebServerWriteUnconfirmed guards shared web server writes: they
// change live authentication and routing for every consumer of the
// runtime's web services.
var ErrWebServerWriteUnconfirmed = errors.New(
	"objects: shared web server writes change live authentication for the runtime's consumers; set Confirmed",
)

// UpdateRaw writes a web server configuration back:
// POST SharedWebServer/{runtimeId}. The payload is raw deliberately — the
// platform's shape varies by runtime (see SharedWebServer) and a typed
// round-trip would drop the fields the type does not carry. Read with
// GetRaw, modify, write back.
func (w WebServers) UpdateRaw(
	ctx context.Context,
	runtimeID string,
	payload json.RawMessage,
	confirmed bool,
) (json.RawMessage, error) {
	if !confirmed {
		return nil, ErrWebServerWriteUnconfirmed
	}

	if runtimeID == "" {
		return nil, errEmptyID("runtime")
	}

	if len(payload) == 0 {
		return nil, errors.New("objects: web server update has an empty payload")
	}

	return postJSON[json.RawMessage](ctx, w.c, payload, "SharedWebServer", runtimeID)
}

// RedactWebServerTokens returns a copy of a SharedWebServer payload with
// every user token replaced by rune-for-rune asterisks, for snapshots and
// logs. Both user-list spellings are handled. The original is not
// modified.
func RedactWebServerTokens(payload json.RawMessage) (json.RawMessage, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(payload, &doc); err != nil {
		return nil, fmt.Errorf("objects: parsing web server payload: %w", err)
	}

	rawUM, ok := doc["userManagement"]
	if !ok {
		return payload, nil
	}

	um, err := redactUserManagement(rawUM)
	if err != nil {
		return nil, err
	}

	doc["userManagement"] = um

	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("objects: rebuilding web server payload: %w", err)
	}

	return out, nil
}

// redactUserManagement redacts the token in every user entry under either
// list spelling.
func redactUserManagement(raw json.RawMessage) (json.RawMessage, error) {
	var um map[string]json.RawMessage
	if err := json.Unmarshal(raw, &um); err != nil {
		return nil, fmt.Errorf("objects: parsing userManagement: %w", err)
	}

	for _, key := range []string{"user", "users"} {
		rawUsers, ok := um[key]
		if !ok {
			continue
		}

		var users []map[string]json.RawMessage
		if err := json.Unmarshal(rawUsers, &users); err != nil {
			continue
		}

		for _, u := range users {
			redactTokenField(u)
		}

		rebuilt, err := json.Marshal(users)
		if err != nil {
			return nil, fmt.Errorf("objects: rebuilding %s list: %w", key, err)
		}

		um[key] = rebuilt
	}

	out, err := json.Marshal(um)
	if err != nil {
		return nil, fmt.Errorf("objects: rebuilding userManagement: %w", err)
	}

	return out, nil
}

// redactTokenField replaces a non-empty "token" value with asterisks of
// the same length.
func redactTokenField(user map[string]json.RawMessage) {
	rawToken, ok := user["token"]
	if !ok {
		return
	}

	var token string
	if json.Unmarshal(rawToken, &token) != nil || token == "" {
		return
	}

	masked := make([]rune, 0, len(token))
	for range token {
		masked = append(masked, '*')
	}

	redacted, err := json.Marshal(string(masked))
	if err != nil {
		return
	}

	user["token"] = redacted
}
