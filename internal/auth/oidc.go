package auth

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCOptions configures the single OpenID Connect provider.
type OIDCOptions struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// OIDCProvider performs the authorization-code flow and returns the verified
// claims of an ID token. Verification — signature, issuer, audience, expiry —
// is go-oidc's; this type adds the nonce comparison and claim extraction.
type OIDCProvider struct {
	config   oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// NewOIDCProvider discovers the issuer's endpoints and JWKS via OIDC
// discovery and builds a provider ready to run the authorization-code flow.
func NewOIDCProvider(ctx context.Context, options OIDCOptions) (*OIDCProvider, error) {
	provider, err := oidc.NewProvider(ctx, options.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover oidc provider: %w", err)
	}
	return &OIDCProvider{
		config: oauth2.Config{
			ClientID:     options.ClientID,
			ClientSecret: options.ClientSecret,
			RedirectURL:  options.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: options.ClientID}),
	}, nil
}

// AuthCodeURL builds the authorization request URL for one sign-in attempt,
// carrying its state, nonce, and PKCE code challenge.
func (p *OIDCProvider) AuthCodeURL(state, nonce, challenge string) string {
	return p.config.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
}

// Exchange trades an authorization code for verified claims. It fails unless
// the ID token's nonce equals the one minted for this flow, which is what
// makes a stolen or replayed token useless.
func (p *OIDCProvider) Exchange(ctx context.Context, code, verifier, nonce string) (IdentityClaims, error) {
	token, err := p.config.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", verifier))
	if err != nil {
		return IdentityClaims{}, fmt.Errorf("exchange code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return IdentityClaims{}, fmt.Errorf("token response carried no id_token")
	}
	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return IdentityClaims{}, fmt.Errorf("verify id token: %w", err)
	}
	if idToken.Nonce != nonce {
		return IdentityClaims{}, fmt.Errorf("id token nonce does not match this sign-in")
	}
	var payload struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		HostedDomain  string `json:"hd"`
		Name          string `json:"name"`
	}
	if err := idToken.Claims(&payload); err != nil {
		return IdentityClaims{}, fmt.Errorf("decode id token claims: %w", err)
	}
	return IdentityClaims{
		Subject:       idToken.Subject,
		Email:         payload.Email,
		EmailVerified: payload.EmailVerified,
		HostedDomain:  payload.HostedDomain,
		Name:          payload.Name,
	}, nil
}
