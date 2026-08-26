package objects

import (
	"context"
	"strconv"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/internal/query"
)

// DeployedCertificate is one deployed X.509 certificate, from
// DeployedExpiredCertificate/query.
type DeployedCertificate struct {
	AccountID       string `json:"accountId"`
	CertificateName string `json:"certificateName"`
	CertificateID   string `json:"certificateId"`
	CertificateType string `json:"certificateType"`
	ContainerID     string `json:"containerId"`
	ContainerName   string `json:"containerName"`
	EnvironmentName string `json:"environmentName"`
	EnvironmentID   string `json:"environmentId"`
	Location        string `json:"location"`
	ExpirationDate  string `json:"expirationDate"`
}

// certificateInventoryDays is the boundary passed when the caller wants
// everything: ~100 years, far beyond any certificate lifetime.
const certificateInventoryDays = 36500

// Certificates queries deployed certificate expiry.
type Certificates struct {
	c *boomi.Client
}

// NewCertificates returns a Certificates service over c.
func NewCertificates(c *boomi.Client) Certificates {
	return Certificates{c: c}
}

// ExpiringWithin lists deployed certificates expiring within days.
//
// Omitting an expirationBoundary makes the platform silently apply
// LESS_THAN 30, which under-reports badly — so the boundary is always
// sent. days <= 0 selects a boundary wide enough to inventory everything.
func (c Certificates) ExpiringWithin(ctx context.Context, days int) ([]DeployedCertificate, error) {
	if days <= 0 {
		days = certificateInventoryDays
	}

	return QueryAll[DeployedCertificate](ctx, c.c, "DeployedExpiredCertificate",
		mustFilter(query.Lt("expirationBoundary", strconv.Itoa(days))))
}
