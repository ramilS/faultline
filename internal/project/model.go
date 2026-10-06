// Package project defines project and ingest-key domain contracts.
package project

import (
	"errors"
	"time"
)

const (
	PlatformJavaScript          = "javascript"
	PlatformJVMSpring           = "jvm-spring"
	PlatformJVMKtor             = "jvm-ktor"
	PlatformKotlinMultiplatform = "kotlin-multiplatform"
	PlatformScala               = "scala"
	PlatformNestJS              = "nestjs"
	PlatformPython              = "python"
	PlatformPythonDjango        = "python-django"
	PlatformPythonFastAPI       = "python-fastapi"
	PlatformPythonFlask         = "python-flask"
	PlatformGo                  = "go"
	PlatformRuby                = "ruby"
	PlatformRubyRails           = "ruby-rails"
	PlatformPHP                 = "php"
	PlatformPHPLaravel          = "php-laravel"
	PlatformPHPSymfony          = "php-symfony"
	PlatformRust                = "rust"
	PlatformAndroid             = "android"
	PlatformIOS                 = "ios"
	PlatformOther               = "other"

	maxProjectNameLength = 120
	maxRateLimitRPS      = 10_000
	maxRateLimitBurst    = 100_000
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

// Project is a configured application that can receive ingest events.
type Project struct {
	ID        int64
	Slug      string
	Name      string
	Platform  string
	CreatedAt time.Time
}

// Key is the persisted public ingest credential and its admission settings.
// PublicKey remains listable so SDKs can be configured.
type Key struct {
	ID             int64
	ProjectID      int64
	PublicKey      string
	RateLimitRPS   int
	RateLimitBurst int
	RevokedAt      *time.Time
}

// CreateProject is the repository input for a new project.
type CreateProject struct {
	Slug     string
	Name     string
	Platform string
}

// CreateKey is the repository input for a new ingest key.
type CreateKey struct {
	ProjectID      int64
	PublicKey      string
	RateLimitRPS   int
	RateLimitBurst int
}
