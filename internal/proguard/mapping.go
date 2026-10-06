package proguard

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// maxLineBytes bounds one mapping line. A real R8 line stays far below it; the
// bound stops a hostile upload from making the scanner buffer without limit.
const maxLineBytes = 1 << 20

// ErrInvalidMapping reports text that is not an R8 or ProGuard mapping file.
var ErrInvalidMapping = errors.New("invalid proguard mapping")

// Mapping holds the parsed classes a caller asked Parse to keep.
type Mapping struct {
	classes map[string]*class
}

type class struct {
	original string
	methods  map[string][]method
}

// method is one mapping line for a method. The obfuscated range is the lines it
// covers in the shrunk code and the original range the lines they came from; a
// zero range means the mapping line carried none.
type method struct {
	class                          string
	name                           string
	obfuscatedStart, obfuscatedEnd int
	originalStart, originalEnd     int
}

func (m method) hasRange() bool { return m.obfuscatedEnd > 0 }

// Parse reads a mapping and keeps the classes whose obfuscated name wanted
// accepts. Everything else is skipped without being parsed, so retracing a few
// frames never holds a whole mapping in memory.
func Parse(reader io.Reader, wanted func(obfuscatedClass string) bool) (*Mapping, error) {
	return read(reader, wanted)
}

// Validate reports whether reader holds a mapping file, without keeping any of
// it. It checks structure, not that every member line means something.
func Validate(reader io.Reader) error {
	_, err := read(reader, func(string) bool { return false })
	return err
}

func read(reader io.Reader, wanted func(string) bool) (*Mapping, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	mapping := &Mapping{classes: map[string]*class{}}
	var current *class
	sawClass := false

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
		case line[0] == ' ' || line[0] == '\t':
			if !sawClass {
				return nil, fmt.Errorf("%w: member line before any class", ErrInvalidMapping)
			}
			if current != nil {
				current.addMember(trimmed)
			}
		default:
			original, obfuscated, ok := parseClassHeader(line)
			if !ok {
				return nil, fmt.Errorf("%w: unrecognised line", ErrInvalidMapping)
			}
			sawClass = true
			current = nil
			if wanted(obfuscated) {
				current = mapping.class(original, obfuscated)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidMapping, err)
	}
	if !sawClass {
		return nil, fmt.Errorf("%w: no class lines", ErrInvalidMapping)
	}
	return mapping, nil
}

// class returns the kept class for an obfuscated name, so a mapping that lists
// the same class in two blocks merges them.
func (m *Mapping) class(original, obfuscated string) *class {
	kept := m.classes[obfuscated]
	if kept == nil {
		kept = &class{original: original, methods: map[string][]method{}}
		m.classes[obfuscated] = kept
	}
	return kept
}

func parseClassHeader(line string) (original, obfuscated string, ok bool) {
	body, found := strings.CutSuffix(line, ":")
	if !found {
		return "", "", false
	}
	original, obfuscated, found = strings.Cut(body, " -> ")
	original, obfuscated = strings.TrimSpace(original), strings.TrimSpace(obfuscated)
	return original, obfuscated, found && original != "" && obfuscated != ""
}

// addMember records a method line and ignores fields and anything it cannot
// read: a member it skips only costs that member its retrace.
func (c *class) addMember(line string) {
	left, obfuscatedName, found := cutLast(line, " -> ")
	if !found || !strings.Contains(left, "(") {
		return
	}
	entry := method{class: c.original}

	if parts := strings.SplitN(left, ":", 3); len(parts) == 3 {
		start, startOK := atoi(parts[0])
		end, endOK := atoi(parts[1])
		if startOK && endOK {
			entry.obfuscatedStart, entry.obfuscatedEnd = start, end
			left = parts[2]
		}
	}
	closing := strings.IndexByte(left, ')')
	if closing < 0 {
		return
	}
	signature, tail := left[:closing+1], left[closing+1:]
	if tail != "" {
		if !entry.readOriginalLines(tail) {
			return
		}
	}
	head, _, _ := strings.Cut(signature, "(")
	name := head[strings.LastIndexByte(head, ' ')+1:]
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		entry.class, name = name[:dot], name[dot+1:]
	}
	if name == "" {
		return
	}
	entry.name = name
	c.methods[strings.TrimSpace(obfuscatedName)] = append(c.methods[strings.TrimSpace(obfuscatedName)], entry)
}

// readOriginalLines parses the ":start[:end]" suffix of a method line.
func (m *method) readOriginalLines(tail string) bool {
	numbers := strings.Split(strings.TrimPrefix(tail, ":"), ":")
	if len(numbers) > 2 {
		return false
	}
	start, ok := atoi(numbers[0])
	if !ok {
		return false
	}
	m.originalStart, m.originalEnd = start, start
	if len(numbers) == 2 {
		end, ok := atoi(numbers[1])
		if !ok {
			return false
		}
		m.originalEnd = end
	}
	return true
}

func cutLast(text, separator string) (before, after string, found bool) {
	index := strings.LastIndex(text, separator)
	if index < 0 {
		return text, "", false
	}
	return text[:index], text[index+len(separator):], true
}

func atoi(text string) (int, bool) {
	value, err := strconv.Atoi(text)
	return value, err == nil && value >= 0
}
