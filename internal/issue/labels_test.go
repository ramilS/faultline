package issue

import (
	"testing"

	"github.com/ramilS/faultline/internal/sourcemap"
)

func resolvedFrame(generated, original sourcemap.Position) sourcemap.Frame {
	return sourcemap.Frame{Generated: generated, Original: &original}
}

func TestLabelsFromASymbolicatedException(t *testing.T) {
	cases := map[string]struct {
		exception *sourcemap.Exception
		message   string
		want      Labels
	}{
		"native frame resolved by its dSYM": {
			exception: &sourcemap.Exception{Type: "EXC_BAD_ACCESS", Value: "KERN_INVALID_ADDRESS", Frames: []sourcemap.Frame{
				{Generated: sourcemap.Position{Function: "objc_msgSend"}},
				resolvedFrame(sourcemap.Position{InApp: true}, sourcemap.Position{Function: "PlaybackController.invoke", Filename: "/Users/ci/Muslicast/Sources/PlaybackController.swift"}),
			}},
			want: Labels{Culprit: "PlaybackController.invoke (PlaybackController.swift)"},
		},
		"ProGuard type and frame retraced": {
			exception: &sourcemap.Exception{Type: "SourceException", ObfuscatedType: "bu4", Value: "Source error", Frames: []sourcemap.Frame{
				resolvedFrame(sourcemap.Position{Function: "a", InApp: true}, sourcemap.Position{Function: "com.example.player.Source.open", Filename: "Source.kt"}),
			}},
			want: Labels{Title: "SourceException: Source error", Culprit: "com.example.player.Source.open (Source.kt)"},
		},
		"the event message wins over the exception value in the title": {
			exception: &sourcemap.Exception{Type: "SourceException", ObfuscatedType: "bu4", Value: "Source error"},
			message:   "Playback failed\nsecond line",
			want:      Labels{Title: "SourceException: Playback failed"},
		},
		"JavaScript frame mapped by a source map": {
			exception: &sourcemap.Exception{Type: "TypeError", Value: "x is undefined", Frames: []sourcemap.Frame{
				resolvedFrame(sourcemap.Position{Function: "n", Filename: "https://shop.example/static/app.min.js", InApp: true}, sourcemap.Position{Function: "handleClick", Filename: "webpack:///src/Checkout.tsx"}),
			}},
			want: Labels{Culprit: "handleClick (Checkout.tsx)"},
		},
		"plain frames nothing resolved": {
			exception: &sourcemap.Exception{Type: "ValueError", Value: "bad", Frames: []sourcemap.Frame{
				{Generated: sourcemap.Position{Function: "charge", Filename: "billing.py", InApp: true}},
			}},
			want: Labels{},
		},
		"first in-app frame unresolved, a later one resolved": {
			exception: &sourcemap.Exception{Type: "EXC_BAD_ACCESS", Frames: []sourcemap.Frame{
				{Generated: sourcemap.Position{InApp: true}},
				resolvedFrame(sourcemap.Position{InApp: true}, sourcemap.Position{Function: "main", Filename: "main.c"}),
			}},
			want: Labels{},
		},
		"resolved frame without a file": {
			exception: &sourcemap.Exception{Type: "EXC_BAD_ACCESS", Frames: []sourcemap.Frame{
				resolvedFrame(sourcemap.Position{InApp: true}, sourcemap.Position{Function: "submit_order"}),
			}},
			want: Labels{Culprit: "submit_order"},
		},
		"no exception": {exception: nil, want: Labels{}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := LabelsFrom(tc.exception, tc.message); got != tc.want {
				t.Fatalf("LabelsFrom = %+v, want %+v", got, tc.want)
			}
		})
	}
}
