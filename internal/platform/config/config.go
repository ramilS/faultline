// Package config loads and validates process configuration from the environment.
package config

import (
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAddress            = ":8080"
	defaultMaxConns           = int32(10)
	defaultMinConns           = int32(0)
	defaultMaxConnLifetime    = time.Hour
	defaultMaxConnIdleTime    = 30 * time.Minute
	defaultHealthCheckPeriod  = time.Minute
	defaultSpillDir           = "/var/lib/faultline/spill"
	defaultSpillSegmentBytes  = int64(16 << 20)
	defaultSpillSegmentMaxAge = 2 * time.Second
	defaultSpillFsyncInterval = 50 * time.Millisecond
	defaultSpillMaxBytes      = int64(2 << 30)
	defaultArtifactQuotaBytes = int64(1 << 30)
	// defaultDebugFileRetention keeps 30 mappings and 30 native debug files a
	// project: at a mobile app's ~7MB stored mapping and ~12MB dSYM that is
	// about half of the default quota.
	defaultDebugFileRetention = 30
)

// DatabaseConfig controls PostgreSQL connectivity and pool lifecycle.
type DatabaseConfig struct {
	URL               string
	MaxConns          int32
	MinConns          int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
	// ArtifactQuotaBytes caps the stored (compressed) size of the source maps and
	// ProGuard/R8 mappings one project may store. Zero means no cap.
	ArtifactQuotaBytes int64
	// DebugFileRetention is how many of its most recently used ProGuard/R8
	// mappings, and separately native debug files, a project keeps; older ones
	// are deleted as new ones arrive. Zero keeps everything.
	DebugFileRetention int
}

// Validate reports whether database holds the invariants Load already
// enforces on the environment-sourced path. Load is not the only way a
// DatabaseConfig is built — tests and other callers construct one directly —
// so a caller that opens a pool from an arbitrary DatabaseConfig must check
// this itself rather than assume Load already ran. In particular, a
// non-positive HealthCheckPeriod reaches pgxpool's background health-check
// goroutine, where time.NewTicker panics: that panic surfaces asynchronously,
// after pool construction has already returned success, so it must be caught
// here instead.
func (database DatabaseConfig) Validate() error {
	if database.URL == "" {
		return fmt.Errorf("database URL is required")
	}
	if database.MaxConns <= 0 {
		return fmt.Errorf("database max conns must be greater than zero")
	}
	if database.MinConns < 0 {
		return fmt.Errorf("database min conns must not be negative")
	}
	if database.MinConns > database.MaxConns {
		return fmt.Errorf("database min conns must not exceed max conns")
	}
	if database.HealthCheckPeriod <= 0 {
		return fmt.Errorf("database health check period must be greater than zero")
	}
	if database.ArtifactQuotaBytes < 0 {
		return fmt.Errorf("artifact quota must not be negative")
	}
	if database.DebugFileRetention < 0 {
		return fmt.Errorf("debug file retention must not be negative")
	}
	return nil
}

// DurabilityConfig controls the optional pre-ack spill write-ahead log.
type DurabilityConfig struct {
	SpillEnabled       bool
	SpillDir           string
	SpillSegmentBytes  int64
	SpillSegmentMaxAge time.Duration
	SpillFsyncInterval time.Duration
	SpillMaxBytes      int64
}

// SSOConfig controls optional OpenID Connect sign-in. SSO is off unless
// ClientID is set; when it is set, every other field is required, and an
// empty AllowedDomains is a startup error rather than "admit everyone".
type SSOConfig struct {
	Issuer         string
	ClientID       string
	ClientSecret   string
	RedirectURL    string
	AllowedDomains []string
}

// Enabled reports whether an OIDC provider is configured.
func (c SSOConfig) Enabled() bool { return c.ClientID != "" }

// Config contains process-level configuration shared during application setup.
type Config struct {
	Address      string
	CookieSecure bool
	// PublicURL is the dashboard's external base URL, used to link to issues
	// from notifications. Empty means notifications carry no link.
	PublicURL string
	// WebhookAllowPrivate lets notification webhooks reach private network
	// addresses. Off by default: the server can reach what an outside caller
	// cannot, so an owner-supplied URL must not open that by default.
	WebhookAllowPrivate bool
	Database            DatabaseConfig
	Durability          DurabilityConfig
	SSO                 SSOConfig
}

