package project

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxIconBytes        = 128 << 10
	MaxIconDataURLBytes = 192 << 10
	MaxIconDimension    = 128
)

func validateIdentity(emoji, icon string) error {
	if !validProjectEmoji(emoji) {
		return fmt.Errorf("invalid project emoji")
	}
	_, err := NormalizeIconImage(icon)
	return err
}

// A project icon is one emoji cluster: a symbol, a joined sequence, a flag,
// or a keycap. Keeping it to one cluster also prevents icons from covering
// neighboring project names in the switcher and Telegram header.
func validProjectEmoji(value string) bool {
	if value == "" {
		return true
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 32 {
		return false
	}
	runes := []rune(value)
	if len(runes) == 2 && regionalIndicator(runes[0]) && regionalIndicator(runes[1]) {
		return true
	}
	if len(runes) >= 2 && (runes[0] >= '0' && runes[0] <= '9' || runes[0] == '#' || runes[0] == '*') {
		if len(runes) == 2 {
			return runes[1] == '\u20e3'
		}
		return len(runes) == 3 && runes[1] == '\ufe0f' && runes[2] == '\u20e3'
	}
	needBase := true
	for _, r := range runes {
		if needBase {
			if !unicode.Is(unicode.So, r) || regionalIndicator(r) {
				return false
			}
			needBase = false
			continue
		}
		switch {
		case r == '\u200d':
			needBase = true
		case r == '\ufe0e' || r == '\ufe0f':
		case r >= 0x1f3fb && r <= 0x1f3ff:
		default:
			return false
		}
	}
	return !needBase
}

func regionalIndicator(r rune) bool { return r >= 0x1f1e6 && r <= 0x1f1ff }

// NormalizeIconImage bounds decoding before allocating pixels and strips metadata
// by encoding only the decoded pixels as PNG. No URLs are fetched.
func NormalizeIconImage(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	invalid := fmt.Errorf("invalid project image: use PNG or JPEG up to 128x128 and 128 KiB")
	if len(value) > MaxIconDataURLBytes {
		return "", invalid
	}
	header, encoded, ok := strings.Cut(value, ",")
	if !ok || (header != "data:image/png;base64" && header != "data:image/jpeg;base64") {
		return "", invalid
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) > MaxIconBytes {
		return "", invalid
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "png" && format != "jpeg") || header != "data:image/"+format+";base64" || config.Width < 1 || config.Height < 1 || config.Width > MaxIconDimension || config.Height > MaxIconDimension {
		return "", invalid
	}
	pixels, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", invalid
	}
	var normalized bytes.Buffer
	if err := png.Encode(&normalized, pixels); err != nil {
		return "", invalid
	}
	if normalized.Len() > MaxIconBytes {
		return "", invalid
	}
	return IconDataURL(normalized.Bytes()), nil
}

func IconDataURL(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
}

// IconBytes converts a validated, normalized icon for PostgreSQL bytea storage.
func IconBytes(value string) ([]byte, error) {
	normalized, err := NormalizeIconImage(value)
	if err != nil {
		return nil, err
	}
	if normalized == "" {
		return []byte{}, nil
	}
	return base64.StdEncoding.DecodeString(strings.TrimPrefix(normalized, "data:image/png;base64,"))
}
