package auth

import (
	"errors"
	"fmt"
	"strings"
)

// ErrIdentityRejected reports an identity the domain policy refuses. It is
// deliberately one error for every reason: the sign-in page must not tell a
// caller which check failed.
var ErrIdentityRejected = errors.New("identity rejected")

// IdentityClaims is the subset of an OIDC ID token faultline acts on.
// HostedDomain is Google's `hd` claim, present only for accounts belonging to
// a Workspace organization — a personal account carries none.
type IdentityClaims struct {
	Subject       string
	Email         string
	EmailVerified bool
	HostedDomain  string
	Name          string
}

// AllowedDomains is an exact-match allowlist of Workspace hosted domains.
// Exact match, not suffix match: "evil-example.com" ends with "example.com",
// and a subdomain is a different organization's namespace. Permit normalizes
// each entry the same way it normalizes the incoming claim, so the match is
// exact regardless of case or surrounding whitespace in how the allowlist was
// constructed.
type AllowedDomains []string

// Permit reports whether claims may sign in. The zero value permits nobody.
func (d AllowedDomains) Permit(claims IdentityClaims) error {
	if claims.Subject == "" {
		return fmt.Errorf("%w: no subject", ErrIdentityRejected)
	}
	if claims.Email == "" {
		return fmt.Errorf("%w: no email", ErrIdentityRejected)
	}
	if !claims.EmailVerified {
		return fmt.Errorf("%w: email not verified", ErrIdentityRejected)
	}
	hostedDomain := strings.ToLower(strings.TrimSpace(claims.HostedDomain))
	if hostedDomain == "" {
		return fmt.Errorf("%w: no hosted domain", ErrIdentityRejected)
	}
	for _, domain := range d {
		if hostedDomain == strings.ToLower(strings.TrimSpace(domain)) {
			return nil
		}
	}
	return fmt.Errorf("%w: hosted domain not allowed", ErrIdentityRejected)
}
