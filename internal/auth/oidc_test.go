package auth_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/auth"
)

type testIdP struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	claims map[string]any
}

func newTestIdP(t *testing.T, claims map[string]any) *testIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	idp := &testIdP{key: key, claims: claims}
	mux := http.NewServeMux()
	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                idp.server.URL,
			"authorization_endpoint":                idp.server.URL + "/auth",
			"token_endpoint":                        idp.server.URL + "/token",
			"jwks_uri":                              idp.server.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"keys": []map[string]any{{
			"kty": "RSA",
			"alg": "RS256",
			"use": "sig",
			"kid": "test-key",
			"n":   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"access_token": "access",
			"token_type":   "Bearer",
			"id_token":     idp.signIDToken(t),
		})
	})
	return idp
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (idp *testIdP) signIDToken(t *testing.T) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "kid": "test-key", "typ": "JWT"}
	claims := map[string]any{
		"iss": idp.server.URL,
		"aud": "client",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	for key, value := range idp.claims {
		claims[key] = value
	}

	segment := func(value any) string {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal jwt segment: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(encoded)
	}
	signingInput := segment(header) + "." + segment(claims)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, idp.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign id token: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func newProvider(t *testing.T, idp *testIdP) *auth.OIDCProvider {
	t.Helper()
	provider, err := auth.NewOIDCProvider(context.Background(), auth.OIDCOptions{
		Issuer:       idp.server.URL,
		ClientID:     "client",
		ClientSecret: "secret",
		RedirectURL:  "https://faultline.example/cb",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider() error = %v", err)
	}
	return provider
}

func TestExchangeReturnsClaims(t *testing.T) {
	idp := newTestIdP(t, map[string]any{
		"sub": "sub-1", "email": "person@example.com", "email_verified": true,
		"hd": "example.com", "name": "Person", "nonce": "nonce-1",
	})
	provider := newProvider(t, idp)

	claims, err := provider.Exchange(context.Background(), "code", "verifier", "nonce-1")
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	if claims.Subject != "sub-1" || claims.HostedDomain != "example.com" || !claims.EmailVerified {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestExchangeRejectsMismatchedNonce(t *testing.T) {
	idp := newTestIdP(t, map[string]any{
		"sub": "sub-1", "email": "person@example.com", "email_verified": true,
		"hd": "example.com", "nonce": "attacker-nonce",
	})
	provider := newProvider(t, idp)

	if _, err := provider.Exchange(context.Background(), "code", "verifier", "nonce-1"); err == nil {
		t.Fatal("Exchange() accepted a replayed token with the wrong nonce")
	}
}

func TestAuthCodeURLCarriesPKCEAndNonce(t *testing.T) {
	idp := newTestIdP(t, map[string]any{"sub": "sub-1"})
	provider := newProvider(t, idp)

	url := provider.AuthCodeURL("state-1", "nonce-1", "challenge-1")
	for _, want := range []string{"state=state-1", "nonce=nonce-1", "code_challenge=challenge-1", "code_challenge_method=S256"} {
		if !strings.Contains(url, want) {
			t.Fatalf("AuthCodeURL() = %q, missing %q", url, want)
		}
	}
}
