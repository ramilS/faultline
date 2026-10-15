package project

import (
	"context"

	"github.com/ramilS/faultline/internal/access"
)

// ProjectRepository persists project metadata without exposing storage details.
type ProjectRepository interface {
	Create(context.Context, CreateProject) (Project, error)
	List(context.Context, access.Scope) ([]Project, error)
	FindBySlug(context.Context, access.Scope, string) (Project, error)
	Update(context.Context, string, UpdateProject) (Project, error)
}

// KeyRepository persists public ingest-key credentials and their settings.
// retrieval or revocation operations.
type KeyRepository interface {
	Create(context.Context, CreateKey) (Key, error)
	ListByProjectSlug(context.Context, access.Scope, string) ([]Key, error)
	Revoke(context.Context, access.Scope, string, int64) error
}
