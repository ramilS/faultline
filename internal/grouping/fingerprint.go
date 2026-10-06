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
	Package     string
	ImageOffset *uint64
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
	title := titleFor(exception, input.Message)
	culprit := culpritFor(exception.Frames)

	if frames := groupingFrames(exception.Frames); len(frames) > 0 {
		parts := make([]string, 0, len(frames)+1)
		parts = append(parts, exception.Type)
		for _, frame := range frames {
			parts = append(parts, frameKey(frame))
		}
		return Result{Fingerprint: hashParts(parts...), Title: title, Culprit: culprit}
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

func groupingFrames(frames []Frame) []Frame {
	inApp := make([]Frame, 0, len(frames))
	for _, frame := range frames {
		if frame.InApp {
			inApp = append(inApp, frame)
		}
	}
	if len(inApp) > 0 {
		if len(inApp) > maxInAppFrames {
			inApp = inApp[:maxInAppFrames]
		}
		return inApp
	}
	if len(frames) > maxFallbackDepth {
		return frames[:maxFallbackDepth]
	}
	return frames
}

func culpritFor(frames []Frame) string {
	for _, frame := range frames {
		if frame.InApp {
			return frameKey(frame)
		}
	}
	return ""
}

func titleFor(exception *Exception, message string) string {
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
