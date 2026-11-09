package grouping

import (
	"errors"
	"slices"
	"testing"
)

func nativeFrame(offset, address uint64) Frame {
	return Frame{Package: "/private/var/a/MyApp.app/MyApp", ImageOffset: new(offset), InstructionAddr: new(address), InApp: true}
}

func namesBy(table map[uint64]string) SymbolLookup {
	return func(addresses []uint64) ([]string, error) {
		names := make([]string, len(addresses))
		for index, address := range addresses {
			names[index] = table[address]
		}
		return names, nil
	}
}

func symbolicated(t *testing.T, input GroupingInput, lookup SymbolLookup) Result {
	t.Helper()
	named, err := WithSymbols(input, lookup)
	if err != nil {
		t.Fatalf("WithSymbols: %v", err)
	}
	return Fingerprint(named)
}

func TestFingerprintGroupsOneFunctionAcrossBuilds(t *testing.T) {
	buildA := nativeCrash(nativeFrame(0xc000, 0x104c0c014), nativeFrame(0xb000, 0x104c0b020))
	buildB := nativeCrash(nativeFrame(0xd400, 0x102b0d418), nativeFrame(0xb800, 0x102b0b830))
	names := namesBy(map[uint64]string{
		0x104c0c014: "-[CheckoutController submit:]", 0x104c0b020: "main",
		0x102b0d418: "-[CheckoutController submit:]", 0x102b0b830: "main",
	})

	first, second := symbolicated(t, buildA, names), symbolicated(t, buildB, names)

	if first.Fingerprint != second.Fingerprint {
		t.Fatal("the same functions at different offsets must share a fingerprint")
	}
	if first.LegacyFingerprint != Fingerprint(buildA).Fingerprint || second.LegacyFingerprint != Fingerprint(buildB).Fingerprint {
		t.Fatal("the legacy fingerprint must be the offset fingerprint each build had before")
	}
	if first.Culprit != "-[CheckoutController submit:]" {
		t.Fatalf("culprit = %q, want the symbol", first.Culprit)
	}
}

func TestFingerprintKeysSymbolsWithoutTheAddressInsideTheFunction(t *testing.T) {
	early := nativeCrash(nativeFrame(0xc000, 0x100c014))
	late := nativeCrash(nativeFrame(0xc000, 0x100c0f0))
	names := namesBy(map[uint64]string{0x100c014: "submit", 0x100c0f0: "submit"})

	if symbolicated(t, early, names).Fingerprint != symbolicated(t, late, names).Fingerprint {
		t.Fatal("two call sites in one function must share a fingerprint")
	}
}

func TestFingerprintKeepsTheExceptionTypeBesideSymbols(t *testing.T) {
	names := namesBy(map[uint64]string{0x100c014: "submit"})
	segv := symbolicated(t, nativeCrash(nativeFrame(0xc000, 0x100c014)), names)
	abort := symbolicated(t, GroupingInput{Exception: &Exception{Type: "SIGABRT", Frames: []Frame{nativeFrame(0xc000, 0x100c014)}}}, names)

	if segv.Fingerprint == abort.Fingerprint {
		t.Fatal("different exception types in one function must not share a fingerprint")
	}
}

func TestFingerprintFallsBackToTheOffsetForAFrameNothingNames(t *testing.T) {
	input := nativeCrash(nativeFrame(0xc000, 0x100c014))
	failed := func([]uint64) ([]string, error) { return nil, errors.New("debug file unreadable") }

	named, err := WithSymbols(input, failed)
	if err == nil {
		t.Fatal("the lookup error must be returned for reporting")
	}
	got := Fingerprint(named)
	if got.Fingerprint != Fingerprint(input).Fingerprint || got.LegacyFingerprint != "" {
		t.Fatalf("result = %+v, want the offset fingerprint and no legacy", got)
	}
	if unnamed := symbolicated(t, input, namesBy(nil)); unnamed.Fingerprint != got.Fingerprint || unnamed.LegacyFingerprint != "" {
		t.Fatalf("result = %+v, want the offset fingerprint for an unnamed address", unnamed)
	}
}

func TestWithSymbolsLooksUpOnlyUnnamedGroupingFrames(t *testing.T) {
	frames := []Frame{
		{Module: "com.acme.Billing", Function: "total", InstructionAddr: new(uint64(1)), InApp: true},
		nativeFrame(0xc000, 2),
		{Package: "libsystem_kernel.dylib", ImageOffset: new(uint64(0x10)), InstructionAddr: new(uint64(3))},
		nativeFrame(0xc100, 4),
	}
	var asked []uint64
	record := func(addresses []uint64) ([]string, error) {
		asked = slices.Clone(addresses)
		return []string{"two", "four"}, nil
	}
	input := nativeCrash(frames...)

	named, err := WithSymbols(input, record)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(asked, []uint64{2, 4}) {
		t.Fatalf("looked up %v, want only the in-app frames without a function", asked)
	}
	if named.Exception.Frames[1].Symbol != "two" || named.Exception.Frames[3].Symbol != "four" || named.Exception.Frames[0].Symbol != "" {
		t.Fatalf("frames = %+v", named.Exception.Frames)
	}
	if input.Exception.Frames[1].Symbol != "" {
		t.Fatal("WithSymbols must not modify its input")
	}

	called := false
	_, _ = WithSymbols(GroupingInput{Exception: &Exception{Type: "E", Frames: frames[:1]}}, func([]uint64) ([]string, error) {
		called = true
		return nil, nil
	})
	if called {
		t.Fatal("an event whose frames all have names must not be looked up")
	}
}

func TestSymbolicatedCulpritIsSetOnlyWhenASymbolNamesTheCulpritFrame(t *testing.T) {
	names := namesBy(map[uint64]string{0x100c014: "submit_order", 0x100b020: "main"})
	cases := map[string]struct {
		input GroupingInput
		want  string
	}{
		"culprit frame named by its symbol": {
			input: nativeCrash(nativeFrame(0xc000, 0x100c014), nativeFrame(0xb000, 0x100b020)),
			want:  "submit_order",
		},
		"culprit frame without a symbol, a later frame with one": {
			input: nativeCrash(nativeFrame(0xa000, 0x100a000), nativeFrame(0xb000, 0x100b020)),
			want:  "",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := symbolicated(t, tc.input, names).SymbolicatedCulprit; got != tc.want {
				t.Fatalf("SymbolicatedCulprit = %q, want %q", got, tc.want)
			}
		})
	}
}
