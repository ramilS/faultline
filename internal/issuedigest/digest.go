// Package issuedigest renders an issue and one of its events as compact Markdown for an AI assistant.
package issuedigest

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ramilS/faultline/internal/issue"
	"github.com/ramilS/faultline/internal/sourcemap"
)

const (
	maxTagKeys         = 20
	maxValuesPerTag    = 5
	maxInAppFrames     = 30
	maxFallbackFrames  = 10
	maxCauses          = 5
	maxTextRunes       = 300
	maxContextFields   = 12
	maxEventTags       = 30
	emptyPlaceholder   = "—"
	timestampLayout    = time.RFC3339
	dailyBucketsLabel  = "daily events, last 14 days, oldest first"
	hourlyBucketsLabel = "hourly events, last 24 hours, oldest first"
)

func Render(detail issue.Detail, event *issue.EventDetail) string {
	var out strings.Builder
	writeIssue(&out, detail)
	writeDistribution(&out, detail)
	if event != nil {
		writeEvent(&out, *event)
	}
	return out.String()
}

func writeIssue(out *strings.Builder, detail issue.Detail) {
	source := detail.Issue
	fmt.Fprintf(out, "# Issue %d · %s\n\n", source.ID, source.ProjectSlug)
	fmt.Fprintf(out, "title: %s\n", clip(preferred(source.DisplayTitle, source.Title)))
	fmt.Fprintf(out, "culprit: %s\n", clip(preferred(source.DisplayCulprit, source.Culprit)))
	fmt.Fprintf(out, "level: %s · status: %s\n", source.Level, source.Status)
	fmt.Fprintf(out, "events: %d · users: %d\n", source.TimesSeen, source.UsersSeen)
	fmt.Fprintf(out, "first seen: %s · last seen: %s\n", stamp(source.FirstSeen), stamp(source.LastSeen))
	if detail.Regression != nil {
		fmt.Fprintf(out, "regressed: %s in release %s\n", stamp(detail.Regression.At), preferred(detail.Regression.Release, emptyPlaceholder))
	}
	fmt.Fprintf(out, "environments: %s\n", listOrPlaceholder(detail.Environments))
	fmt.Fprintf(out, "%s: %s\n", dailyBucketsLabel, joinCounts(source.Counts14d[:]))
	fmt.Fprintf(out, "%s: %s\n", hourlyBucketsLabel, joinCounts(source.Counts24h[:]))
}

func writeDistribution(out *strings.Builder, detail issue.Detail) {
	if len(detail.Tags) == 0 {
		return
	}
	out.WriteString("\n## Tags across all events (share of events)\n\n")
	facets := slices.DeleteFunc(slices.Clone(detail.Tags), func(facet issue.TagFacet) bool { return isUserTag(facet.Key) })
	for index, facet := range facets {
		if index == maxTagKeys {
			fmt.Fprintf(out, "… %d more tag keys\n", len(facets)-maxTagKeys)
			break
		}
		values := make([]string, 0, maxValuesPerTag)
		for valueIndex, value := range facet.Values {
			if valueIndex == maxValuesPerTag {
				values = append(values, fmt.Sprintf("… %d more", len(facet.Values)-maxValuesPerTag))
				break
			}
			values = append(values, fmt.Sprintf("%s %s", clip(value.Value), share(value.TimesSeen, detail.Issue.TimesSeen)))
		}
		fmt.Fprintf(out, "%s: %s\n", facet.Key, strings.Join(values, ", "))
	}
}

func writeEvent(out *strings.Builder, event issue.EventDetail) {
	release := emptyPlaceholder
	if event.Release != nil && *event.Release != "" {
		release = *event.Release
	}
	fmt.Fprintf(out, "\n## Event %s\n\n", event.EventID)
	fmt.Fprintf(out, "time: %s · environment: %s · release: %s · level: %s\n",
		stamp(event.Timestamp), preferred(event.Environment, emptyPlaceholder), release, event.Level)
	if event.Message != "" {
		fmt.Fprintf(out, "message: %s\n", clip(event.Message))
	}
	writeException(out, event.SymbolicatedException)

	fields := readPayload(event.Payload)
	writeEventTags(out, fields.tags)
	writeContexts(out, fields.contexts)
	writeBreadcrumbs(out, fields.breadcrumbs)
}

