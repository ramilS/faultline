package storage

import "context"

// MetadataRepository marks the persistence capability for relational metadata.
// Domain methods are added with the use cases that require them.
type MetadataRepository any

// EventRepository marks the persistence capability for event time-series data.
// Domain methods are added with the use cases that require them.
type EventRepository any

// SpanRepository marks the persistence capability for span time-series data.
// Domain methods are added with the use cases that require them.
type SpanRepository any

// Repositories groups storage capabilities supplied to a unit of work.
type Repositories struct {
	Metadata MetadataRepository
	Events   EventRepository
	Spans    SpanRepository
}

// Transactor runs work atomically against a coherent repository set.
type Transactor interface {
	WithinTransaction(context.Context, func(context.Context, Repositories) error) error
}

// EventSink persists a fully aggregated Batch of events, issues, releases, and
// tag facets in one transaction.
type EventSink interface {
	PersistBatch(context.Context, Batch) error
}

// HealthChecker reports whether the database can serve requests.
type HealthChecker interface {
	Ping(context.Context) error
}
