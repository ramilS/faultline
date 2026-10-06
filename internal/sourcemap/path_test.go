package sourcemap

import (
	"errors"
	"testing"
)

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
		err  error
	}{
		{name: "absolute URL", raw: "https://cdn.example/assets/app.js?v=4#x", want: "/assets/app.js"},
		{name: "absolute path", raw: "/assets/app.js", want: "/assets/app.js"},
		{name: "decodes and cleans", raw: "/assets/%2E%2E/scripts/./app.js", want: "/scripts/app.js"},
		{name: "root", raw: "/", err: ErrInvalidPath},
		{name: "empty", raw: "", err: ErrInvalidPath},
		{name: "relative", raw: "assets/app.js", err: ErrInvalidPath},
		{name: "invalid escape", raw: "/assets/%zz/app.js", err: ErrInvalidPath},
		{name: "backslash", raw: "/assets\\app.js", err: ErrInvalidPath},
		{name: "traversal to root", raw: "/assets/../..", err: ErrInvalidPath},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizePath(test.raw)
			if !errors.Is(err, test.err) {
				t.Fatalf("NormalizePath(%q) error = %v, want %v", test.raw, err, test.err)
			}
			if got != test.want {
				t.Fatalf("NormalizePath(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestNormalizePathRejectsAmbiguousColonsInHost(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"double port", "http://localhost:80:80/static/app.js"},
		{"bare ipv6 without brackets", "http://::1/static/app.js"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := NormalizePath(testCase.raw); !errors.Is(err, ErrInvalidPath) {
				t.Fatalf("NormalizePath(%q) error = %v, want ErrInvalidPath", testCase.raw, err)
			}
		})
	}
}

func TestNormalizePathAcceptsBracketedIPv6Host(t *testing.T) {
	got, err := NormalizePath("http://[::1]/static/app.js")
	if err != nil {
		t.Fatalf("NormalizePath returned %v, want nil", err)
	}
	if got != "/static/app.js" {
		t.Fatalf("NormalizePath = %q, want %q", got, "/static/app.js")
	}
}
