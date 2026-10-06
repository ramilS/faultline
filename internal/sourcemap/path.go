package sourcemap

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

// NormalizePath returns the canonical absolute generated-file path used for
// both upload identity and event-frame matching.
func NormalizePath(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidPath, err)
	}

	decoded, err := url.PathUnescape(parsed.EscapedPath())
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidPath, err)
	}
	if !strings.HasPrefix(decoded, "/") || strings.Contains(decoded, "\\") {
		return "", ErrInvalidPath
	}

	cleaned := path.Clean(decoded)
	if cleaned == "/" {
		return "", ErrInvalidPath
	}
	return cleaned, nil
}
