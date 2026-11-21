package issuedigest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	maxBreadcrumbs       = 20
	maxBreadcrumbFields  = 6
	maxContexts          = 12
	contextTypeFieldName = "type"
	deviceContextName    = "device"
)

var identifyingContextFields = []string{
	contextTypeFieldName, "id", "device_app_hash", "device_id", "installation_id", "ip_address", "email", "username",
}

// Sentry SDKs put the owner-given device name ("Anna's iPhone") in contexts.device.name;
// elsewhere "name" names software, as in contexts.os.name.
var identifyingDeviceFields = append([]string{"name"}, identifyingContextFields...)

type keyValue struct {
	key   string
	value string
}

type namedContext struct {
	name   string
	fields []keyValue
}

type breadcrumb struct {
	timestamp string
	level     string
	category  string
	message   string
	data      string
}

type payloadFields struct {
	tags        []keyValue
	contexts    []namedContext
	breadcrumbs []breadcrumb
}

func readPayload(raw json.RawMessage) payloadFields {
	var payload struct {
		Tags        json.RawMessage `json:"tags"`
		Contexts    json.RawMessage `json:"contexts"`
		Breadcrumbs json.RawMessage `json:"breadcrumbs"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return payloadFields{}
	}
	return payloadFields{
		tags:        readTags(payload.Tags),
		contexts:    readContexts(payload.Contexts),
		breadcrumbs: readBreadcrumbs(payload.Breadcrumbs),
	}
}

func readTags(raw json.RawMessage) []keyValue {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] == '[' {
		var pairs [][]json.RawMessage
		if err := json.Unmarshal(trimmed, &pairs); err != nil {
			return nil
		}
		tags := make([]keyValue, 0, len(pairs))
		for _, pair := range pairs {
			if len(pair) != 2 {
				continue
			}
			key, keyOK := scalar(pair[0])
			value, valueOK := scalar(pair[1])
			if keyOK && valueOK && key != "" {
				tags = append(tags, keyValue{key: key, value: value})
			}
		}
		return tags
	}
	tags := scalarFields(trimmed, nil)
	return slices.DeleteFunc(tags, func(tag keyValue) bool { return isUserTag(tag.key) })
}

func isUserTag(key string) bool {
	return key == "user" || strings.HasPrefix(key, "user.")
}

func readContexts(raw json.RawMessage) []namedContext {
	var contexts map[string]json.RawMessage
	if err := json.Unmarshal(raw, &contexts); err != nil {
		return nil
	}
	names := sortedKeys(contexts)
	result := make([]namedContext, 0, min(len(names), maxContexts))
	for _, name := range names {
		if len(result) == maxContexts {
			break
		}
		skipped := identifyingContextFields
		if name == deviceContextName {
			skipped = identifyingDeviceFields
		}
		fields := scalarFields(contexts[name], skipped)
		if name != "" && len(fields) > 0 {
			result = append(result, namedContext{name: name, fields: fields})
		}
	}
	return result
}

func readBreadcrumbs(raw json.RawMessage) []breadcrumb {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	var items []json.RawMessage
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil
		}
	} else {
		var wrapped struct {
			Values []json.RawMessage `json:"values"`
		}
		if err := json.Unmarshal(trimmed, &wrapped); err != nil {
			return nil
		}
		items = wrapped.Values
	}
	items = items[max(0, len(items)-maxBreadcrumbs):]
	crumbs := make([]breadcrumb, 0, len(items))
	for _, item := range items {
		if crumb, ok := readBreadcrumb(item); ok {
			crumbs = append(crumbs, crumb)
		}
	}
	return crumbs
}

func readBreadcrumb(raw json.RawMessage) (breadcrumb, bool) {
	var item struct {
		Timestamp json.RawMessage `json:"timestamp"`
		Level     string          `json:"level"`
		Category  string          `json:"category"`
		Message   string          `json:"message"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return breadcrumb{}, false
	}
	pairs := scalarFields(item.Data, nil)
	data := make([]string, 0, min(len(pairs), maxBreadcrumbFields))
	for _, pair := range pairs[:min(len(pairs), maxBreadcrumbFields)] {
		data = append(data, pair.key+"="+pair.value)
	}
	return breadcrumb{
		timestamp: breadcrumbTime(item.Timestamp),
		level:     item.Level,
		category:  item.Category,
		message:   item.Message,
		data:      strings.Join(data, " "),
	}, true
}

// breadcrumbTime accepts both wire forms Sentry SDKs send: epoch seconds
// (fractional) and an RFC 3339 string.
func breadcrumbTime(raw json.RawMessage) string {
	var seconds float64
	if err := json.Unmarshal(raw, &seconds); err == nil && seconds > 0 && !math.IsInf(seconds, 0) {
		whole, fraction := math.Modf(seconds)
		return time.Unix(int64(whole), int64(fraction*1e9)).UTC().Format(timestampLayout)
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil && text != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return parsed.UTC().Format(timestampLayout)
		}
		return text
	}
	return ""
}

func scalarFields(raw json.RawMessage, skipped []string) []keyValue {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil
	}
	fields := make([]keyValue, 0, len(object))
	for _, key := range sortedKeys(object) {
		if slices.Contains(skipped, key) {
			continue
		}
		if value, ok := scalar(object[key]); ok {
			fields = append(fields, keyValue{key: key, value: value})
		}
	}
	return fields
}

func scalar(raw json.RawMessage) (string, bool) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	switch typed := value.(type) {
	case string:
		return typed, true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	case bool:
		return fmt.Sprint(typed), true
	default:
		return "", false
	}
}

func sortedKeys(object map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
