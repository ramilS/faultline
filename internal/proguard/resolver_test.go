package proguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/sourcemap"
	"github.com/ramilS/faultline/internal/usage"
)

type fakeRepository struct {
	mappings map[string]Stored
	findErr  error
	finds    int
	marked   []string
	markErr  error
}

func (f *fakeRepository) MarkUsed(_ context.Context, _ int64, mappingUUID string) error {
	f.marked = append(f.marked, mappingUUID)
	return f.markErr
}

func (f *fakeRepository) Put(context.Context, int64, string, DecodedUpload) (bool, error) {
	return false, errors.New("unused")
}

func (f *fakeRepository) Find(_ context.Context, _ int64, mappingUUID string) (Stored, error) {
	f.finds++
	if f.findErr != nil {
		return Stored{}, f.findErr
	}
	stored, ok := f.mappings[mappingUUID]
	if !ok {
		return Stored{}, ErrNotFound
	}
	return stored, nil
}

type recordingFallback struct {
	calls  int
	result *sourcemap.Exception
	err    error
}

func (r *recordingFallback) Resolve(context.Context, sourcemap.EventInput) (*sourcemap.Exception, error) {
	r.calls++
	return r.result, r.err
}

func storedMapping(t *testing.T, text string) Stored {
	t.Helper()
	upload, err := DecodeUpload(strings.NewReader(text))
	if err != nil {
		t.Fatalf("DecodeUpload: %v", err)
	}
	return Stored{Compressed: upload.Compressed, UncompressedBytes: upload.UncompressedBytes}
}

const secondMappingUUID = "11111111-2222-5333-8444-555555555555"

