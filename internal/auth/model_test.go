package auth

import (
	"errors"
	"testing"
	"time"
)

func TestParseRole(t *testing.T) {
	for _, value := range []string{"owner", "member"} {
		t.Run(value, func(t *testing.T) {
			role, err := ParseRole(value)
			if err != nil {
				t.Fatalf("ParseRole(%q) error = %v", value, err)
			}
			if string(role) != value {
				t.Fatalf("ParseRole(%q) = %q", value, role)
			}
		})
	}

	for _, value := range []string{"", "Owner", "admin", "owner "} {
		t.Run("rejects "+value, func(t *testing.T) {
			if _, err := ParseRole(value); err == nil {
				t.Fatalf("ParseRole(%q) error = nil, want error", value)
			}
		})
	}
}

func TestUserIsActive(t *testing.T) {
	if !(User{}).IsActive() {
		t.Fatal("IsActive() = false for nil DeactivatedAt, want true")
	}
	deactivated := time.Now()
	if (User{DeactivatedAt: &deactivated}).IsActive() {
		t.Fatal("IsActive() = true for set DeactivatedAt, want false")
	}
}

func TestUserIsOwner(t *testing.T) {
	if !(User{Role: RoleOwner}).IsOwner() {
		t.Fatal("IsOwner() = false for RoleOwner, want true")
	}
	if (User{Role: RoleMember}).IsOwner() {
		t.Fatal("IsOwner() = true for RoleMember, want false")
	}
}

func TestParseRoleWrapsSentinelError(t *testing.T) {
	_, err := ParseRole("admin")
	if !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("ParseRole() error = %v, want wrapping ErrInvalidRole", err)
	}
}
