package auth_test

import (
	"errors"
	"testing"

	"github.com/ramilS/faultline/internal/auth"
)

func TestAllowedDomainsPermit(t *testing.T) {
	allowed := auth.AllowedDomains{"example.com", "second.example", "  Mixed-Case.example  "}
	valid := auth.IdentityClaims{
		Subject:       "sub-1",
		Email:         "person@example.com",
		EmailVerified: true,
		HostedDomain:  "example.com",
		Name:          "Person",
	}

	mutate := func(change func(*auth.IdentityClaims)) auth.IdentityClaims {
		claims := valid
		change(&claims)
		return claims
	}

	cases := []struct {
		name    string
		claims  auth.IdentityClaims
		allowed bool
	}{
		{"exact match", valid, true},
		{"second domain", mutate(func(c *auth.IdentityClaims) {
			c.HostedDomain, c.Email = "second.example", "person@second.example"
		}), true},
		{"uppercase hd", mutate(func(c *auth.IdentityClaims) { c.HostedDomain = "Example.COM" }), true},
		{"mixed-case whitespace allowlist entry", mutate(func(c *auth.IdentityClaims) {
			c.HostedDomain, c.Email = "mixed-case.example", "person@mixed-case.example"
		}), true},
		{"missing hd", mutate(func(c *auth.IdentityClaims) { c.HostedDomain = "" }), false},
		{"foreign hd", mutate(func(c *auth.IdentityClaims) { c.HostedDomain = "other.example" }), false},
		{"suffix lookalike", mutate(func(c *auth.IdentityClaims) { c.HostedDomain = "evil-example.com" }), false},
		{"subdomain", mutate(func(c *auth.IdentityClaims) { c.HostedDomain = "sub.example.com" }), false},
		{"unverified email", mutate(func(c *auth.IdentityClaims) { c.EmailVerified = false }), false},
		{"missing subject", mutate(func(c *auth.IdentityClaims) { c.Subject = "" }), false},
		{"missing email", mutate(func(c *auth.IdentityClaims) { c.Email = "" }), false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := allowed.Permit(testCase.claims)
			if testCase.allowed && err != nil {
				t.Fatalf("Permit() error = %v, want nil", err)
			}
			if !testCase.allowed && !errors.Is(err, auth.ErrIdentityRejected) {
				t.Fatalf("Permit() error = %v, want ErrIdentityRejected", err)
			}
		})
	}
}

// TestAllowlistWithAnEmptyEntryStillRejectsAnAbsentHostedDomain makes
// Permit's explicit hd-presence check load-bearing. Every other rejection
// case above also fails through the allowlist loop, so deleting that branch
// left the whole suite green — and the branch is the only thing keeping
// personal Google accounts out, since they carry no `hd` at all. An empty
// allowlist entry would then compare equal to the absent claim and admit
// them. Config filters empty entries out, so this is unreachable today; it
// is a guard on the type's own guarantee, not on the loader's.
func TestAllowlistWithAnEmptyEntryStillRejectsAnAbsentHostedDomain(t *testing.T) {
	err := auth.AllowedDomains{""}.Permit(auth.IdentityClaims{
		Subject:       "sub-1",
		Email:         "person@gmail.com",
		EmailVerified: true,
	})
	if !errors.Is(err, auth.ErrIdentityRejected) {
		t.Fatalf("Permit() error = %v, want ErrIdentityRejected — a personal account has no hd and must never match", err)
	}
}

func TestEmptyAllowedDomainsPermitsNobody(t *testing.T) {
	err := auth.AllowedDomains(nil).Permit(auth.IdentityClaims{
		Subject: "sub-1", Email: "person@example.com", EmailVerified: true, HostedDomain: "example.com",
	})
	if !errors.Is(err, auth.ErrIdentityRejected) {
		t.Fatalf("Permit() error = %v, want ErrIdentityRejected", err)
	}
}
