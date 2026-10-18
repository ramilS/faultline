package native

import (
	"context"
	"debug/dwarf"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/sourcemap"
)

const (
	// Apple processes commonly load hundreds of system frameworks. Keep the
	// bound high enough that the application image is not silently discarded
	// while still limiting work on untrusted event payloads.
	maxImagesPerEvent = 512
	parsedCacheBudget = 128 << 20
)

type parsedObject struct {
	data   *dwarf.Data
	funcs  []functionRange
	vmaddr uint64
}

// imageLookup is what one event learned about an image, so the rest of its
// frames in that image neither query the repository nor parse it again.
type imageLookup struct {
	object *parsedObject
	err    error
}

type functionRange struct {
	low, high uint64
	name      string
}

type objectCache struct {
	mu    sync.Mutex
	m     map[string]*parsedObject
	bytes int64
}

type resolver struct {
	repo     Repository
	fallback sourcemap.Resolver
	cache    objectCache
}

func NewResolver(repo Repository, fallback sourcemap.Resolver) sourcemap.Resolver {
	return &resolver{repo: repo, fallback: fallback, cache: objectCache{m: make(map[string]*parsedObject)}}
}

type payloadFrame struct {
	InstructionAddr address `json:"instruction_addr"`
	Function        string  `json:"function"`
	Filename        string  `json:"filename"`
	Lineno          *int    `json:"lineno"`
	InApp           bool    `json:"in_app"`
}

type payloadException struct {
	Type       string `json:"type"`
	Value      string `json:"value"`
	Stacktrace *struct {
		Frames []payloadFrame `json:"frames"`
	} `json:"stacktrace"`
}

type debugImage struct {
	Type      string  `json:"type"`
	DebugID   string  `json:"debug_id"`
	ImageAddr address `json:"image_addr"`
	ImageSize address `json:"image_size"`
}

type address struct {
	value uint64
	valid bool
}

func (a *address) UnmarshalJSON(raw []byte) error {
	text := strings.TrimSpace(string(raw))
	if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
		text = text[1 : len(text)-1]
	}
	a.value, a.valid = parseAddress(text)
	return nil
}

func (r *resolver) Resolve(ctx context.Context, in sourcemap.EventInput) (*sourcemap.Exception, error) {
	var payload struct {
		Exception *sourcemap.ExceptionValues[payloadException] `json:"exception"`
		DebugMeta struct {
			Images []debugImage `json:"images"`
		} `json:"debug_meta"`
	}
	if json.Unmarshal(in.Payload, &payload) != nil || payload.Exception == nil || len(payload.Exception.Values) == 0 {
		return r.fallback.Resolve(ctx, in)
	}
	images := validImages(payload.DebugMeta.Images)
	if len(images) == 0 || r.repo == nil {
		return r.fallback.Resolve(ctx, in)
	}
	var firstFailure error
	var resolvedAny bool
	var head *sourcemap.Exception
	looked := make(map[string]imageLookup)
	for _, value := range payload.Exception.Values {
		exception := &sourcemap.Exception{Type: value.Type, Value: value.Value, Cause: head}
		if value.Stacktrace != nil {
			for _, source := range slices.Backward(value.Stacktrace.Frames) {
				frame := sourcemap.Frame{Generated: sourcemap.Position{Function: source.Function, Filename: source.Filename, InApp: source.InApp}}
				if source.Lineno != nil {
					frame.Generated.Line = *source.Lineno
				}
				address, ok := source.InstructionAddr.value, source.InstructionAddr.valid
				if !ok {
					exception.Frames = append(exception.Frames, frame)
					continue
				}
				image, ok := imageFor(images, address)
				if !ok {
					exception.Frames = append(exception.Frames, frame)
					continue
				}
				object, err := r.objectFor(ctx, in.ProjectID, image.DebugID, looked)
				if errors.Is(err, ErrNotFound) {
					exception.Frames = append(exception.Frames, frame)
					continue
				}
				if err != nil {
					if firstFailure == nil {
						firstFailure = err
					}
					exception.Frames = append(exception.Frames, frame)
					continue
				}
				pc := address - image.Base + object.vmaddr
				if original, ok := resolvePC(object, pc, source.InApp); ok {
					frame.Original = original
					resolvedAny = true
				}
				exception.Frames = append(exception.Frames, frame)
			}
		}
		head = exception
	}
	if !resolvedAny {
		fallback, err := r.fallback.Resolve(ctx, in)
		return fallback, errors.Join(firstFailure, err)
	}
	return head, firstFailure
}

type imageRange struct {
	DebugID   string
	Base, End uint64
}