// Load reads configuration from the environment and rejects invalid values.
func Load() (Config, error) {
	address := os.Getenv("FAULTLINE_ADDR")
	if address == "" {
		address = defaultAddress
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	cookieSecure, err := boolValue("FAULTLINE_COOKIE_SECURE", true)
	if err != nil {
		return Config{}, err
	}

	maxConns, err := int32Value("DATABASE_MAX_CONNS", defaultMaxConns)
	if err != nil {
		return Config{}, err
	}
	if maxConns <= 0 {
		return Config{}, fmt.Errorf("DATABASE_MAX_CONNS must be greater than zero")
	}
	minConns, err := int32Value("DATABASE_MIN_CONNS", defaultMinConns)
	if err != nil {
		return Config{}, err
	}
	if minConns < 0 {
		return Config{}, fmt.Errorf("DATABASE_MIN_CONNS must not be negative")
	}
	if minConns > maxConns {
		return Config{}, fmt.Errorf("DATABASE_MIN_CONNS must not exceed DATABASE_MAX_CONNS")
	}

	maxLifetime, err := durationValue("DATABASE_MAX_CONN_LIFETIME", defaultMaxConnLifetime)
	if err != nil {
		return Config{}, err
	}
	maxIdleTime, err := durationValue("DATABASE_MAX_CONN_IDLE_TIME", defaultMaxConnIdleTime)
	if err != nil {
		return Config{}, err
	}
	healthPeriod, err := durationValue("DATABASE_HEALTH_CHECK_PERIOD", defaultHealthCheckPeriod)
	if err != nil {
		return Config{}, err
	}
	if healthPeriod == 0 {
		return Config{}, fmt.Errorf("DATABASE_HEALTH_CHECK_PERIOD must be greater than zero")
	}

	artifactQuota, err := int64Value("FAULTLINE_ARTIFACT_QUOTA_BYTES", defaultArtifactQuotaBytes)
	if err != nil {
		return Config{}, err
	}
	if artifactQuota < 0 {
		return Config{}, fmt.Errorf("FAULTLINE_ARTIFACT_QUOTA_BYTES must not be negative")
	}
	debugFileRetention, err := int64Value("FAULTLINE_DEBUG_FILE_RETENTION", defaultDebugFileRetention)
	if err != nil {
		return Config{}, err
	}
	if debugFileRetention < 0 || debugFileRetention > math.MaxInt32 {
		return Config{}, fmt.Errorf("FAULTLINE_DEBUG_FILE_RETENTION must be between 0 and %d", math.MaxInt32)
	}

	spillEnabled, err := boolValue("FAULTLINE_SPILL_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	spillDir := os.Getenv("SPILL_DIR")
	if spillDir == "" {
		spillDir = defaultSpillDir
	}
	segmentBytes, err := int64Value("FAULTLINE_SPILL_SEGMENT_BYTES", defaultSpillSegmentBytes)
	if err != nil {
		return Config{}, err
	}
	segmentMaxAge, err := durationValue("FAULTLINE_SPILL_SEGMENT_MAX_AGE", defaultSpillSegmentMaxAge)
	if err != nil {
		return Config{}, err
	}
	fsyncInterval, err := durationValue("FAULTLINE_SPILL_FSYNC_INTERVAL", defaultSpillFsyncInterval)
	if err != nil {
		return Config{}, err
	}
	maxBytes, err := int64Value("FAULTLINE_SPILL_MAX_BYTES", defaultSpillMaxBytes)
	if err != nil {
		return Config{}, err
	}
	if spillEnabled {
		if segmentBytes <= 0 || maxBytes <= 0 || segmentMaxAge <= 0 || fsyncInterval <= 0 {
			return Config{}, fmt.Errorf("spill sizes and intervals must be positive when spill is enabled")
		}
		if maxBytes < segmentBytes {
			return Config{}, fmt.Errorf("FAULTLINE_SPILL_MAX_BYTES must be >= FAULTLINE_SPILL_SEGMENT_BYTES")
		}
	}

	sso, err := loadSSO()
	if err != nil {
		return Config{}, err
	}

	publicURL, err := loadPublicURL()
	if err != nil {
		return Config{}, err
	}
	webhookAllowPrivate, err := boolValue("FAULTLINE_WEBHOOK_ALLOW_PRIVATE", false)
	if err != nil {
		return Config{}, err
	}

	return Config{
		Address:             address,
		CookieSecure:        cookieSecure,
		PublicURL:           publicURL,
		WebhookAllowPrivate: webhookAllowPrivate,
		Database: DatabaseConfig{
			URL:                databaseURL,
			MaxConns:           maxConns,
			MinConns:           minConns,
			MaxConnLifetime:    maxLifetime,
			MaxConnIdleTime:    maxIdleTime,
			HealthCheckPeriod:  healthPeriod,
			ArtifactQuotaBytes: artifactQuota,
			DebugFileRetention: int(debugFileRetention),
		},
		Durability: DurabilityConfig{
			SpillEnabled:       spillEnabled,
			SpillDir:           spillDir,
			SpillSegmentBytes:  segmentBytes,
			SpillSegmentMaxAge: segmentMaxAge,
			SpillFsyncInterval: fsyncInterval,
			SpillMaxBytes:      maxBytes,
		},
		SSO: sso,
	}, nil
}

// loadPublicURL reads the optional FAULTLINE_PUBLIC_URL, without a trailing
// slash. It must be an absolute http(s) URL: it is pasted into links people
// click from a chat, so a relative or schemeless value would produce links
// that go nowhere.
func loadPublicURL() (string, error) {
	raw := strings.TrimRight(strings.TrimSpace(os.Getenv("FAULTLINE_PUBLIC_URL")), "/")
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return "", fmt.Errorf("FAULTLINE_PUBLIC_URL must be an absolute http or https URL")
	}
	return raw, nil
}

// loadSSO reads optional OIDC settings from the environment. It returns a
// zero SSOConfig (Enabled() == false) when FAULTLINE_OIDC_CLIENT_ID is unset,
// and an error if SSO is configured but any required setting is missing.
func loadSSO() (SSOConfig, error) {
	clientID := strings.TrimSpace(os.Getenv("FAULTLINE_OIDC_CLIENT_ID"))
	if clientID == "" {
		return SSOConfig{}, nil
	}
	sso := SSOConfig{
		Issuer:       strings.TrimSpace(os.Getenv("FAULTLINE_OIDC_ISSUER")),
		ClientID:     clientID,
		ClientSecret: os.Getenv("FAULTLINE_OIDC_CLIENT_SECRET"),
		RedirectURL:  strings.TrimSpace(os.Getenv("FAULTLINE_OIDC_REDIRECT_URL")),
	}
	for name, value := range map[string]string{
		"FAULTLINE_OIDC_ISSUER":        sso.Issuer,
		"FAULTLINE_OIDC_CLIENT_SECRET": sso.ClientSecret,
		"FAULTLINE_OIDC_REDIRECT_URL":  sso.RedirectURL,
	} {
		if value == "" {
			return SSOConfig{}, fmt.Errorf("%s is required when FAULTLINE_OIDC_CLIENT_ID is set", name)
		}
	}
	for domain := range strings.SplitSeq(os.Getenv("FAULTLINE_OIDC_ALLOWED_HD"), ",") {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain != "" {
			sso.AllowedDomains = append(sso.AllowedDomains, domain)
		}
	}
	if len(sso.AllowedDomains) == 0 {
		return SSOConfig{}, fmt.Errorf("FAULTLINE_OIDC_ALLOWED_HD must list at least one domain when SSO is enabled")
	}
	for name, value := range map[string]string{
		"FAULTLINE_OIDC_ISSUER":       sso.Issuer,
		"FAULTLINE_OIDC_REDIRECT_URL": sso.RedirectURL,
	} {
		if err := requireSecureURL(name, value); err != nil {
			return SSOConfig{}, err
		}
	}
	return sso, nil
}

// requireSecureURL rejects a credential-bearing URL that is not https. The
// authorization code and the client secret both travel over these two URLs,
// so plaintext would expose them. Loopback is exempt: an OIDC provider may
// legitimately be reached at http://localhost during development, and Google
// permits loopback redirect URIs for exactly that reason.
func requireSecureURL(name, value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("%s is not a valid URL: %w", name, err)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%s must include a host", name)
	}
	if parsed.Scheme == "https" {
		return nil
	}
	host := parsed.Hostname()
	if parsed.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1") {
		return nil
	}
	return fmt.Errorf("%s must use https (http is permitted only for localhost)", name)
}

func boolValue(name string, fallback bool) (bool, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", name, err)
	}
	return value, nil
}

func int32Value(name string, fallback int32) (int32, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be a 32-bit integer: %w", name, err)
	}
	return int32(value), nil
}

func int64Value(name string, fallback int64) (int64, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a 64-bit integer: %w", name, err)
	}
	return value, nil
}

func durationValue(name string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a Go duration: %w", name, err)
	}
	if value < 0 {
		return 0, fmt.Errorf("%s must not be negative", name)
	}
	return value, nil
}
