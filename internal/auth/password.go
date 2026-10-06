package auth

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	passwordMinimumRunes = 12
	argon2Memory         = 64 * 1024
	argon2Iterations     = 3
	argon2Parallelism    = 2
	argon2SaltLength     = 16
	argon2KeyLength      = 32
	maximumPHCLength     = 256
)

var (
	errInvalidPasswordHash = errors.New("invalid password hash")
	errEntropyRequired     = errors.New("entropy reader is required")

	// ErrPasswordTooShort and ErrPasswordInvalidUTF8 report a password that
	// fails ValidatePassword. Exported so callers (internal/api) can map them
	// to a 400 by identity (errors.Is) instead of matching message text.
	ErrPasswordTooShort    = errors.New("password must be at least 12 Unicode code points")
	ErrPasswordInvalidUTF8 = errors.New("password must be valid UTF-8")
)

func HashPassword(password string, entropy io.Reader) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	if entropy == nil {
		return "", errEntropyRequired
	}

	salt := make([]byte, argon2SaltLength)
	if _, err := io.ReadFull(entropy, salt); err != nil {
		return "", err
	}

	key := argon2.IDKey(
		[]byte(password),
		salt,
		argon2Iterations,
		argon2Memory,
		argon2Parallelism,
		argon2KeyLength,
	)

	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argon2Memory,
		argon2Iterations,
		argon2Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func VerifyPassword(encodedHash, password string) (bool, error) {
	salt, expectedKey, err := parsePasswordHash(encodedHash)
	if err != nil {
		return false, err
	}

	candidateKey := argon2.IDKey(
		[]byte(password),
		salt,
		argon2Iterations,
		argon2Memory,
		argon2Parallelism,
		argon2KeyLength,
	)

	return subtle.ConstantTimeCompare(candidateKey, expectedKey) == 1, nil
}

func ValidatePassword(password string) error {
	if !utf8.ValidString(password) {
		return ErrPasswordInvalidUTF8
	}
	if utf8.RuneCountInString(password) < passwordMinimumRunes {
		return ErrPasswordTooShort
	}
	return nil
}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func parsePasswordHash(encodedHash string) ([]byte, []byte, error) {
	if encodedHash == "" || len(encodedHash) > maximumPHCLength {
		return nil, nil, errInvalidPasswordHash
	}

	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return nil, nil, errInvalidPasswordHash
	}

	parameters := strings.Split(parts[3], ",")
	if len(parameters) != 3 {
		return nil, nil, errInvalidPasswordHash
	}
	memory, ok := parsePHCParameter(parameters[0], "m", argon2Memory)
	if !ok || memory != argon2Memory {
		return nil, nil, errInvalidPasswordHash
	}
	iterations, ok := parsePHCParameter(parameters[1], "t", argon2Iterations)
	if !ok || iterations != argon2Iterations {
		return nil, nil, errInvalidPasswordHash
	}
	parallelism, ok := parsePHCParameter(parameters[2], "p", argon2Parallelism)
	if !ok || parallelism != argon2Parallelism {
		return nil, nil, errInvalidPasswordHash
	}

	if len(parts[4]) != base64.RawStdEncoding.EncodedLen(argon2SaltLength) ||
		len(parts[5]) != base64.RawStdEncoding.EncodedLen(argon2KeyLength) {
		return nil, nil, errInvalidPasswordHash
	}

	decoder := base64.RawStdEncoding.Strict()
	salt, err := decoder.DecodeString(parts[4])
	if err != nil || len(salt) != argon2SaltLength {
		return nil, nil, errInvalidPasswordHash
	}
	key, err := decoder.DecodeString(parts[5])
	if err != nil || len(key) != argon2KeyLength {
		return nil, nil, errInvalidPasswordHash
	}

	return salt, key, nil
}

func parsePHCParameter(parameter, name string, maximum uint64) (uint64, bool) {
	prefix := name + "="
	if !strings.HasPrefix(parameter, prefix) {
		return 0, false
	}
	valueText := strings.TrimPrefix(parameter, prefix)
	if valueText == "" {
		return 0, false
	}
	value, err := strconv.ParseUint(valueText, 10, 64)
	if err != nil || strconv.FormatUint(value, 10) != valueText || value == 0 || value > maximum {
		return 0, false
	}
	return value, true
}