func validImages(input []debugImage) []imageRange {
	if len(input) > maxImagesPerEvent {
		input = input[:maxImagesPerEvent]
	}
	result := make([]imageRange, 0, len(input))
	for _, image := range input {
		if image.Type != "macho" && image.Type != "apple" {
			continue
		}
		parsed, err := uuid.Parse(image.DebugID)
		if err != nil {
			continue
		}
		base, ok := image.ImageAddr.value, image.ImageAddr.valid
		if !ok {
			continue
		}
		size, ok := image.ImageSize.value, image.ImageSize.valid
		if !ok || size == 0 || base > ^uint64(0)-size {
			continue
		}
		result = append(result, imageRange{DebugID: parsed.String(), Base: base, End: base + size})
	}
	return result
}

func parseAddress(value string) (uint64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(value, 0, 64)
	return n, err == nil
}

func imageFor(images []imageRange, address uint64) (imageRange, bool) {
	for _, image := range images {
		if address >= image.Base && address < image.End {
			return image, true
		}
	}
	return imageRange{}, false
}

// objectFor resolves an image once per event; looked holds what earlier frames
// of the same event already found.
func (r *resolver) objectFor(ctx context.Context, projectID int64, debugID string, looked map[string]imageLookup) (*parsedObject, error) {
	if found, ok := looked[debugID]; ok {
		return found.object, found.err
	}
	object, err := r.lookup(ctx, projectID, debugID)
	looked[debugID] = imageLookup{object: object, err: err}
	return object, err
}

// lookup checks the parsed cache before the repository: Find returns the whole
// compressed debug file. A debug id's content is immutable once stored (Put
// refuses different bytes under it), so the project and debug id are a sound
// cache key.
func (r *resolver) lookup(ctx context.Context, projectID int64, debugID string) (*parsedObject, error) {
	key := fmt.Sprintf("%d/%s", projectID, debugID)
	r.cache.mu.Lock()
	cached := r.cache.m[key]
	r.cache.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	stored, err := r.repo.Find(ctx, projectID, debugID)
	if errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("find native debug file %s: %w", debugID, err)
	}
	object, err := r.load(key, stored)
	if err != nil {
		return nil, fmt.Errorf("read native debug file %s: %w", debugID, err)
	}
	return object, nil
}

func (r *resolver) load(key string, stored Stored) (*parsedObject, error) {
	body, err := decompress(stored)
	if err != nil {
		return nil, err
	}
	file, closeFile, err := openObject(body, stored.ObjectIndex)
	if err != nil {
		return nil, err
	}
	defer func() { _ = closeFile() }()
	data, err := file.DWARF()
	if err != nil {
		return nil, err
	}
	parsed := &parsedObject{data: data, vmaddr: stored.VMAddr}
	reader := data.Reader()
	for {
		entry, err := reader.Next()
		if err != nil {
			return nil, err
		}
		if entry == nil {
			break
		}
		if entry.Tag != dwarf.TagSubprogram {
			continue
		}
		name, _ := entry.Val(dwarf.AttrName).(string)
		if name == "" {
			name, _ = entry.Val(dwarf.AttrLinkageName).(string)
		}
		if name == "" {
			continue
		}
		ranges, err := data.Ranges(entry)
		if err != nil {
			return nil, err
		}
		for _, span := range ranges {
			if span[1] > span[0] {
				parsed.funcs = append(parsed.funcs, functionRange{span[0], span[1], name})
			}
		}
	}
	r.cache.mu.Lock()
	if existing := r.cache.m[key]; existing != nil {
		r.cache.mu.Unlock()
		return existing, nil
	}
	if stored.UncompressedBytes <= parsedCacheBudget && r.cache.bytes+stored.UncompressedBytes > parsedCacheBudget {
		clear(r.cache.m)
		r.cache.bytes = 0
	}
	if stored.UncompressedBytes <= parsedCacheBudget {
		r.cache.m[key] = parsed
		r.cache.bytes += stored.UncompressedBytes
	}
	r.cache.mu.Unlock()
	return parsed, nil
}

func resolvePC(object *parsedObject, pc uint64, inApp bool) (*sourcemap.Position, bool) {
	var best *functionRange
	for i := range object.funcs {
		span := &object.funcs[i]
		if pc >= span.low && pc < span.high && (best == nil || span.high-span.low < best.high-best.low) {
			best = span
		}
	}
	if best == nil {
		return nil, false
	}
	position := &sourcemap.Position{Function: best.name, InApp: inApp}
	reader := object.data.Reader()
	entry, err := reader.SeekPC(pc)
	if err == nil && entry != nil {
		if lines, err := object.data.LineReader(entry); err == nil && lines != nil {
			var line dwarf.LineEntry
			if lines.SeekPC(pc, &line) == nil && line.File != nil {
				position.Filename = line.File.Name
				position.Line = line.Line
				position.Column = line.Column
			}
		}
	}
	return position, true
}
