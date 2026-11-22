package ingest

import (
	"encoding/json"
	"strings"
)

// Tag keys Sentry promotes from contexts.device and contexts.os, kept the same
// so filters written against Sentry carry over.
const (
	deviceTagKey = "device"
	osTagKey     = "os"
)

type sentryDeviceContext struct {
	Model json.RawMessage `json:"model"`
}

type sentryOSContext struct {
	OS      json.RawMessage `json:"os"`
	Name    json.RawMessage `json:"name"`
	Version json.RawMessage `json:"version"`
}

func withContextTags(tags map[string]string, device, os json.RawMessage) map[string]string {
	derived := map[string]string{
		deviceTagKey: deviceTag(device),
		osTagKey:     osTag(os),
	}
	for key, value := range derived {
		if value == "" {
			continue
		}
		if _, explicit := tags[key]; explicit {
			continue
		}
		if tags == nil {
			tags = make(map[string]string, len(derived))
		}
		tags[key] = value
	}
	return tags
}

func deviceTag(raw json.RawMessage) string {
	var context sentryDeviceContext
	if json.Unmarshal(raw, &context) != nil {
		return ""
	}
	return contextField(context.Model)
}

// osTag follows Relay's normalisation: a precomputed contexts.os.os wins,
// otherwise "name version", or the name alone.
func osTag(raw json.RawMessage) string {
	var context sentryOSContext
	if json.Unmarshal(raw, &context) != nil {
		return ""
	}
	if precomputed := contextField(context.OS); precomputed != "" {
		return precomputed
	}
	name := contextField(context.Name)
	if name == "" {
		return ""
	}
	if version := contextField(context.Version); version != "" {
		return name + " " + version
	}
	return name
}

func contextField(raw json.RawMessage) string {
	value, ok := scalarTagValue(raw)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}
