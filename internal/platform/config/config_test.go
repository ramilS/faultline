package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLoadUsesDatabaseDefaults(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("DATABASE_URL", "postgres://faultline:secret@localhost/faultline")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got.Address != ":8080" {
		t.Fatalf("Address = %q, want :8080", got.Address)
	}
	want := DatabaseConfig{
		URL:                "postgres://faultline:secret@localhost/faultline",
		MaxConns:           10,
		MinConns:           0,
		MaxConnLifetime:    time.Hour,
		MaxConnIdleTime:    30 * time.Minute,
		HealthCheckPeriod:  time.Minute,
		ArtifactQuotaBytes: 1 << 30,
	}
	if got.Database != want {
		t.Fatalf("Database = %#v, want %#v", got.Database, want)
	}
}

func TestLoadUsesConfiguredValues(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("FAULTLINE_ADDR", "127.0.0.1:9090")
	t.Setenv("DATABASE_URL", "postgres://db/faultline")
	t.Setenv("DATABASE_MAX_CONNS", "24")
	t.Setenv("DATABASE_MIN_CONNS", "3")
	t.Setenv("DATABASE_MAX_CONN_LIFETIME", "45m")
	t.Setenv("DATABASE_MAX_CONN_IDLE_TIME", "10m")
	t.Setenv("DATABASE_HEALTH_CHECK_PERIOD", "15s")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got.Address != "127.0.0.1:9090" {
		t.Fatalf("Address = %q", got.Address)
	}
	if got.Database.MaxConns != 24 || got.Database.MinConns != 3 {
		t.Fatalf("connection bounds = %d/%d", got.Database.MinConns, got.Database.MaxConns)
	}
	if got.Database.MaxConnLifetime != 45*time.Minute || got.Database.MaxConnIdleTime != 10*time.Minute || got.Database.HealthCheckPeriod != 15*time.Second {
		t.Fatalf("durations = %#v", got.Database)
	}
}

func TestLoadCookieSecureDefaultsToTrue(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("DATABASE_URL", "postgres://db/faultline")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !got.CookieSecure {
		t.Fatal("CookieSecure = false, want true")
	}
}

func TestLoadCookieSecureUsesExplicitBoolean(t *testing.T) {
	for _, value := range []string{"true", "false"} {
		t.Run(value, func(t *testing.T) {
			clearEnvironment(t)
			t.Setenv("DATABASE_URL", "postgres://db/faultline")
			t.Setenv("FAULTLINE_COOKIE_SECURE", value)

			got, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			want := value == "true"
			if got.CookieSecure != want {
				t.Fatalf("CookieSecure = %v, want %v", got.CookieSecure, want)
			}
		})
	}
}

