package native

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type cocoaBuild struct {
	stored    Stored
	images    []Image
	addresses []uint64
}

func loadCocoaBuild(t *testing.T, name string) cocoaBuild {
	t.Helper()
	directory := filepath.Join("..", "ingest", "testdata", "cocoa")
	body, err := os.ReadFile(filepath.Join(directory, name+".dwarf"))
	if err != nil {
		t.Fatal(err)
	}
	upload, err := DecodeUpload(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	object := upload.Objects[0]
	envelope, err := os.ReadFile(filepath.Join(directory, name+".envelope"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(envelope), "\n")
	var event struct {
		Exception struct {
			Values []struct {
				Stacktrace struct {
					Frames []struct {
						InstructionAddr address `json:"instruction_addr"`
						InApp           bool    `json:"in_app"`
					} `json:"frames"`
				} `json:"stacktrace"`
			} `json:"values"`
		} `json:"exception"`
		DebugMeta json.RawMessage `json:"debug_meta"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &event); err != nil {
		t.Fatalf("event of %s: %v", name, err)
	}
	build := cocoaBuild{
		stored: Stored{BlobID: 1, DebugID: object.DebugID, ObjectIndex: object.Index, VMAddr: object.VMAddr, Compressed: upload.Compressed, UncompressedBytes: upload.UncompressedBytes},
		images: ImagesFromDebugMeta(event.DebugMeta),
	}
	for _, frame := range event.Exception.Values[0].Stacktrace.Frames {
		if frame.InApp {
			build.addresses = append(build.addresses, frame.InstructionAddr.value)
		}
	}
	return build
}

func TestFunctionNamesNamesFramesWithAClangDebugFile(t *testing.T) {
	for _, name := range []string{"build-a", "build-b"} {
		t.Run(name, func(t *testing.T) {
			build := loadCocoaBuild(t, name)
			repository := &countingRepository{stored: build.stored}
			addresses := append(slices.Clone(build.addresses), 0x10)

			names, err := NewResolver(repository, &fallbackResolver{}).FunctionNames(t.Context(), 7, build.images, addresses)
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"main", "checkout", "submit_order", ""}; !slices.Equal(names, want) {
				t.Fatalf("names = %q, want %q", names, want)
			}
			if repository.finds != 1 || len(repository.marked) != 1 {
				t.Fatalf("finds=%d marked=%v, want one lookup and one use recorded for the image", repository.finds, repository.marked)
			}
		})
	}
}

func TestFunctionNamesLeavesFramesUnnamedWhenTheDebugFileIsMissingOrUnreadable(t *testing.T) {
	build := loadCocoaBuild(t, "build-a")
	resolver := NewResolver(memoryRepository{err: ErrNotFound}, &fallbackResolver{})
	names, err := resolver.FunctionNames(t.Context(), 7, build.images, build.addresses)
	if err != nil || !slices.Equal(names, []string{"", "", ""}) {
		t.Fatalf("missing debug file: names=%q err=%v, want unnamed frames and no error", names, err)
	}

	unreadable := build.stored
	unreadable.Compressed = []byte("not gzip")
	names, err = NewResolver(memoryRepository{stored: unreadable}, &fallbackResolver{}).FunctionNames(t.Context(), 7, build.images, build.addresses)
	if err == nil || !slices.Equal(names, []string{"", "", ""}) {
		t.Fatalf("unreadable debug file: names=%q err=%v, want unnamed frames and the error", names, err)
	}
	if errors.Is(err, ErrNotFound) || strings.Count(err.Error(), "read native debug file") != 1 {
		t.Fatalf("err = %v, want one read failure for the image", err)
	}
}
