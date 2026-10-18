package native

import (
	"bytes"
	"context"
	"debug/macho"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/sourcemap"
)

func TestDecodeUploadThinAndUniversal(t *testing.T) {
	first := minimalMachO(macho.CpuArm64, [16]byte{1})
	upload, err := decodeBytes(first)
	if err != nil {
		t.Fatalf("DecodeUpload(thin): %v", err)
	}
	if len(upload.Objects) != 1 || upload.Objects[0].DebugID != "01000000-0000-0000-0000-000000000000" || upload.Objects[0].CPU != "arm64" || upload.Objects[0].VMAddr != 0x1000 {
		t.Fatalf("thin objects = %#v", upload.Objects)
	}

	second := minimalMachO(macho.CpuAmd64, [16]byte{2})
	fat := minimalFat(first, second)
	upload, err = decodeBytes(fat)
	if err != nil {
		t.Fatalf("DecodeUpload(fat): %v", err)
	}
	if len(upload.Objects) != 2 || upload.Objects[0].Index != 0 || upload.Objects[1].Index != 1 || upload.Objects[1].CPU != "x86_64" {
		t.Fatalf("fat objects = %#v", upload.Objects)
	}
}

func TestDecodeUploadRejectsMalformedAndNoDWARF(t *testing.T) {
	if _, err := decodeBytes([]byte("not macho")); !errors.Is(err, ErrInvalidDebugFile) {
		t.Fatalf("malformed error = %v", err)
	}
	body := minimalMachO(macho.CpuArm64, [16]byte{1})
	// Truncate a declared DWARF section while leaving the Mach-O headers valid.
	body = body[:len(body)-1]
	if _, err := decodeBytes(body); !errors.Is(err, ErrInvalidDebugFile) {
		t.Fatalf("no-DWARF error = %v", err)
	}
}

type memoryRepository struct {
	stored Stored
	err    error
}

func (r memoryRepository) Put(context.Context, int64, Upload) error { return nil }
func (r memoryRepository) Find(context.Context, int64, string) (Stored, error) {
	return r.stored, r.err
}

type fallbackResolver struct {
	called bool
	result *sourcemap.Exception
}

func (r *fallbackResolver) Resolve(context.Context, sourcemap.EventInput) (*sourcemap.Exception, error) {
	r.called = true
	return r.result, nil
}

