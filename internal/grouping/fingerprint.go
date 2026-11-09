package grouping

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	maxInAppFrames   = 16
	maxFallbackDepth = 8
)

// Frame is a single normalized stack frame used for grouping.
type Frame struct {
	Module   string
	Function string
	InApp    bool
	// Package is the native image the frame ran in, and ImageOffset the frame's
	// offset inside it. They identify a frame that has no module or function.
	Package         string
	ImageOffset     *uint64
	InstructionAddr *uint64
	Symbol          string
}

// Exception is the validated exception detail used for grouping.
type Exception struct {
	Type   string
	Value  string
	Frames []Frame
}

// GroupingInput is the minimal validated event detail fingerprinting needs.
type GroupingInput struct {
	Logger    string
	Message   string
	Exception *Exception
}

// Result is the deterministic grouping outcome for an event.
type Result struct {
	Fingerprint string
	Title       string
	Culprit     string
	// LegacyFingerprint is the fingerprint without symbols, set only when it
	// differs from Fingerprint.
	LegacyFingerprint string
	// SymbolicatedCulprit is Culprit when its frame is named by a native
	// symbol, and "" otherwise.
	SymbolicatedCulprit string
}

// Fingerprint derives a deterministic fingerprint, title, and culprit. Priority:
// exception with a stacktrace (in-app frames, else top frames), then exception
// without a stacktrace, then plain message.
func Fingerprint(input GroupingInput) Result {
	if input.Exception == nil {
		return Result{
			Fingerprint: hashParts(input.Logger, normalizeMessage(input.Message)),
			Title:       firstLine(input.Message),
		}
	}

	exception := input.Exception
	title := Title(exception, input.Message)
	culprit := Culprit(exception.Frames)

	if frames := groupingFrames(exception.Frames); len(frames) > 0 {
		result := Result{
			Fingerprint:         hashFrames(exception.Type, frames, frameKey),
			Title:               title,
			Culprit:             culprit,
			SymbolicatedCulprit: symbolicatedCulprit(exception.Frames),
		}
		if legacy := hashFrames(exception.Type, frames, unsymbolizedFrameKey); legacy != result.Fingerprint {
			result.LegacyFingerprint = legacy
		}
		return result
	}

	basis := exception.Value
	if basis == "" {
		basis = input.Message
	}
	return Result{
		Fingerprint: hashParts(exception.Type, normalizeMessage(basis)),
		Title:       title,
		Culprit:     culprit,
	}
}

func hashFrames(exceptionType string, frames []Frame, key func(Frame) string) string {
	parts := make([]string, 0, len(frames)+1)
	parts = append(parts, exceptionType)
	for _, frame := range frames {
		parts = append(parts, key(frame))
	}
	return hashParts(parts...)
}

func groupingFrames(frames []Frame) []Frame {
	indices := groupingFrameIndices(frames)
	selected := make([]Frame, len(indices))
	for position, index := range indices {
		selected[position] = frames[index]
	}
	return selected
}

func groupingFrameIndices(frames []Frame) []int {
	indices := make([]int, 0, min(len(frames), maxInAppFrames))
	for index, frame := range frames {
		if frame.InApp && len(indices) < maxInAppFrames {
			indices = append(indices, index)
		}
	}
	if len(indices) > 0 {
		return indices
	}
	for index := range min(len(frames), maxFallbackDepth) {
		indices = append(indices, index)
	}
	return indices
}

func symbolicatedCulprit(frames []Frame) string {
	for _, frame := range frames {
		if frame.InApp {
			return normalizeFunction(frame.Symbol)
		}
	}
	return ""
}

// Culprit names the first in-app frame the way the fingerprint keys it.
func Culprit(frames []Frame) string {
	for _, frame := range frames {
		if frame.InApp {
			return frameKey(frame)
		}
	}
	return ""
}

func Title(exception *Exception, message string) string {
	line := firstLine(message)
	if line == "" {
		line = firstLine(exception.Value)
	}
	if exception.Type == "" {
		return line
	}
	if line == "" {
		return exception.Type
	}
	return exception.Type + ": " + line
}

func firstLine(text string) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		text = text[:index]
	}
	return strings.TrimSpace(text)
}

func hashParts(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}
