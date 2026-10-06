package group

import "context"

// Repository persists groups and their membership and project grants.
type Repository interface {
	List(ctx context.Context) ([]Group, error)
	Find(ctx context.Context, id int64) (Detail, error)
	Create(ctx context.Context, input NewGroup) (Group, error)
	Rename(ctx context.Context, id int64, name string) (Group, error)
	Delete(ctx context.Context, id int64) error
	ReplaceMembers(ctx context.Context, id int64, userIDs []int64) error
	ReplaceProjects(ctx context.Context, id int64, projectIDs []int64) error
}