func writeException(out *strings.Builder, exception *sourcemap.Exception) {
	if exception == nil {
		return
	}
	out.WriteString("\n### Exception (error site first)\n\n```\n")
	for depth := 0; exception != nil; depth++ {
		if depth == maxCauses {
			out.WriteString("… more causes\n")
			break
		}
		if depth > 0 {
			out.WriteString("caused by: ")
		}
		fmt.Fprintf(out, "%s: %s\n", exception.Type, clip(exception.Value))
		writeFrames(out, exception.Frames)
		exception = exception.Cause
	}
	out.WriteString("```\n")
}

func writeFrames(out *strings.Builder, frames []sourcemap.Frame) {
	shown := framesToShow(frames)
	for _, frame := range shown {
		position := frame.Generated
		if frame.Original != nil {
			position = *frame.Original
		}
		fmt.Fprintf(out, "  at %s\n", describePosition(position))
	}
	if omitted := len(frames) - len(shown); omitted > 0 {
		fmt.Fprintf(out, "  … %d other frames\n", omitted)
	}
}

func framesToShow(frames []sourcemap.Frame) []sourcemap.Frame {
	inApp := make([]sourcemap.Frame, 0, len(frames))
	for _, frame := range frames {
		if isInApp(frame) {
			inApp = append(inApp, frame)
		}
	}
	if len(inApp) > 0 {
		return inApp[:min(len(inApp), maxInAppFrames)]
	}
	return frames[:min(len(frames), maxFallbackFrames)]
}

func isInApp(frame sourcemap.Frame) bool {
	if frame.Original != nil {
		return frame.Original.InApp
	}
	return frame.Generated.InApp
}

func describePosition(position sourcemap.Position) string {
	function := preferred(position.Function, "?")
	if position.Filename == "" {
		return function
	}
	location := position.Filename
	if position.Line > 0 {
		location += ":" + strconv.Itoa(position.Line)
	}
	return fmt.Sprintf("%s (%s)", function, location)
}

func writeEventTags(out *strings.Builder, tags []keyValue) {
	if len(tags) == 0 {
		return
	}
	out.WriteString("\n### Event tags\n\n")
	pairs := make([]string, 0, min(len(tags), maxEventTags))
	for _, tag := range tags[:min(len(tags), maxEventTags)] {
		pairs = append(pairs, tag.key+"="+clip(tag.value))
	}
	out.WriteString(strings.Join(pairs, " · "))
	out.WriteString("\n")
}

func writeContexts(out *strings.Builder, contexts []namedContext) {
	if len(contexts) == 0 {
		return
	}
	out.WriteString("\n### Contexts\n\n")
	for _, context := range contexts {
		pairs := make([]string, 0, min(len(context.fields), maxContextFields))
		for _, field := range context.fields[:min(len(context.fields), maxContextFields)] {
			pairs = append(pairs, field.key+"="+clip(field.value))
		}
		fmt.Fprintf(out, "%s: %s\n", context.name, strings.Join(pairs, " · "))
	}
}

func writeBreadcrumbs(out *strings.Builder, breadcrumbs []breadcrumb) {
	if len(breadcrumbs) == 0 {
		return
	}
	fmt.Fprintf(out, "\n### Breadcrumbs (last %d, oldest first)\n\n", len(breadcrumbs))
	for _, crumb := range breadcrumbs {
		parts := []string{preferred(crumb.timestamp, emptyPlaceholder)}
		if crumb.level != "" {
			parts = append(parts, crumb.level)
		}
		if crumb.category != "" {
			parts = append(parts, "["+crumb.category+"]")
		}
		if crumb.message != "" {
			parts = append(parts, clip(crumb.message))
		}
		if crumb.data != "" {
			parts = append(parts, clip(crumb.data))
		}
		out.WriteString(strings.Join(parts, " "))
		out.WriteString("\n")
	}
}

func preferred(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func stamp(value time.Time) string {
	if value.IsZero() {
		return emptyPlaceholder
	}
	return value.UTC().Format(timestampLayout)
}

func listOrPlaceholder(values []string) string {
	if len(values) == 0 {
		return emptyPlaceholder
	}
	return strings.Join(values, ", ")
}

func joinCounts(counts []int64) string {
	parts := make([]string, 0, len(counts))
	for _, count := range counts {
		parts = append(parts, strconv.FormatInt(count, 10))
	}
	return strings.Join(parts, " ")
}

func share(part, total int64) string {
	if total <= 0 {
		return fmt.Sprintf("(%d)", part)
	}
	return fmt.Sprintf("%d%%", (part*100+total/2)/total)
}

func clip(value string) string {
	flattened := strings.Join(strings.Fields(value), " ")
	runes := []rune(flattened)
	if len(runes) <= maxTextRunes {
		return flattened
	}
	return string(runes[:maxTextRunes]) + "…"
}