func TestResolverTranslatesImageAddressAndPreservesFrameFlags(t *testing.T) {
	upload, err := decodeBytes(minimalMachO(macho.CpuArm64, [16]byte{1}))
	if err != nil {
		t.Fatal(err)
	}
	stored := Stored{BlobID: 1, DebugID: upload.Objects[0].DebugID, ObjectIndex: 0, VMAddr: upload.Objects[0].VMAddr, Compressed: upload.Compressed, UncompressedBytes: upload.UncompressedBytes}
	fallback := &fallbackResolver{}
	resolver := NewResolver(memoryRepository{stored: stored}, fallback)
	payload := []byte(`{"exception":{"values":[{"type":"Crash","value":"boom","stacktrace":{"frames":[{"instruction_addr":"0x100010","in_app":true}]}}]},"debug_meta":{"images":[{"type":"macho","debug_id":"01000000-0000-0000-0000-000000000000","image_addr":"0x100000","image_size":"0x1000"}]}}`)
	got, err := resolver.Resolve(t.Context(), sourcemap.EventInput{ProjectID: 7, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if fallback.called {
		t.Fatal("fallback called for resolved native event")
	}
	if got == nil || len(got.Frames) != 1 || got.Frames[0].Original == nil || got.Frames[0].Original.Function != "demo" || got.Frames[0].Original.Filename != "Demo.kt" || got.Frames[0].Original.Line != 42 || got.Frames[0].Original.Column != 7 || !got.Frames[0].Original.InApp {
		t.Fatalf("resolved exception = %#v original=%+v", got, got.Frames[0].Original)
	}
}

func TestResolverFallsBackForUnknownOrMalformedImages(t *testing.T) {
	want := &sourcemap.Exception{Type: "fallback"}
	for _, payload := range [][]byte{
		[]byte(`{"exception":{"values":[{"type":"Crash"}]},"debug_meta":{"images":[{"type":"macho","debug_id":"not-a-uuid"}]}}`),
		[]byte(`{"exception":{"values":[{"type":"Crash","stacktrace":{"frames":[{"instruction_addr":"bad"}]}}]},"debug_meta":{"images":[]}}`),
	} {
		fallback := &fallbackResolver{result: want}
		got, err := NewResolver(memoryRepository{err: ErrNotFound}, fallback).Resolve(t.Context(), sourcemap.EventInput{Payload: payload})
		if err != nil || got != want || !fallback.called {
			t.Fatalf("fallback result = %#v, %v, called=%v", got, err, fallback.called)
		}
	}
}

func TestResolverPreservesUnresolvedFramesAndReportsRepositoryFailures(t *testing.T) {
	upload, err := decodeBytes(minimalMachO(macho.CpuArm64, [16]byte{1}))
	if err != nil {
		t.Fatal(err)
	}
	stored := Stored{BlobID: 1, DebugID: upload.Objects[0].DebugID, ObjectIndex: 0, VMAddr: upload.Objects[0].VMAddr, Compressed: upload.Compressed, UncompressedBytes: upload.UncompressedBytes}
	payload := []byte(`{"exception":{"values":[{"type":"Crash","value":"boom","stacktrace":{"frames":[{"function":"kept","instruction_addr":"bad","in_app":false},{"instruction_addr":"0x100010","in_app":true}]}}]},"debug_meta":{"images":[{"type":"macho","debug_id":"01000000-0000-0000-0000-000000000000","image_addr":"0x100000","image_size":"0x1000"}]}}`)

	got, err := NewResolver(memoryRepository{stored: stored}, &fallbackResolver{}).Resolve(t.Context(), sourcemap.EventInput{ProjectID: 7, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got.Frames) != 2 || got.Frames[0].Original == nil || got.Frames[1].Generated.Function != "kept" || got.Frames[1].Generated.InApp {
		t.Fatalf("partial result = %#v", got)
	}

	want := &sourcemap.Exception{Type: "fallback"}
	fallback := &fallbackResolver{result: want}
	got, err = NewResolver(memoryRepository{err: errors.New("database offline")}, fallback).Resolve(t.Context(), sourcemap.EventInput{ProjectID: 7, Payload: payload})
	if got != want || !fallback.called || err == nil || !strings.Contains(err.Error(), "database offline") {
		t.Fatalf("failure result=%#v err=%v fallback=%v", got, err, fallback.called)
	}
}

func minimalMachO(cpu macho.Cpu, id [16]byte) []byte {
	abbrev := []byte{
		1, 0x11, 1, 0x11, 1, 0x12, 1, 0x10, 0x17, 0, 0,
		2, 0x2e, 0, 0x03, 0x08, 0x11, 1, 0x12, 1, 0, 0,
		0,
	}
	var entries bytes.Buffer
	entries.WriteByte(1)
	binary.Write(&entries, binary.LittleEndian, uint64(0x1000))
	binary.Write(&entries, binary.LittleEndian, uint64(0x1100))
	binary.Write(&entries, binary.LittleEndian, uint32(0))
	entries.WriteByte(2)
	entries.WriteString("demo")
	entries.WriteByte(0)
	binary.Write(&entries, binary.LittleEndian, uint64(0x1000))
	binary.Write(&entries, binary.LittleEndian, uint64(0x1100))
	entries.WriteByte(0)
	var info bytes.Buffer
	binary.Write(&info, binary.LittleEndian, uint32(2+4+1+entries.Len()))
	binary.Write(&info, binary.LittleEndian, uint16(4))
	binary.Write(&info, binary.LittleEndian, uint32(0))
	info.WriteByte(8)
	info.Write(entries.Bytes())
	line := minimalLineTable()

	const headerSize = 32
	const uuidSize = 24
	const textSize = 72
	const dwarfCommandSize = 72 + 3*80
	dataOffset := headerSize + uuidSize + textSize + dwarfCommandSize
	buf := bytes.NewBuffer(make([]byte, 0, dataOffset+len(info.Bytes())+len(abbrev)+len(line)))
	write := func(v any) { binary.Write(buf, binary.LittleEndian, v) }
	write(uint32(0xfeedfacf))
	write(uint32(cpu))
	write(uint32(0))
	write(uint32(0xa))
	write(uint32(3))
	write(uint32(uuidSize + textSize + dwarfCommandSize))
	write(uint32(0))
	write(uint32(0))
	write(uint32(lcUUID))
	write(uint32(uuidSize))
	buf.Write(id[:])
	writeSegment64(buf, "__TEXT", 0x1000, 0x1000, 0, 0, 0)
	writeSegment64(buf, "__DWARF", 0, uint64(len(info.Bytes())+len(abbrev)+len(line)), uint64(dataOffset), uint64(len(info.Bytes())+len(abbrev)+len(line)), 3)
	writeSection64(buf, "__debug_info", "__DWARF", 0, uint64(len(info.Bytes())), uint32(dataOffset))
	writeSection64(buf, "__debug_abbrev", "__DWARF", uint64(len(info.Bytes())), uint64(len(abbrev)), uint32(dataOffset+len(info.Bytes())))
	writeSection64(buf, "__debug_line", "__DWARF", uint64(len(info.Bytes())+len(abbrev)), uint64(len(line)), uint32(dataOffset+len(info.Bytes())+len(abbrev)))
	buf.Write(info.Bytes())
	buf.Write(abbrev)
	buf.Write(line)
	return buf.Bytes()
}

func minimalLineTable() []byte {
	var header bytes.Buffer
	header.Write([]byte{1, 1, 1, 0xfb, 14, 13})
	header.Write([]byte{0, 1, 1, 1, 1, 0, 0, 0, 1, 0, 0, 1})
	header.WriteByte(0) // include-directories terminator
	header.WriteString("Demo.kt")
	header.Write([]byte{0, 0, 0, 0, 0}) // filename terminator fields and table terminator

	var program bytes.Buffer
	program.Write([]byte{0, 9, 2}) // extended DW_LNE_set_address
	binary.Write(&program, binary.LittleEndian, uint64(0x1000))
	program.Write([]byte{4, 1, 3, 41, 5, 7, 1}) // file 1, line 42, column 7, copy
	program.Write([]byte{2, 0x80, 0x02})        // advance PC by 0x100
	program.Write([]byte{0, 1, 1})              // extended DW_LNE_end_sequence

	var table bytes.Buffer
	binary.Write(&table, binary.LittleEndian, uint32(2+4+header.Len()+program.Len()))
	binary.Write(&table, binary.LittleEndian, uint16(4))
	binary.Write(&table, binary.LittleEndian, uint32(header.Len()))
	table.Write(header.Bytes())
	table.Write(program.Bytes())
	return table.Bytes()
}

func writeName(buf *bytes.Buffer, name string) {
	var field [16]byte
	copy(field[:], name)
	buf.Write(field[:])
}
func writeSegment64(buf *bytes.Buffer, name string, addr, memsz, off, filesz uint64, sections uint32) {
	binary.Write(buf, binary.LittleEndian, uint32(0x19))
	binary.Write(buf, binary.LittleEndian, 72+80*sections)
	writeName(buf, name)
	for _, v := range []uint64{addr, memsz, off, filesz} {
		binary.Write(buf, binary.LittleEndian, v)
	}
	for _, v := range []uint32{7, 5, sections, 0} {
		binary.Write(buf, binary.LittleEndian, v)
	}
}
func writeSection64(buf *bytes.Buffer, section, segment string, addr, size uint64, off uint32) {
	writeName(buf, section)
	writeName(buf, segment)
	binary.Write(buf, binary.LittleEndian, addr)
	binary.Write(buf, binary.LittleEndian, size)
	binary.Write(buf, binary.LittleEndian, off)
	for range 7 {
		binary.Write(buf, binary.LittleEndian, uint32(0))
	}
}

func minimalFat(first, second []byte) []byte {
	off1 := uint32(8 + 2*20)
	off2 := off1 + uint32(len(first))
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(0xcafebabe))
	binary.Write(&buf, binary.BigEndian, uint32(2))
	for _, arch := range []struct {
		cpu       macho.Cpu
		off, size uint32
	}{{macho.CpuArm64, off1, uint32(len(first))}, {macho.CpuAmd64, off2, uint32(len(second))}} {
		binary.Write(&buf, binary.BigEndian, uint32(arch.cpu))
		binary.Write(&buf, binary.BigEndian, uint32(0))
		binary.Write(&buf, binary.BigEndian, arch.off)
		binary.Write(&buf, binary.BigEndian, arch.size)
		binary.Write(&buf, binary.BigEndian, uint32(0))
	}
	buf.Write(first)
	buf.Write(second)
	return buf.Bytes()
}

func decodeBytes(body []byte) (Upload, error) {
	return DecodeUpload(bytes.NewReader(body), int64(len(body)))
}
