package proguard

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/sourcemap"
)

const (
	// maxMappingsPerEvent bounds how many mappings one event may make the
	// resolver load. debug_meta is SDK input; a real app reports one mapping
	// per module.
	maxMappingsPerEvent = 8

	// sdkPlaceholderFile is what R8 renames a class's source file attribute to
	// by default, so it says nothing about the original file.
	sdkPlaceholderFile = "SourceFile"
)

type resolver struct {
	repo     Repository
	fallback sourcemap.Resolver
	cache    *mappingCache
}

// NewResolver builds a read-time resolver that retraces the JVM exceptions of
// events naming ProGuard/R8 mappings in debug_meta, and hands every other
// event, and every event whose mappings are not stored, to fallback unchanged.
func NewResolver(repo Repository, fallback sourcemap.Resolver) sourcemap.Resolver {
	return &resolver{repo: repo, fallback: fallback, cache: newMappingCache(maxCachedMappings)}
}

type payloadFrame struct {
	Module   string `json:"module"`
	Function string `json:"function"`
	Filename string `json:"filename"`
	Lineno   *int   `json:"lineno"`
	InApp    bool   `json:"in_app"`
}

type payloadException struct {
	Type       string `json:"type"`
	Value      string `json:"value"`
	Stacktrace *struct {
		Frames []payloadFrame `json:"frames"`
	} `json:"stacktrace"`
}

func (r *resolver) Resolve(ctx context.Context, in sourcemap.EventInput) (*sourcemap.Exception, error) {
	// debug_meta stays raw for the same reason as in the source-map resolver: a
	// type mismatch inside it must not fail the decode of the exception beside it.
	var payload struct {
		Exception *sourcemap.ExceptionValues[payloadException] `json:"exception"`
		DebugMeta json.RawMessage                              `json:"debug_meta"`
	}
	if err := json.Unmarshal(in.Payload, &payload); err != nil {
		return r.fallback.Resolve(ctx, in)
	}
	mappingUUIDs := proguardUUIDs(payload.DebugMeta)
	if len(mappingUUIDs) == 0 || payload.Exception == nil || len(payload.Exception.Values) == 0 {
		return r.fallback.Resolve(ctx, in)
	}

	values := payload.Exception.Values
	mappings, failures := r.load(ctx, in.ProjectID, mappingUUIDs, classesIn(values))
	if len(mappings) == 0 {
		exception, err := r.fallback.Resolve(ctx, in)
		return exception, errors.Join(append(failures, err)...)
	}

	// Sentry sends exception values oldest-first; the linked chain runs
	// outermost-first, so each value becomes the new head with the previous
	// head as its cause.
	var head *sourcemap.Exception
	var retracedAny bool
	for _, value := range values {
		exception := &sourcemap.Exception{Type: value.Type, Value: value.Value, Cause: head}
		if value.Stacktrace != nil {
			frames, retraced := retraceFrames(value.Stacktrace.Frames, mappings)
			exception.Frames = frames
			retracedAny = retracedAny || retraced
		}
		head = exception
	}
	if !retracedAny {
		// The event's debug_meta named a mapping, but none of its frames'
		// classes were in it — a build's proguard image tag that is simply
		// irrelevant to this exception, plausible for an event whose frames
		// are not JVM at all (a hybrid app mixing native and JS frames in one
		// event). Falling back here means such frames still get JS source-map
		// resolution instead of being left unretraced.
		exception, err := r.fallback.Resolve(ctx, in)
		return exception, errors.Join(append(failures, err)...)
	}
	return head, errors.Join(failures...)
}

// load returns the mappings the event names, parsed for only the classes its
// frames mention, from the cache when it already covers them. A mapping that
// was never uploaded is skipped silently, since an event can name a build
// nobody uploaded; a mapping that is stored but unreadable is a failure the
// caller should report. Neither is cached.
func (r *resolver) load(ctx context.Context, projectID int64, mappingUUIDs []string, classes map[string]struct{}) ([]*Mapping, []error) {
	var (
		mappings []*Mapping
		failures []error
	)
	for _, mappingUUID := range mappingUUIDs {
		key := strconv.FormatInt(projectID, 10) + "/" + mappingUUID
		if mapping, ok := r.cache.get(key, classes); ok {
			mappings = append(mappings, mapping)
			continue
		}
		stored, err := r.repo.Find(ctx, projectID, mappingUUID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("find proguard mapping %s: %w", mappingUUID, err))
			continue
		}
		covered := r.cache.coverageFor(key, classes)
		mapping, err := parseStored(stored, func(obfuscated string) bool {
			_, ok := covered[obfuscated]
			return ok
		})
		if err != nil {
			failures = append(failures, fmt.Errorf("read proguard mapping %s: %w", mappingUUID, err))
			continue
		}
		r.cache.put(key, mapping, covered)
		mappings = append(mappings, mapping)
	}
	return mappings, failures
}

