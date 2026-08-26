package objects

import (
	"context"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/internal/query"
)

// License is one purchased-versus-used pair.
type License struct {
	Purchased int `json:"purchased"`
	Used      int `json:"used"`
}

// AccountLicensing is the connection licence position, by connector
// class. Production and test are separate allowances on the platform and
// are separate here.
type AccountLicensing struct {
	Standard           *License `json:"standard"`
	SmallBusiness      *License `json:"smallBusiness"`
	Enterprise         *License `json:"enterprise"`
	TradingPartner     *License `json:"tradingPartner"`
	StandardTest       *License `json:"standardTest"`
	SmallBusinessTest  *License `json:"smallBusinessTest"`
	EnterpriseTest     *License `json:"enterpriseTest"`
	TradingPartnerTest *License `json:"tradingPartnerTest"`
}

// NamedLicense pairs a licence with the class it belongs to.
type NamedLicense struct {
	Name    string
	License License
}

// Connections lists the connection classes in the platform's own order:
// production classes first, then their test counterparts. A class the
// account is not licensed for at all is absent from the payload and is
// skipped — showing it as 0 of 0 would read as a real entitlement.
func (a AccountLicensing) Connections() []NamedLicense {
	pairs := []struct {
		name string
		lic  *License
	}{
		{"Standard", a.Standard},
		{"Small business", a.SmallBusiness},
		{"Enterprise", a.Enterprise},
		{"Trading partner", a.TradingPartner},
		{"Standard (test)", a.StandardTest},
		{"Small business (test)", a.SmallBusinessTest},
		{"Enterprise (test)", a.EnterpriseTest},
		{"Trading partner (test)", a.TradingPartnerTest},
	}

	out := make([]NamedLicense, 0, len(pairs))

	for _, p := range pairs {
		if p.lic == nil {
			continue
		}

		out = append(out, NamedLicense{Name: p.name, License: *p.lic})
	}

	return out
}

// Account is the account itself: what it is licensed for and what state
// it is in. Licensing counts connections by class; Molecule and Cloud
// count runtime allowances.
type Account struct {
	AccountID      string           `json:"accountId"`
	Name           string           `json:"name"`
	Status         string           `json:"status"`
	DateCreated    string           `json:"dateCreated"`
	ExpirationDate string           `json:"expirationDate"`
	SupportLevel   string           `json:"supportLevel"`
	SupportAccess  bool             `json:"supportAccess"`
	OverDeployed   bool             `json:"overDeployed"`
	Licensing      AccountLicensing `json:"licensing"`
	Molecule       *License         `json:"molecule"`
	Cloud          *License         `json:"cloud"`
}

// AccountUserRole is one person's role assignment on the account. Query
// only — there is no GET, and the id is a composite.
type AccountUserRole struct {
	ID         string `json:"id"`
	AccountID  string `json:"accountId"`
	UserID     string `json:"userId"`
	RoleID     string `json:"roleId"`
	FirstName  string `json:"firstName"`
	LastName   string `json:"lastName"`
	NotifyUser bool   `json:"notifyUser"`
}

// Role is one role and the privileges it grants.
type Role struct {
	ID          string `json:"id"`
	AccountID   string `json:"accountId"`
	Name        string `json:"name"`
	ParentID    string `json:"parentId"`
	Description string `json:"Description"`
	Privileges  *struct {
		Privilege []struct {
			Name string `json:"name"`
		} `json:"Privilege"`
	} `json:"Privileges"`
}

// PrivilegeNames flattens the nested privilege list.
func (r Role) PrivilegeNames() []string {
	if r.Privileges == nil {
		return nil
	}

	out := make([]string, 0, len(r.Privileges.Privilege))

	for _, p := range r.Privileges.Privilege {
		if p.Name != "" {
			out = append(out, p.Name)
		}
	}

	return out
}

// Accounts accesses account-level objects: the account itself, its role
// assignments, and its roles.
type Accounts struct {
	c *boomi.Client
}

// NewAccounts returns an Accounts service over c.
func NewAccounts(c *boomi.Client) Accounts {
	return Accounts{c: c}
}

// Get fetches the client's account: GET Account/{accountId} — licence
// position, status and expiry.
func (a Accounts) Get(ctx context.Context) (Account, error) {
	return getJSON[Account](ctx, a.c, "Account", a.c.AccountID())
}

// UserRoles lists every role assignment on the account — who can log in,
// and as what.
func (a Accounts) UserRoles(ctx context.Context) ([]AccountUserRole, error) {
	return QueryAll[AccountUserRole](
		ctx, a.c, "AccountUserRole", mustFilter(query.Eq("accountId", a.c.AccountID())),
	)
}

// Roles lists the account's roles and the privileges each grants.
func (a Accounts) Roles(ctx context.Context) ([]Role, error) {
	return QueryAll[Role](ctx, a.c, "Role", nil)
}
