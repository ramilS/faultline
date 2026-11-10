package notify

import (
	"fmt"
	"net/url"
	"slices"
)

const maxProxyLength = 2048

var proxySchemes = []string{"http", "https", "socks5", "socks5h"}

func parseProxyURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || !slices.Contains(proxySchemes, parsed.Scheme) {
		return nil, invalid("proxy must be an http://, https://, socks5:// or socks5h:// URL such as socks5://user:password@proxy.example.com:1080; Telegram MTProto proxies cannot carry Bot API requests")
	}
	if parsed.Hostname() == "" {
		return nil, invalid("proxy must name a host, as in socks5://proxy.example.com:1080")
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, invalid("proxy must be scheme://[user:password@]host[:port] with no path or query")
	}
	return parsed, nil
}

func validateProxy(raw string, allowPrivate bool) error {
	if len(raw) > maxProxyLength {
		return invalid(fmt.Sprintf("proxy must be at most %d characters", maxProxyLength))
	}
	parsed, err := parseProxyURL(raw)
	if err != nil {
		return err
	}
	return refuseInsideHost("proxy", parsed.Hostname(), allowPrivate)
}

func RedactedProxy(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "(unreadable proxy URL)"
	}
	return parsed.Redacted()
}
