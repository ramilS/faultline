package grouping

import (
	"regexp"
	"testing"
)

var hexFingerprint = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestFingerprintExceptionWithStacktraceIsInAppScoped(t *testing.T) {
	base := GroupingInput{Message: "boom", Exception: &Exception{
		Type: "IllegalStateException",
		Frames: []Frame{
			{Module: "java.base", Function: "run", InApp: false},
			{Module: "com.acme.Billing", Function: "total", InApp: true},
		},
	}}

	first := Fingerprint(base)
	if !hexFingerprint.MatchString(first.Fingerprint) {
		t.Fatalf("fingerprint = %q, want 64 lowercase hex", first.Fingerprint)
	}
	if Fingerprint(base).Fingerprint != first.Fingerprint {
		t.Fatal("fingerprint must be deterministic")
	}

	// A new non-in-app frame does not change grouping.
	withFramework := GroupingInput{Message: "boom", Exception: &Exception{
		Type: "IllegalStateException",
		Frames: []Frame{
			{Module: "org.springframework.web", Function: "handle", InApp: false},
			{Module: "java.base", Function: "run", InApp: false},
			{Module: "com.acme.Billing", Function: "total", InApp: true},
		},
	}}
	if Fingerprint(withFramework).Fingerprint != first.Fingerprint {
		t.Fatal("non-in-app frames must not affect the fingerprint")
	}

	// Changing the in-app frame changes grouping.
	moved := GroupingInput{Message: "boom", Exception: &Exception{
		Type:   "IllegalStateException",
		Frames: []Frame{{Module: "com.acme.Billing", Function: "recompute", InApp: true}},
	}}
	if Fingerprint(moved).Fingerprint == first.Fingerprint {
		t.Fatal("a different in-app frame must change the fingerprint")
	}
}

func TestFingerprintFallsBackToTopFramesWithoutInApp(t *testing.T) {
	frames := make([]Frame, 10)
	for index := range frames {
		frames[index] = Frame{Module: "lib", Function: string(rune('a' + index))}
	}
	withEight := GroupingInput{Exception: &Exception{Type: "E", Frames: frames[:8]}}
	withTen := GroupingInput{Exception: &Exception{Type: "E", Frames: frames}}
	if Fingerprint(withEight).Fingerprint != Fingerprint(withTen).Fingerprint {
		t.Fatal("only the top 8 frames matter when no frame is in-app")
	}
}

func TestFingerprintExceptionWithoutStacktraceIgnoresVolatileDetail(t *testing.T) {
	a := GroupingInput{Exception: &Exception{Type: "TimeoutException", Value: "timed out after 512 ms"}}
	b := GroupingInput{Exception: &Exception{Type: "TimeoutException", Value: "timed out after 37 ms"}}
	if Fingerprint(a).Fingerprint != Fingerprint(b).Fingerprint {
		t.Fatal("normalized values differing only by numbers must group together")
	}
	other := GroupingInput{Exception: &Exception{Type: "OtherException", Value: "timed out after 512 ms"}}
	if Fingerprint(a).Fingerprint == Fingerprint(other).Fingerprint {
		t.Fatal("different exception types must not group together")
	}
}

func TestFingerprintPlainMessage(t *testing.T) {
	a := GroupingInput{Logger: "billing", Message: "user 42 rejected"}
	b := GroupingInput{Logger: "billing", Message: "user 99 rejected"}
	if Fingerprint(a).Fingerprint != Fingerprint(b).Fingerprint {
		t.Fatal("plain messages differing only by numbers must group together")
	}
	differentLogger := GroupingInput{Logger: "payments", Message: "user 42 rejected"}
	if Fingerprint(a).Fingerprint == Fingerprint(differentLogger).Fingerprint {
		t.Fatal("logger must participate in plain-message grouping")
	}
}

func TestFingerprintTitleAndCulprit(t *testing.T) {
	withException := Fingerprint(GroupingInput{
		Message: "Invoice mismatch\nsecond line",
		Exception: &Exception{
			Type:   "IllegalStateException",
			Frames: []Frame{{Module: "com.acme.Billing", Function: "total", InApp: true}},
		},
	})
	if withException.Title != "IllegalStateException: Invoice mismatch" {
		t.Fatalf("title = %q", withException.Title)
	}
	if withException.Culprit != "com.acme.Billing.total" {
		t.Fatalf("culprit = %q", withException.Culprit)
	}

	plain := Fingerprint(GroupingInput{Message: "disk almost full\ndetails"})
	if plain.Title != "disk almost full" || plain.Culprit != "" {
		t.Fatalf("plain title/culprit = %q / %q", plain.Title, plain.Culprit)
	}
}

func nativeCrash(frames ...Frame) GroupingInput {
	return GroupingInput{Exception: &Exception{Type: "EXC_BAD_ACCESS", Frames: frames}}
}

func TestFingerprintSeparatesNativeFramesByImageOffset(t *testing.T) {
	load := nativeCrash(Frame{Package: "/x/MyApp.app/MyApp", ImageOffset: new(uint64(0xc000)), InApp: true})
	save := nativeCrash(Frame{Package: "/x/MyApp.app/MyApp", ImageOffset: new(uint64(0xd400)), InApp: true})
	bare := nativeCrash(Frame{InApp: true})

	if Fingerprint(load).Fingerprint == Fingerprint(save).Fingerprint {
		t.Fatal("native frames at different offsets must not share a fingerprint")
	}
	if Fingerprint(load).Fingerprint == Fingerprint(bare).Fingerprint {
		t.Fatal("a native frame must not share the fingerprint of an empty frame")
	}
}

func TestFingerprintKeepsNativeFrameStableAcrossLaunches(t *testing.T) {
	first := nativeCrash(Frame{Package: "/private/var/a/MyApp.app/MyApp", ImageOffset: new(uint64(0xc000)), InApp: true})
	relaunch := nativeCrash(Frame{Package: "/private/var/b/MyApp.app/MyApp", ImageOffset: new(uint64(0xc000)), InApp: true})

	if Fingerprint(first).Fingerprint != Fingerprint(relaunch).Fingerprint {
		t.Fatal("the same image offset must group together whatever the install path")
	}
}

func TestFingerprintCulpritNamesTheNativeFrame(t *testing.T) {
	result := Fingerprint(nativeCrash(Frame{Package: "/x/MyApp", ImageOffset: new(uint64(0xc000)), InApp: true}))

	if result.Culprit != "MyApp+0xc000" {
		t.Fatalf("culprit = %q, want MyApp+0xc000", result.Culprit)
	}
}