func TestLoadCookieSecureRejectsInvalidBoolean(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("DATABASE_URL", "postgres://db/faultline")
	t.Setenv("FAULTLINE_COOKIE_SECURE", "yes")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "FAULTLINE_COOKIE_SECURE must be a boolean") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestLoadRejectsInvalidDatabaseConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		wantErr string
	}{
		{name: "missing URL", wantErr: "DATABASE_URL is required"},
		{name: "bad maximum", key: "DATABASE_MAX_CONNS", value: "many", wantErr: "DATABASE_MAX_CONNS"},
		{name: "non-positive maximum", key: "DATABASE_MAX_CONNS", value: "0", wantErr: "must be greater than zero"},
		{name: "negative minimum", key: "DATABASE_MIN_CONNS", value: "-1", wantErr: "must not be negative"},
		{name: "bad lifetime", key: "DATABASE_MAX_CONN_LIFETIME", value: "later", wantErr: "DATABASE_MAX_CONN_LIFETIME"},
		{name: "bad idle time", key: "DATABASE_MAX_CONN_IDLE_TIME", value: "later", wantErr: "DATABASE_MAX_CONN_IDLE_TIME"},
		{name: "bad health period", key: "DATABASE_HEALTH_CHECK_PERIOD", value: "later", wantErr: "DATABASE_HEALTH_CHECK_PERIOD"},
		{name: "zero health period", key: "DATABASE_HEALTH_CHECK_PERIOD", value: "0s", wantErr: "must be greater than zero"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnvironment(t)
			if tt.name != "missing URL" {
				t.Setenv("DATABASE_URL", "postgres://db/faultline")
			}
			if tt.key != "" {
				t.Setenv(tt.key, tt.value)
			}

			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadRejectsMinimumAboveMaximum(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("DATABASE_URL", "postgres://db/faultline")
	t.Setenv("DATABASE_MAX_CONNS", "2")
	t.Setenv("DATABASE_MIN_CONNS", "3")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "must not exceed") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestLoadDurabilityDefaults(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("DATABASE_URL", "postgres://x")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.Durability
	if d.SpillEnabled {
		t.Fatal("spill should default to disabled")
	}
	if d.SpillDir != "/var/lib/faultline/spill" || d.SpillSegmentBytes != 16777216 ||
		d.SpillSegmentMaxAge != 2*time.Second || d.SpillFsyncInterval != 50*time.Millisecond ||
		d.SpillMaxBytes != 2147483648 {
		t.Fatalf("unexpected durability defaults: %+v", d)
	}
}

func TestLoadDurabilityFromEnv(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("FAULTLINE_SPILL_ENABLED", "true")
	t.Setenv("FAULTLINE_SPILL_SEGMENT_MAX_AGE", "5s")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Durability.SpillEnabled || cfg.Durability.SpillSegmentMaxAge != 5*time.Second {
		t.Fatalf("env not applied: %+v", cfg.Durability)
	}
}

func TestLoadSSODisabledByDefault(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/faultline")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SSO.Enabled() {
		t.Fatal("SSO must be disabled when FAULTLINE_OIDC_CLIENT_ID is unset")
	}
}

func TestLoadSSORejectsEmptyDomainAllowlist(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/faultline")
	t.Setenv("FAULTLINE_OIDC_CLIENT_ID", "client")
	t.Setenv("FAULTLINE_OIDC_CLIENT_SECRET", "secret")
	t.Setenv("FAULTLINE_OIDC_ISSUER", "https://accounts.google.com")
	t.Setenv("FAULTLINE_OIDC_REDIRECT_URL", "https://faultline.example/api/v1/auth/sso/callback")
	t.Setenv("FAULTLINE_OIDC_ALLOWED_HD", "")

	if _, err := Load(); err == nil {
		t.Fatal("Load() must fail when SSO is configured with no allowed domain")
	}
}

func TestLoadSSONormalizesDomains(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/faultline")
	t.Setenv("FAULTLINE_OIDC_CLIENT_ID", "client")
	t.Setenv("FAULTLINE_OIDC_CLIENT_SECRET", "secret")
	t.Setenv("FAULTLINE_OIDC_ISSUER", "https://accounts.google.com")
	t.Setenv("FAULTLINE_OIDC_REDIRECT_URL", "https://faultline.example/api/v1/auth/sso/callback")
	t.Setenv("FAULTLINE_OIDC_ALLOWED_HD", " Example.COM , second.example ")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := []string{"example.com", "second.example"}
	if !slices.Equal(cfg.SSO.AllowedDomains, want) {
		t.Fatalf("AllowedDomains = %v, want %v", cfg.SSO.AllowedDomains, want)
	}
}

func TestLoadSSORequiresEverySetting(t *testing.T) {
	for _, missing := range []string{
		"FAULTLINE_OIDC_CLIENT_SECRET",
		"FAULTLINE_OIDC_ISSUER",
		"FAULTLINE_OIDC_REDIRECT_URL",
	} {
		t.Run(missing, func(t *testing.T) {
			clearEnvironment(t)
			t.Setenv("DATABASE_URL", "postgres://localhost/faultline")
			t.Setenv("FAULTLINE_OIDC_CLIENT_ID", "client")
			t.Setenv("FAULTLINE_OIDC_CLIENT_SECRET", "secret")
			t.Setenv("FAULTLINE_OIDC_ISSUER", "https://accounts.google.com")
			t.Setenv("FAULTLINE_OIDC_REDIRECT_URL", "https://faultline.example/cb")
			t.Setenv("FAULTLINE_OIDC_ALLOWED_HD", "example.com")
			t.Setenv(missing, "")

			if _, err := Load(); err == nil {
				t.Fatalf("Load() must fail when %s is empty", missing)
			}
		})
	}
}

func TestLoadSSORejectsPlaintextURLs(t *testing.T) {
	cases := map[string]string{
		"FAULTLINE_OIDC_ISSUER":       "http://accounts.example.com",
		"FAULTLINE_OIDC_REDIRECT_URL": "http://faultline.example/cb",
	}
	for name, plaintext := range cases {
		t.Run(name, func(t *testing.T) {
			clearEnvironment(t)
			t.Setenv("DATABASE_URL", "postgres://localhost/faultline")
			t.Setenv("FAULTLINE_OIDC_CLIENT_ID", "client")
			t.Setenv("FAULTLINE_OIDC_CLIENT_SECRET", "secret")
			t.Setenv("FAULTLINE_OIDC_ISSUER", "https://accounts.google.com")
			t.Setenv("FAULTLINE_OIDC_REDIRECT_URL", "https://faultline.example/cb")
			t.Setenv("FAULTLINE_OIDC_ALLOWED_HD", "example.com")
			t.Setenv(name, plaintext)

			if _, err := Load(); err == nil {
				t.Fatalf("Load() accepted a plaintext %s", name)
			}
		})
	}
}

func TestLoadSSOPermitsLoopbackOverHTTP(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/faultline")
	t.Setenv("FAULTLINE_OIDC_CLIENT_ID", "client")
	t.Setenv("FAULTLINE_OIDC_CLIENT_SECRET", "secret")
	t.Setenv("FAULTLINE_OIDC_ISSUER", "http://localhost:8081/realms/faultline")
	t.Setenv("FAULTLINE_OIDC_REDIRECT_URL", "http://localhost:8080/api/v1/auth/sso/callback")
	t.Setenv("FAULTLINE_OIDC_ALLOWED_HD", "example.com")

	if _, err := Load(); err != nil {
		t.Fatalf("Load() rejected a loopback development setup: %v", err)
	}
}

func clearEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"FAULTLINE_ADDR",
		"FAULTLINE_COOKIE_SECURE",
		"DATABASE_URL",
		"DATABASE_MAX_CONNS",
		"DATABASE_MIN_CONNS",
		"DATABASE_MAX_CONN_LIFETIME",
		"DATABASE_MAX_CONN_IDLE_TIME",
		"DATABASE_HEALTH_CHECK_PERIOD",
		"FAULTLINE_SPILL_ENABLED",
		"SPILL_DIR",
		"FAULTLINE_SPILL_SEGMENT_BYTES",
		"FAULTLINE_SPILL_SEGMENT_MAX_AGE",
		"FAULTLINE_SPILL_FSYNC_INTERVAL",
		"FAULTLINE_SPILL_MAX_BYTES",
		"FAULTLINE_OIDC_CLIENT_ID",
		"FAULTLINE_OIDC_CLIENT_SECRET",
		"FAULTLINE_OIDC_ISSUER",
		"FAULTLINE_OIDC_REDIRECT_URL",
		"FAULTLINE_OIDC_ALLOWED_HD",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadArtifactQuotaFromEnv(t *testing.T) {
	cases := map[string]struct {
		value   string
		want    int64
		wantErr bool
	}{
		"explicit":      {"5000", 5000, false},
		"zero disables": {"0", 0, false},
		"negative":      {"-1", 0, true},
		"not a number":  {"lots", 0, true},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			clearEnvironment(t)
			t.Setenv("DATABASE_URL", "postgres://db/faultline")
			t.Setenv("FAULTLINE_ARTIFACT_QUOTA_BYTES", testCase.value)

			got, err := Load()

			if (err != nil) != testCase.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, testCase.wantErr)
			}
			if err == nil && got.Database.ArtifactQuotaBytes != testCase.want {
				t.Fatalf("ArtifactQuotaBytes = %d, want %d", got.Database.ArtifactQuotaBytes, testCase.want)
			}
		})
	}
}