// parseStored decompresses and parses a stored mapping. UncompressedBytes was
// already checked against MaxDecodedBytes when the mapping was uploaded, but
// that ceiling can move between releases, so a stored mapping that now exceeds
// it is rejected here rather than silently truncated and partially retraced.
func parseStored(stored Stored, wanted func(string) bool) (*Mapping, error) {
	if stored.UncompressedBytes > MaxDecodedBytes {
		return nil, fmt.Errorf("stored mapping is %d bytes, exceeds the %d byte limit", stored.UncompressedBytes, MaxDecodedBytes)
	}
	reader, err := gzip.NewReader(bytes.NewReader(stored.Compressed))
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}
	defer reader.Close()
	return Parse(io.LimitReader(reader, stored.UncompressedBytes+1), wanted)
}

// proguardUUIDs lists the canonical mapping UUIDs an event's debug_meta names,
// in order and without repeats. debug_meta is untrusted SDK input, so anything
// malformed simply names no mapping.
func proguardUUIDs(raw json.RawMessage) []string {
	var meta struct {
		Images []struct {
			Type string `json:"type"`
			UUID string `json:"uuid"`
		} `json:"images"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &meta) != nil {
		return nil
	}
	var found []string
	for _, image := range meta.Images {
		if image.Type != "proguard" || len(image.UUID) != canonicalUUIDLen {
			continue
		}
		parsed, err := uuid.Parse(image.UUID)
		if err != nil {
			continue
		}
		if canonical := parsed.String(); !slices.Contains(found, canonical) {
			found = append(found, canonical)
		}
		if len(found) == maxMappingsPerEvent {
			break
		}
	}
	return found
}

func classesIn(values []payloadException) map[string]struct{} {
	classes := map[string]struct{}{}
	for _, value := range values {
		if value.Stacktrace == nil {
			continue
		}
		for _, frame := range value.Stacktrace.Frames {
			classes[frame.Module] = struct{}{}
		}
	}
	return classes
}

// retraceFrames walks Sentry's caller-to-callee frames from the error site
// outwards and expands each into the original frames it stands for.
func retraceFrames(frames []payloadFrame, mappings []*Mapping) ([]sourcemap.Frame, bool) {
	retraced := make([]sourcemap.Frame, 0, len(frames))
	var retracedAny bool
	for _, source := range slices.Backward(frames) {
		generated := sourcemap.Position{Function: source.Function, Filename: source.Filename, InApp: source.InApp}
		if source.Lineno != nil {
			generated.Line = *source.Lineno
		}
		originals, found := retrace(mappings, Frame{Class: source.Module, Method: source.Function, Line: generated.Line})
		if !found {
			retraced = append(retraced, sourcemap.Frame{Generated: generated})
			continue
		}
		retracedAny = true
		for _, original := range originals {
			retraced = append(retraced, sourcemap.Frame{
				Generated: generated,
				Original: &sourcemap.Position{
					Function: original.Class + "." + original.Method,
					Filename: fileFor(original, source.Filename),
					Line:     original.Line,
					InApp:    source.InApp,
				},
			})
		}
	}
	return retraced, retracedAny
}

// retrace tries every mapping the event names, not just the first that knows
// the class: R8 allocates short obfuscated names ("a.a") independently per
// module, so two unrelated mappings commonly reuse the same class name. A
// mapping whose Retrace resolved only the class, not the method or line
// (Resolved false), is kept as a fallback in case no other mapping does
// better, but a later mapping with a genuine method match takes priority.
func retrace(mappings []*Mapping, frame Frame) ([]Original, bool) {
	var fallback []Original
	for _, mapping := range mappings {
		originals, ok := mapping.Retrace(frame)
		if !ok {
			continue
		}
		if len(originals) == 1 && !originals[0].Resolved {
			if fallback == nil {
				fallback = originals
			}
			continue
		}
		return originals, true
	}
	return fallback, fallback != nil
}

// fileFor keeps the file name the SDK reported for the class the frame was
// reported in, unless R8 replaced it with its placeholder, and derives one for
// every other class since the SDK's name does not describe an inlined class.
func fileFor(original Original, reported string) string {
	if !original.Inlined && reported != "" && reported != sdkPlaceholderFile {
		return reported
	}
	return SourceFile(original.Class)
}
