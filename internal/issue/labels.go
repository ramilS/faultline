package issue

import (
	"path"
	"strings"

	"github.com/ramilS/faultline/internal/grouping"
	"github.com/ramilS/faultline/internal/sourcemap"
)

// Labels are an issue's display title and culprit. Either is "" when the
// symbolicated event did not name it.
type Labels struct {
	Title   string
	Culprit string
}

func (labels Labels) IsEmpty() bool {
	return labels.Title == "" && labels.Culprit == ""
}

// LabelsFrom derives display labels from a symbolicated exception: the title
// only when a mapping renamed the exception type, and the culprit only when
// the first in-app frame resolved to an original function.
func LabelsFrom(exception *sourcemap.Exception, message string) Labels {
	if exception == nil {
		return Labels{}
	}
	var labels Labels
	if exception.ObfuscatedType != "" {
		labels.Title = grouping.Title(&grouping.Exception{Type: exception.Type, Value: exception.Value}, message)
	}
	labels.Culprit = symbolicatedCulprit(exception.Frames)
	return labels
}

func symbolicatedCulprit(frames []sourcemap.Frame) string {
	for _, frame := range frames {
		if !frame.Generated.InApp {
			continue
		}
		if frame.Original == nil {
			return ""
		}
		function := grouping.Culprit([]grouping.Frame{{Function: frame.Original.Function, InApp: true}})
		if function == "" {
			return ""
		}
		if file := path.Base(strings.ReplaceAll(frame.Original.Filename, `\`, "/")); file != "." && file != "/" {
			return function + " (" + file + ")"
		}
		return function
	}
	return ""
}
