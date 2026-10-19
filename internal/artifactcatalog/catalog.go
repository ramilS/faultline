// Package artifactcatalog defines the read-only project artifact inventory.
package artifactcatalog

import (
	"context"
	"errors"
	"time"
)

type Type string

const (
	SourceMap       Type = "source_map"
	ProguardMapping Type = "proguard_mapping"
	NativeDebugFile Type = "native_debug_file"
)

var ErrInvalidCursor = errors.New("invalid artifact catalog cursor")

func (t Type) Valid() bool {
	return t == SourceMap || t == ProguardMapping || t == NativeDebugFile
}

type SourceMapReference struct {
	Release       string `json:"release"`
	GeneratedPath string `json:"generated_path"`
	DebugID       string `json:"debug_id"`
}

type NativeReference struct {
	DebugID string `json:"debug_id"`
	CPUName string `json:"cpu_name"`
}

type Artifact struct {
	Type              Type
	ID                int64
	ContentSHA256     string
	MappingUUID       string
	SizeBytes         int64
	UploadedAt        time.Time
	LastUsedAt        *time.Time
	SourceMapRefs     []SourceMapReference
	NativeDebugIDRefs []NativeReference
}

type ListOptions struct {
	Type   Type
	Limit  int
	Cursor string
}

type Page struct {
	Artifacts  []Artifact
	NextCursor string
}

type Repository interface {
	List(context.Context, int64, ListOptions) (Page, error)
}