// androidEvent builds a sentry-java style event. Frames are given caller-first,
// the order Sentry sends, and every frame names the mapping image.
func androidEvent(images string, values ...string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"exception":{"values":[%s]},"debug_meta":{"images":%s}}`, strings.Join(values, ","), images))
}

func proguardImages(uuids ...string) string {
	var images []string
	for _, id := range uuids {
		images = append(images, fmt.Sprintf(`{"type":"proguard","uuid":%q}`, id))
	}
	return "[" + strings.Join(images, ",") + "]"
}

func exceptionValue(typ, message string, frames ...string) string {
	return fmt.Sprintf(`{"type":%q,"value":%q,"stacktrace":{"frames":[%s]}}`, typ, message, strings.Join(frames, ","))
}

func frame(module, function, filename string, line int) string {
	return fmt.Sprintf(`{"module":%q,"function":%q,"filename":%q,"lineno":%d,"in_app":true}`, module, function, filename, line)
}

func resolve(t *testing.T, repo *fakeRepository, fallback *recordingFallback, payload json.RawMessage) (*sourcemap.Exception, error) {
	t.Helper()
	return NewResolver(repo, fallback).Resolve(t.Context(), sourcemap.EventInput{ProjectID: 7, Payload: payload})
}

func TestResolverRetracesFramesErrorSiteFirst(t *testing.T) {
	repo := &fakeRepository{mappings: map[string]Stored{mappingUUID: storedMapping(t, shopMapping)}}
	fallback := &recordingFallback{}
	payload := androidEvent(proguardImages(mappingUUID), exceptionValue("java.lang.IllegalStateException", "boom",
		frame("a.b", "b", "SourceFile", 7),
		frame("a.a", "c", "SourceFile", 12),
	))

	exception, err := resolve(t, repo, fallback, payload)

	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if fallback.calls != 0 {
		t.Fatal("an event with a mapping must not reach the fallback resolver")
	}
	if exception.Type != "java.lang.IllegalStateException" || exception.Value != "boom" || len(exception.Frames) != 2 {
		t.Fatalf("exception = %+v", exception)
	}
	site := exception.Frames[0]
	if site.Generated.Function != "c" || site.Generated.Line != 12 {
		t.Fatalf("generated = %+v, want the reported location", site.Generated)
	}
	want := sourcemap.Position{Function: "com.acme.shop.Cart.load", Filename: "Cart.java", Line: 102, InApp: true}
	if site.Original == nil || *site.Original != want {
		t.Fatalf("original = %+v, want %+v", site.Original, want)
	}
	caller := exception.Frames[1]
	if caller.Original == nil || caller.Original.Function != "com.acme.shop.Checkout.pay" || caller.Original.Line != 31 {
		t.Fatalf("caller original = %+v", caller.Original)
	}
}

func TestResolverExpandsInlinedFramesInnermostFirst(t *testing.T) {
	repo := &fakeRepository{mappings: map[string]Stored{mappingUUID: storedMapping(t, shopMapping)}}
	payload := androidEvent(proguardImages(mappingUUID), exceptionValue("E", "m", frame("a.b", "a", "SourceFile", 5)))

	exception, err := resolve(t, repo, &recordingFallback{}, payload)

	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(exception.Frames) != 2 {
		t.Fatalf("frames = %d, want 2 for an inlined call", len(exception.Frames))
	}
	inner, outer := exception.Frames[0], exception.Frames[1]
	if inner.Original.Function != "com.acme.shop.Cart.total" || inner.Original.Filename != "Cart.java" || inner.Original.Line != 201 {
		t.Fatalf("inner = %+v", inner.Original)
	}
	if outer.Original.Function != "com.acme.shop.Checkout.submit" || outer.Original.Line != 14 {
		t.Fatalf("outer = %+v", outer.Original)
	}
	if inner.Generated != outer.Generated {
		t.Fatalf("both frames must keep the one reported location: %+v vs %+v", inner.Generated, outer.Generated)
	}
}

func TestResolverKeepsARealSDKFileNameForTheReportedClassOnly(t *testing.T) {
	repo := &fakeRepository{mappings: map[string]Stored{mappingUUID: storedMapping(t, shopMapping)}}
	payload := androidEvent(proguardImages(mappingUUID), exceptionValue("E", "m", frame("a.b", "a", "Checkout.kt", 5)))

	exception, err := resolve(t, repo, &recordingFallback{}, payload)

	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	inner, outer := exception.Frames[0].Original, exception.Frames[1].Original
	if inner.Filename != "Cart.java" {
		t.Fatalf("an inlined frame belongs to another class, so it must not take the reported file: %q", inner.Filename)
	}
	if outer.Filename != "Checkout.kt" {
		t.Fatalf("filename = %q, want the SDK's real file name for the reported class", outer.Filename)
	}
}

func TestResolverFallsBackWhenThereIsNothingToApply(t *testing.T) {
	sentinel := &sourcemap.Exception{Type: "from fallback"}
	frameJSON := frame("a.a", "c", "SourceFile", 12)
	for _, test := range []struct {
		name    string
		payload json.RawMessage
		repo    *fakeRepository
	}{
		{"no proguard image", androidEvent(`[{"type":"macho","debug_id":"x"}]`, exceptionValue("E", "m", frameJSON)), &fakeRepository{}},
		{"no debug_meta", json.RawMessage(`{"exception":{"values":[` + exceptionValue("E", "m", frameJSON) + `]}}`), &fakeRepository{}},
		{"mapping never uploaded", androidEvent(proguardImages(mappingUUID), exceptionValue("E", "m", frameJSON)), &fakeRepository{}},
		{"uuid that is not a uuid", androidEvent(proguardImages("not-a-uuid"), exceptionValue("E", "m", frameJSON)), &fakeRepository{}},
		{"images of the wrong type", androidEvent(`"oops"`, exceptionValue("E", "m", frameJSON)), &fakeRepository{}},
		{"payload that is not an object", json.RawMessage(`[]`), &fakeRepository{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fallback := &recordingFallback{result: sentinel}

			exception, err := resolve(t, test.repo, fallback, test.payload)

			if err != nil || exception != sentinel || fallback.calls != 1 {
				t.Fatalf("got %+v, %v with %d fallback calls; want the fallback's result once", exception, err, fallback.calls)
			}
		})
	}
}

// TestResolverFallsBackWhenNoFrameMatchesAnyStoredMapping covers a hybrid
// event: its debug_meta names a real, stored proguard mapping (unlike the
// "mapping never uploaded" case, which is handled before any frame is looked
// at), but none of its frames' classes are in that mapping — plausible for an
// event whose stack is not JVM at all, such as a native+JS hybrid app crash
// that happens to carry a stale or irrelevant proguard image tag. The event
// must still get ordinary source-map resolution rather than being left with
// every frame unretraced.
func TestResolverFallsBackWhenNoFrameMatchesAnyStoredMapping(t *testing.T) {
	repo := &fakeRepository{mappings: map[string]Stored{mappingUUID: storedMapping(t, shopMapping)}}
	sentinel := &sourcemap.Exception{Type: "from fallback"}
	fallback := &recordingFallback{result: sentinel}
	payload := androidEvent(proguardImages(mappingUUID), exceptionValue("TypeError", "m", frame("app.js", "onClick", "app.js", 12)))

	exception, err := resolve(t, repo, fallback, payload)

	if err != nil || exception != sentinel || fallback.calls != 1 {
		t.Fatalf("got %+v, %v with %d fallback calls; want the fallback's result once", exception, err, fallback.calls)
	}
}

func TestResolverLeavesFramesTheMappingDoesNotKnow(t *testing.T) {
	repo := &fakeRepository{mappings: map[string]Stored{mappingUUID: storedMapping(t, shopMapping)}}
	payload := androidEvent(proguardImages(mappingUUID), exceptionValue("E", "m",
		frame("x.y", "z", "SourceFile", 3),
		frame("a.a", "c", "SourceFile", 12),
	))

	exception, err := resolve(t, repo, &recordingFallback{}, payload)

	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if exception.Frames[0].Original == nil {
		t.Fatal("the mapped frame must still be retraced")
	}
	unmapped := exception.Frames[1]
	if unmapped.Original != nil || unmapped.Generated.Function != "z" {
		t.Fatalf("an unmapped class must stay as reported: %+v", unmapped)
	}
}

func TestResolverTriesEveryMappingTheEventNames(t *testing.T) {
	second := "com.other.Lib -> q.r:\n    1:1:void go():40:40 -> a\n"
	repo := &fakeRepository{mappings: map[string]Stored{
		mappingUUID:       storedMapping(t, shopMapping),
		secondMappingUUID: storedMapping(t, second),
	}}
	payload := androidEvent(proguardImages(mappingUUID, secondMappingUUID), exceptionValue("E", "m",
		frame("q.r", "a", "SourceFile", 1),
		frame("a.a", "c", "SourceFile", 12),
	))

	exception, err := resolve(t, repo, &recordingFallback{}, payload)

	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if exception.Frames[0].Original.Function != "com.acme.shop.Cart.load" || exception.Frames[1].Original.Function != "com.other.Lib.go" {
		t.Fatalf("frames = %+v / %+v", exception.Frames[0].Original, exception.Frames[1].Original)
	}
}

func TestResolverBuildsTheCauseChainOutermostFirst(t *testing.T) {
	repo := &fakeRepository{mappings: map[string]Stored{mappingUUID: storedMapping(t, shopMapping)}}
	payload := androidEvent(proguardImages(mappingUUID),
		exceptionValue("Inner", "root cause", frame("a.a", "c", "SourceFile", 12)),
		exceptionValue("Outer", "wrapper", frame("a.b", "b", "SourceFile", 7)),
	)

	exception, err := resolve(t, repo, &recordingFallback{}, payload)

	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if exception.Type != "Outer" || exception.Cause == nil || exception.Cause.Type != "Inner" || exception.Cause.Cause != nil {
		t.Fatalf("chain = %+v", exception)
	}
	if exception.Cause.Frames[0].Original == nil || exception.Cause.Frames[0].Original.Function != "com.acme.shop.Cart.load" {
		t.Fatal("the cause's frames must be retraced too")
	}
}

// TestResolverPrefersAGenuineMatchOverAnEarlierClassOnlyFallback covers the
// collision R8 makes routine: each module's obfuscator allocates short names
// ("a.a") independently, so two unrelated mappings commonly reuse one. The
// first mapping here knows the class "a.a" but not this method, so its
// Retrace is a class-only fallback that happens to look exactly like a real
// unobfuscated match (frame.Method carried through unchanged, as it always is
// for a constructor). The second mapping actually resolves it.
func TestResolverPrefersAGenuineMatchOverAnEarlierClassOnlyFallback(t *testing.T) {
	first := "com.other.Unrelated -> a.a:\n    1:1:void run():9:9 -> x\n"
	second := shopMapping
	repo := &fakeRepository{mappings: map[string]Stored{
		mappingUUID:       storedMapping(t, first),
		secondMappingUUID: storedMapping(t, second),
	}}
	payload := androidEvent(proguardImages(mappingUUID, secondMappingUUID), exceptionValue("E", "m", frame("a.a", "<init>", "SourceFile", 9)))

	exception, err := resolve(t, repo, &recordingFallback{}, payload)

	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if exception.Frames[0].Original == nil || exception.Frames[0].Original.Function != "com.acme.shop.Cart.<init>" {
		t.Fatalf("original = %+v, want the second mapping's real <init> match, not the first's same-named class-only fallback", exception.Frames[0].Original)
	}
}

func TestResolverReportsButSurvivesUnreadableMappings(t *testing.T) {
	corrupt := Stored{Compressed: []byte("not gzip")}
	repo := &fakeRepository{mappings: map[string]Stored{mappingUUID: corrupt}}
	payload := androidEvent(proguardImages(mappingUUID), exceptionValue("E", "m", frame("a.a", "c", "SourceFile", 12)))
	fallback := &recordingFallback{result: &sourcemap.Exception{Type: "E"}}

	exception, err := resolve(t, repo, fallback, payload)

	if err == nil {
		t.Fatal("a corrupt stored mapping must be reported")
	}
	if exception == nil {
		t.Fatal("the event must stay readable")
	}
}

// TestResolverRejectsAStoredMappingOverTheCurrentLimit covers a stored mapping
// that was valid when uploaded but now exceeds MaxDecodedBytes because a later
// release lowered it: it must be reported and skipped, not silently truncated
// and parsed from the piece that fits.
func TestResolverRejectsAStoredMappingOverTheCurrentLimit(t *testing.T) {
	oversized := storedMapping(t, shopMapping)
	oversized.UncompressedBytes = MaxDecodedBytes + 1
	repo := &fakeRepository{mappings: map[string]Stored{mappingUUID: oversized}}
	payload := androidEvent(proguardImages(mappingUUID), exceptionValue("E", "m", frame("a.a", "c", "SourceFile", 12)))
	fallback := &recordingFallback{result: &sourcemap.Exception{Type: "E"}}

	exception, err := resolve(t, repo, fallback, payload)

	if err == nil {
		t.Fatal("an over-limit stored mapping must be reported")
	}
	if exception != fallback.result {
		t.Fatalf("exception = %+v, want the fallback's result, not a partially-parsed mapping", exception)
	}
}

func TestResolverReportsRepositoryFailures(t *testing.T) {
	repo := &fakeRepository{findErr: errors.New("database down")}
	payload := androidEvent(proguardImages(mappingUUID), exceptionValue("E", "m", frame("a.a", "c", "SourceFile", 12)))
	fallback := &recordingFallback{result: &sourcemap.Exception{Type: "E"}}

	exception, err := resolve(t, repo, fallback, payload)

	if err == nil || exception == nil {
		t.Fatalf("got %+v, %v; want the fallback's exception and the failure", exception, err)
	}
}

func TestResolverNeverAsksForMoreMappingsThanItReads(t *testing.T) {
	var uuids []string
	for i := range maxMappingsPerEvent + 5 {
		uuids = append(uuids, fmt.Sprintf("00000000-0000-5000-8000-%012d", i))
	}
	repo := &fakeRepository{}
	payload := androidEvent(proguardImages(uuids...), exceptionValue("E", "m", frame("a.a", "c", "SourceFile", 12)))

	if _, err := resolve(t, repo, &recordingFallback{}, payload); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if repo.finds != maxMappingsPerEvent {
		t.Fatalf("finds = %d, want the cap of %d", repo.finds, maxMappingsPerEvent)
	}
}

func TestResolverReusesParsedMappingForRepeatedReads(t *testing.T) {
	repo := &fakeRepository{mappings: map[string]Stored{mappingUUID: storedMapping(t, shopMapping)}}
	resolver := NewResolver(repo, &recordingFallback{})
	payload := androidEvent(proguardImages(mappingUUID), exceptionValue("E", "m", frame("a.a", "c", "SourceFile", 12)))

	for range 3 {
		exception, err := resolver.Resolve(t.Context(), sourcemap.EventInput{ProjectID: 7, Payload: payload})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got := exception.Frames[0].Original; got == nil || got.Function != "com.acme.shop.Cart.load" {
			t.Fatalf("original = %+v", got)
		}
	}

	if repo.finds != 1 {
		t.Fatalf("finds = %d, want 1: later reads must be served from the cache", repo.finds)
	}
}

func TestResolverReparsesWhenAnEventMentionsANewClassAndKeepsTheOldOnes(t *testing.T) {
	repo := &fakeRepository{mappings: map[string]Stored{mappingUUID: storedMapping(t, shopMapping)}}
	resolver := NewResolver(repo, &recordingFallback{})
	resolveClass := func(module, function string, line int) *sourcemap.Frame {
		payload := androidEvent(proguardImages(mappingUUID), exceptionValue("E", "m", frame(module, function, "SourceFile", line)))
		exception, err := resolver.Resolve(t.Context(), sourcemap.EventInput{ProjectID: 7, Payload: payload})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		return &exception.Frames[0]
	}

	resolveClass("a.a", "c", 12)
	if got := resolveClass("a.b", "b", 7); got.Original == nil || got.Original.Function != "com.acme.shop.Checkout.pay" {
		t.Fatalf("new class original = %+v", got.Original)
	}
	if got := resolveClass("a.a", "c", 12); got.Original == nil || got.Original.Function != "com.acme.shop.Cart.load" {
		t.Fatalf("first class must still retrace after the merge: %+v", got.Original)
	}

	if repo.finds != 2 {
		t.Fatalf("finds = %d, want 2: one per newly requested class, none for the repeat", repo.finds)
	}
}

func TestResolverCachesMappingsPerProject(t *testing.T) {
	repo := &fakeRepository{mappings: map[string]Stored{mappingUUID: storedMapping(t, shopMapping)}}
	resolver := NewResolver(repo, &recordingFallback{})
	payload := androidEvent(proguardImages(mappingUUID), exceptionValue("E", "m", frame("a.a", "c", "SourceFile", 12)))

	for _, projectID := range []int64{7, 8} {
		if _, err := resolver.Resolve(t.Context(), sourcemap.EventInput{ProjectID: projectID, Payload: payload}); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
	}

	if repo.finds != 2 {
		t.Fatalf("finds = %d, want 2: a cached mapping must not be shared across projects", repo.finds)
	}
}

func TestResolverDoesNotCacheAMappingThatIsNotStoredYet(t *testing.T) {
	repo := &fakeRepository{mappings: map[string]Stored{}}
	resolver := NewResolver(repo, &recordingFallback{})
	payload := androidEvent(proguardImages(mappingUUID), exceptionValue("E", "m", frame("a.a", "c", "SourceFile", 12)))

	if _, err := resolver.Resolve(t.Context(), sourcemap.EventInput{ProjectID: 7, Payload: payload}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	repo.mappings[mappingUUID] = storedMapping(t, shopMapping)
	exception, err := resolver.Resolve(t.Context(), sourcemap.EventInput{ProjectID: 7, Payload: payload})

	if err != nil || exception.Frames[0].Original == nil {
		t.Fatalf("a mapping uploaded after the first read must be picked up: %+v, %v", exception, err)
	}
}

func TestMappingCacheEvictsLeastRecentlyUsedEntry(t *testing.T) {
	cache := newMappingCache(2)
	classes := map[string]struct{}{"a.a": {}}
	for _, key := range []string{"one", "two"} {
		cache.put(key, &Mapping{}, classes)
	}
	if _, ok := cache.get("one", classes); !ok {
		t.Fatal("one must still be cached")
	}
	cache.put("three", &Mapping{}, classes)

	if _, ok := cache.get("two", classes); ok {
		t.Fatal("two was least recently used and must be evicted")
	}
	if _, ok := cache.get("one", classes); !ok {
		t.Fatal("one was refreshed by get and must survive")
	}
}

func TestMappingCacheForgetsCoverageBeyondItsClassBudget(t *testing.T) {
	cache := newMappingCache(2)
	seen := map[string]struct{}{}
	for index := range maxCoveredClasses {
		seen[fmt.Sprintf("c%d", index)] = struct{}{}
	}
	cache.put("m", &Mapping{}, seen)

	wanted := cache.coverageFor("m", map[string]struct{}{"extra": {}})

	if len(wanted) != 1 {
		t.Fatalf("coverage = %d classes, want only the requested one once the budget is exceeded", len(wanted))
	}
}

func TestResolverRetracesAnExceptionSentAsABareArray(t *testing.T) {
	repo := &fakeRepository{mappings: map[string]Stored{mappingUUID: storedMapping(t, shopMapping)}}
	fallback := &recordingFallback{}
	value := exceptionValue("java.lang.IllegalStateException", "boom", frame("a.b", "b", "SourceFile", 7), frame("a.a", "c", "SourceFile", 12))
	payload := json.RawMessage(fmt.Sprintf(`{"exception":[%s],"debug_meta":{"images":%s}}`, value, proguardImages(mappingUUID)))

	exception, err := resolve(t, repo, fallback, payload)

	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if fallback.calls != 0 || exception == nil || len(exception.Frames) != 2 {
		t.Fatalf("got %+v with %d fallback calls; want a retraced exception without fallback", exception, fallback.calls)
	}
	if site := exception.Frames[0]; site.Original == nil || site.Original.Function != "com.acme.shop.Cart.load" {
		t.Fatalf("original = %+v", site.Original)
	}
}

type fakeClock struct{ at time.Time }

func (c *fakeClock) now() time.Time { return c.at }

// resolverAt is a resolver whose use recording runs on clock.
func resolverAt(repo *fakeRepository, clock *fakeClock) *resolver {
	r := NewResolver(repo, &recordingFallback{}).(*resolver)
	r.used = usage.NewThrottle(usage.Interval, clock.now)
	return r
}

// Retention keeps the mappings crashes still need, so a mapping served from the
// cache must keep counting as used, without a write for every event.
func TestResolverRecordsAMappingAsUsedOncePerInterval(t *testing.T) {
	repo := &fakeRepository{mappings: map[string]Stored{mappingUUID: storedMapping(t, shopMapping)}}
	clock := &fakeClock{at: time.Unix(1_000, 0)}
	r := resolverAt(repo, clock)
	input := sourcemap.EventInput{ProjectID: 7, Payload: androidEvent(proguardImages(mappingUUID), exceptionValue("E", "m", frame("a.a", "c", "SourceFile", 12)))}

	for range 2 {
		if _, err := r.Resolve(t.Context(), input); err != nil {
			t.Fatal(err)
		}
	}
	if len(repo.marked) != 1 {
		t.Fatalf("marked %v within one interval, want once", repo.marked)
	}
	clock.at = clock.at.Add(usage.Interval)
	if _, err := r.Resolve(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if len(repo.marked) != 2 || repo.finds != 1 {
		t.Fatalf("marked %v after %d finds, want a cached mapping marked again", repo.marked, repo.finds)
	}
}

func TestResolverReportsAFailedUseRecordAndRetriesIt(t *testing.T) {
	repo := &fakeRepository{mappings: map[string]Stored{mappingUUID: storedMapping(t, shopMapping)}, markErr: errors.New("database offline")}
	r := resolverAt(repo, &fakeClock{at: time.Unix(1_000, 0)})
	input := sourcemap.EventInput{ProjectID: 7, Payload: androidEvent(proguardImages(mappingUUID), exceptionValue("E", "m", frame("a.a", "c", "SourceFile", 12)))}

	exception, err := r.Resolve(t.Context(), input)
	if err == nil || !strings.Contains(err.Error(), "database offline") || exception == nil || exception.Frames[0].Original == nil {
		t.Fatalf("exception=%#v err=%v, want it retraced and the failed record reported", exception, err)
	}
	repo.markErr = nil
	if _, err := r.Resolve(t.Context(), input); err != nil || len(repo.marked) != 2 {
		t.Fatalf("marked %v err=%v, want the failed record retried", repo.marked, err)
	}
}
