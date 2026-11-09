package scrub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type Segment struct {
	Text     string `json:"text"`
	Redacted bool   `json:"redacted"`
}

type Preview struct {
	Scrubbed string    `json:"scrubbed"`
	Segments []Segment `json:"segments"`
}

// Private-use code points bracket every replacement in the marking run. They
// never reach storage: the marking run only locates the spans the real run
// produced.
const (
	markOpen  = ""
	markClose = ""
)

func (s *Scrubber) PreviewSample(sample string, redactIP bool) (Preview, error) {
	trimmed := strings.TrimSpace(sample)
	if (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Valid([]byte(trimmed)) {
		return s.PreviewPayload(json.RawMessage(trimmed), redactIP)
	}
	scrubbed := s.String(sample)
	marked := s.marking().String(sample)
	return Preview{Scrubbed: scrubbed, Segments: segments(scrubbed, marked)}, nil
}

func (s *Scrubber) PreviewPayload(payload json.RawMessage, redactIP bool) (Preview, error) {
	scrubbed, err := indentedPayload(s, payload, redactIP)
	if err != nil {
		return Preview{}, err
	}
	marked, err := indentedPayload(s.marking(), payload, redactIP)
	if err != nil {
		return Preview{}, err
	}
	return Preview{Scrubbed: scrubbed, Segments: segments(scrubbed, marked)}, nil
}

func indentedPayload(s *Scrubber, payload json.RawMessage, redactIP bool) (string, error) {
	scrubbed, err := s.Payload(payload, redactIP)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, scrubbed, "", "  "); err != nil {
		return "", fmt.Errorf("indent payload: %w", err)
	}
	return out.String(), nil
}

func (s *Scrubber) marking() *Scrubber {
	marked := *s
	marked.filtered = markOpen + s.filtered + markClose
	marked.global = markRules(s.global)
	marked.project = markRules(s.project)
	return &marked
}

func markRules(rules []compiledRule) []compiledRule {
	marked := make([]compiledRule, len(rules))
	for index, rule := range rules {
		marked[index] = newCompiledRule(rule.pattern, markOpen+rule.replacement+markClose, rule.refs)
	}
	return marked
}

// segments splits scrubbed by the markers in marked. The marking run can
// diverge from the real one: a rule may match a marker, or the markers' extra
// bytes may cross a growth limit. Whenever marked without its markers is not
// scrubbed, or the markers do not nest, the whole text is returned as one
// unmarked segment rather than a highlight that misplaces a span.
func segments(scrubbed, marked string) []Segment {
	plain := []Segment{{Text: scrubbed}}
	if strings.NewReplacer(markOpen, "", markClose, "").Replace(marked) != scrubbed {
		return plain
	}
	var (
		result  []Segment
		current strings.Builder
		depth   int
	)
	flush := func(redacted bool) {
		if current.Len() > 0 {
			result = append(result, Segment{Text: current.String(), Redacted: redacted})
			current.Reset()
		}
	}
	for _, char := range marked {
		switch string(char) {
		case markOpen:
			flush(depth > 0)
			depth++
		case markClose:
			if depth == 0 {
				return plain
			}
			flush(true)
			depth--
		default:
			current.WriteRune(char)
		}
	}
	if depth != 0 {
		return plain
	}
	flush(false)
	return mergeSegments(result)
}

func mergeSegments(parts []Segment) []Segment {
	merged := make([]Segment, 0, len(parts))
	for _, part := range parts {
		if last := len(merged) - 1; last >= 0 && merged[last].Redacted == part.Redacted {
			merged[last].Text += part.Text
			continue
		}
		merged = append(merged, part)
	}
	if len(merged) == 0 {
		return []Segment{{Text: ""}}
	}
	return merged
}
