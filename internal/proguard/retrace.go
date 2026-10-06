package proguard

import "strings"

// Frame is a stack frame as the app reported it: obfuscated class and method,
// and a line number that is zero when the frame carried none.
type Frame struct {
	Class  string
	Method string
	Line   int
}

// Original is one frame of the un-obfuscated stack. Inlined marks a frame that
// belongs to a method the optimizer inlined into the reported one, so its
// class differs from the class the reported frame was found in. Resolved is
// false only for the last-resort case: the class is known but its method or
// line is not, so Method and Line are copied from the reported frame rather
// than looked up.
type Original struct {
	Class    string
	Method   string
	Line     int
	Inlined  bool
	Resolved bool
}

// Retrace maps an obfuscated frame to the original frames it stands for,
// innermost first. It reports false when the class is not in the mapping. A
// known class with an unknown method, or an ambiguous one, still yields the
// original class with the reported method name and line.
func (m *Mapping) Retrace(frame Frame) ([]Original, bool) {
	mapped := m.classes[frame.Class]
	if mapped == nil {
		return nil, false
	}
	candidates := mapped.methods[frame.Method]

	if frame.Line > 0 {
		if ranged := rangedMatches(candidates, frame.Line); len(ranged) > 0 {
			originals := make([]Original, 0, len(ranged))
			for _, entry := range ranged {
				originals = append(originals, entry.original(mapped, frame.Line))
			}
			return originals, true
		}
		candidates = unranged(candidates)
	}
	if single, ok := singleMethod(candidates); ok {
		return []Original{single.original(mapped, frame.Line)}, true
	}
	return []Original{{Class: mapped.original, Method: frame.Method, Line: frame.Line, Resolved: false}}, true
}

func rangedMatches(candidates []method, line int) []method {
	var matches []method
	for _, entry := range candidates {
		if entry.hasRange() && entry.obfuscatedStart <= line && line <= entry.obfuscatedEnd {
			matches = append(matches, entry)
		}
	}
	return matches
}

func unranged(candidates []method) []method {
	var matches []method
	for _, entry := range candidates {
		if !entry.hasRange() {
			matches = append(matches, entry)
		}
	}
	return matches
}

// singleMethod returns the one original method the candidates agree on. Several
// overloads of one original name agree; different original names do not, and
// picking one would be a guess.
func singleMethod(candidates []method) (method, bool) {
	if len(candidates) == 0 {
		return method{}, false
	}
	for _, entry := range candidates[1:] {
		if entry.class != candidates[0].class || entry.name != candidates[0].name {
			return method{}, false
		}
	}
	return candidates[0], true
}

// original maps a reported line through this entry's ranges. Lines are linear
// within a range and are clamped to the original range's end; an entry with no
// original lines keeps the reported line.
func (m method) original(mapped *class, line int) Original {
	originalLine := line
	if m.originalStart > 0 && m.hasRange() && line > 0 {
		originalLine = m.originalStart + (line - m.obfuscatedStart)
		if m.originalEnd >= m.originalStart && originalLine > m.originalEnd {
			originalLine = m.originalEnd
		}
	}
	return Original{Class: m.class, Method: m.name, Line: originalLine, Inlined: m.class != mapped.original, Resolved: true}
}

// SourceFile derives the file an original class was compiled from. The mapping
// does not record it, so this assumes the top-level class name plus ".java",
// which is wrong for Kotlin sources.
func SourceFile(originalClass string) string {
	name := originalClass[strings.LastIndexByte(originalClass, '.')+1:]
	// Cut at the first "$" for a nested class ("Outer$Inner" -> "Outer"). A
	// simple name that starts with "$" (a legal, if unusual, Java identifier)
	// would cut to empty; keep the whole simple name rather than emit a bare
	// ".java".
	if before, _, found := strings.Cut(name, "$"); found && before != "" {
		name = before
	}
	return name + ".java"
}
