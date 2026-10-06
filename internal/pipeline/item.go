// Package pipeline fans accepted events across fingerprint-affinity shards that
// aggregate issue deltas and flush them to a storage.EventSink.
package pipeline

import "github.com/ramilS/faultline/internal/storage"

// Item is a single accepted event dispatched to a shard. The event row already
// carries its computed fingerprint; Title, Culprit, and Tags feed the issue and
// tag-facet deltas, and Release (when non-empty) is registered for the project.
type Item struct {
	Row     storage.EventRow
	Title   string
	Culprit string
	Tags    map[string]string
	Release string
	// User is the event's identity contribution to its issue's distinct-user
	// count, already reduced to one key/value pair by the ingest package. It
	// is nil when the event carried no identity, or when the identity was an
	// ip the project has not opted into aggregating (projects.store_user_ip).
	// Aggregating it into issue_users is a later stage; this stage only
	// carries the value through.
	User *UserIdentity
}

// UserIdentity names one end user for an issue's distinct-user aggregate. It
// mirrors ingest.EventUser without depending on the ingest package, which
// already imports pipeline.
type UserIdentity struct {
	Key   string
	Value string
}

// spanWork is a batch of spans dispatched to a shard for a single project.
type spanWork struct {
	projectID int64
	rows      []storage.SpanRow
}
