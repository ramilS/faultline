// Package group defines the access-group domain: named collections of users
// granted access to a set of projects.
package group

import (
	"errors"
	"time"
)

const maxGroupNameLength = 64

var (
	ErrNotFound    = errors.New("not found")
	ErrConflict    = errors.New("conflict")
	ErrInvalidName = errors.New("invalid name")
	// ErrUnknownReference reports that a group exists but a user or project id
	// named in a replace request does not. Distinct from ErrNotFound: the
	// group itself was found, so a 404 here would misreport which resource is
	// missing.
	ErrUnknownReference = errors.New("unknown reference")
)

// Group is a named collection of users and the projects they can reach
// through it.
type Group struct {
	ID           int64
	Name         string
	MemberCount  int
	ProjectCount int
	CreatedAt    time.Time
}

// Detail is a single group's full membership and project grants.
type Detail struct {
	Group      Group
	UserIDs    []int64
	ProjectIDs []int64
}

// NewGroup is the repository input for a new group.
type NewGroup struct {
	Name string
}
