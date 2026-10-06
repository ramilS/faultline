package grouping

import (
	"fmt"
	"strings"
	"unicode"
)

// normalizeMessage collapses volatile detail so the same logical message hashes
// identically across occurrences: it lowercases, masks numbers, long hex runs,
// and UUIDs with '#', replaces quoted literals with "?", and collapses
// whitespace. Single pass, no regex.
func normalizeMessage(message string) string {
	runes := []rune(strings.ToLower(message))
	var builder strings.Builder
	builder.Grow(len(message))
	pendingSpace := false

	emit := func(text string) {
		if text == "" {
			return
		}
		if pendingSpace && builder.Len() > 0 {
			builder.WriteByte(' ')
		}
		pendingSpace = false
		builder.WriteString(text)
	}

	for index := 0; index < len(runes); {
		current := runes[index]
		switch {
		case unicode.IsSpace(current):
			if builder.Len() > 0 {
				pendingSpace = true
			}
			index++
		case current == '"' || current == '\'':
			closing := index + 1
			for closing < len(runes) && runes[closing] != current {
				closing++
			}
			emit(`"?"`)
			if closing < len(runes) {
				index = closing + 1
			} else {
				index = len(runes)
			}
		default:
			if span := matchUUID(runes, index); span > 0 {
				emit("#")
				index += span
				continue
			}
			if isWordRune(current) {
				start := index
				for index < len(runes) && isWordRune(runes[index]) {
					index++
				}
				emit(classifyWord(string(runes[start:index])))
				continue
			}
			emit(string(current))
			index++
		}
	}
	return builder.String()
}

func classifyWord(word string) string {
	hasDigit := false
	onlyNumeric := true // digits and dots only
	onlyHex := true
	hasDot := false
	for _, r := range word {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == '.':
			hasDot = true
			onlyHex = false
		default:
			onlyNumeric = false
			if r < 'a' || r > 'f' {
				onlyHex = false
			}
		}
	}
	if hasDigit && onlyNumeric {
		return "#"
	}
	if onlyHex && !hasDot && len(word) >= 4 {
		return "#"
	}
	return word
}

func matchUUID(runes []rune, start int) int {
	groups := [...]int{8, 4, 4, 4, 12}
	position := start
	for groupIndex, size := range groups {
		if groupIndex > 0 {
			if position >= len(runes) || runes[position] != '-' {
				return 0
			}
			position++
		}
		for range size {
			if position >= len(runes) || !isHexRune(runes[position]) {
				return 0
			}
			position++
		}
	}
	return position - start
}

func isWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.'
}

func isHexRune(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// frameKey is the string a frame contributes to the fingerprint and the
// culprit. A frame with a module or function is keyed on those; only a frame
// with neither, such as an unsymbolicated native frame, falls back to its
// image and the offset inside it.
func frameKey(frame Frame) string {
	if key := normalizeFrame(frame.Module, frame.Function); key != "" {
		return key
	}
	return nativeFrameKey(frame.Package, frame.ImageOffset)
}

// nativeFrameKey names a native frame by the image's base name and its offset
// inside the image. The offset survives a relaunch, where the load address does
// not, and the base name survives a different install path.
func nativeFrameKey(pkg string, offset *uint64) string {
	pkg = strings.TrimRight(pkg, `/\`)
	name := pkg[strings.LastIndexAny(pkg, `/\`)+1:]
	switch {
	case name == "":
		return ""
	case offset == nil:
		return name
	default:
		return fmt.Sprintf("%s+0x%x", name, *offset)
	}
}

// normalizeFrame strips compiler- and bundler-generated noise from a stack frame
// so the same source location hashes identically across builds.
func normalizeFrame(module, function string) string {
	normalizedModule := normalizeModule(module)
	normalizedFunction := normalizeFunction(function)
	switch {
	case normalizedFunction == "":
		return normalizedModule
	case normalizedModule == "":
		return normalizedFunction
	default:
		return normalizedModule + "." + normalizedFunction
	}
}

func normalizeModule(module string) string {
	if strings.Contains(module, "$Proxy") || strings.Contains(module, "$$EnhancerBy") {
		return "<proxy>"
	}
	result := module
	if index := strings.Index(result, "$$Lambda$"); index >= 0 {
		result = result[:index]
	}
	if index := strings.IndexByte(result, '?'); index >= 0 {
		result = result[:index]
	}
	return stripTrailingAnonymousClass(result)
}

func stripTrailingAnonymousClass(module string) string {
	index := strings.LastIndexByte(module, '$')
	if index < 0 || index == len(module)-1 {
		return module
	}
	for _, r := range module[index+1:] {
		if r < '0' || r > '9' {
			return module
		}
	}
	return module[:index]
}

func normalizeFunction(function string) string {
	if function == "<anonymous>" {
		return ""
	}
	result := function
	result = strings.TrimSuffix(result, "$default")
	result = strings.TrimSuffix(result, "$suspendImpl")
	return stripScalaClosureNumber(result)
}

// stripScalaClosureNumber drops the counter the Scala compiler appends to a
// closure's method name ($anonfun$chargeCard$1), which shifts when a closure is
// added or reordered in the same class and would otherwise split the issue.
func stripScalaClosureNumber(function string) string {
	if !strings.Contains(function, "$anonfun$") {
		return function
	}
	cut := strings.LastIndexByte(function, '$')
	digits := function[cut+1:]
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return function
	}
	return function[:cut]
}
