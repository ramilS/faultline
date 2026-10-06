package ingest

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/ramilS/faultline/internal/platform/httpjson"
	"github.com/ramilS/faultline/internal/spill"
)

// sentryRateLimitScope labels the enforcement unit in X-Sentry-Rate-Limits.
// Our bucket is per ingest key, so "key" is the accurate scope; the empty
// category field means the limit applies to every data type.
const sentryRateLimitScope = "key"

type sentryAcceptedResponse struct {
	ID string `json:"id"`
}

// ServeSentry admits Sentry envelopes over POST /api/{project_id}/envelope/.
// Order is credentials → decode → charge → dispatch, so an oversize or
// malformed envelope is rejected before it can consume a key's rate budget.
func (h *Handler) ServeSentry(w http.ResponseWriter, r *http.Request) {
	projectID, ok := ProjectIDFromPath(r.PathValue("project_id"))
	if !ok {
		writeSentryError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	publicKey := SentryKeyFromRequest(r)
	if publicKey == "" {
		writeSentryError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	info, err := h.keys.Lookup(r.Context(), publicKey)
	if errors.Is(err, ErrKeyNotFound) || (err == nil && info.ProjectID != projectID) {
		writeSentryError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("ingest key lookup: %w", err))
		writeSentryError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	body := http.MaxBytesReader(w, r.Body, MaxCompressedBytes)
	envelope, captured, err := DecodeSentryEnvelope(body, RequestOptions{Gzip: r.Header.Get("Content-Encoding") == "gzip"})
	if err != nil {
		writeSentryDecodeError(w, err)
		return
	}

	parsed, err := EnvelopeFromSentryItems(envelope, h.now())
	if err != nil {
		writeSentryDecodeError(w, err)
		return
	}

	// At least 1, even for zero events: a transaction-only or session envelope
	// still costs the network round trip, decode work, and (in durable mode) a
	// spill write, and charging 0 would let those requests bypass the limiter
	// entirely — rate.Limiter.ReserveN(now, 0) always succeeds with no delay.
	eventUnits := max(1, len(parsed.Events))
	if !h.chargeSentryRateLimit(w, info, eventUnits) {
		return
	}

	// A zero-item envelope (bare header, no event/transaction/session/... lines)
	// carries nothing to spill or dispatch, so writing a durable WAL record for
	// it — and paying its fsync — would be pure waste.
	if len(envelope.Items) > 0 {
		receivedAt := h.now()
		if h.durable {
			if err := h.spiller.Append(info.ProjectID, receivedAt, captured); err != nil {
				h.writeSpillFailure(w, err)
				return
			}
		} else {
			DispatchEnvelope(r.Context(), parsed, info.ProjectID, receivedAt, h.dedup, h.rules, h.settings, h.dispatch, h.dispatchSpans, h.reportError)
		}
	}

	httpjson.Write(w, http.StatusOK, sentryAcceptedResponse{ID: responseEventID(envelope, parsed)})
}

// EnvelopeFromSentryItems maps the item types we understand and silently
// discards the rest. The specification requires servers to skip unknown item
// types, and an SDK that gets an error for a session envelope retries it
// indefinitely. receivedAt stamps events whose payload omits a timestamp; the
// durable feeder passes the spilled record's own timestamp so a replayed
// envelope keeps its original receipt time instead of the replay time.
func EnvelopeFromSentryItems(envelope SentryEnvelope, receivedAt time.Time) (Envelope, error) {
	parsed := Envelope{Events: []Event{}, Spans: []Span{}}
	fallbackEventID := ""
	if countEventItems(envelope.Items) == 1 {
		fallbackEventID = envelope.EventID
	}
	for _, item := range envelope.Items {
		switch item.Type {
		case "event":
			if len(parsed.Events) >= MaxEvents {
				return Envelope{}, fmt.Errorf("%w: more than %d events", ErrTooLarge, MaxEvents)
			}
			event, err := mapSentryEventInEnvelope(item.Payload, fallbackEventID, receivedAt)
			if err != nil {
				return Envelope{}, err
			}
			parsed.Events = append(parsed.Events, event)
		case "transaction":
			spans, err := MapSentryTransaction(item.Payload)
			if err != nil {
				return Envelope{}, err
			}
			if len(parsed.Spans)+len(spans) > MaxSpans {
				return Envelope{}, fmt.Errorf("%w: more than %d spans", ErrTooLarge, MaxSpans)
			}
			parsed.Spans = append(parsed.Spans, spans...)
		}
	}
	return parsed, nil
}

// countEventItems counts the event items. The envelope header's id may stand in
// for a missing payload id only when it is unambiguous: with several events it
// would give them all one id and dedup would silently drop all but the first.
func countEventItems(items []SentryItem) int {
	count := 0
	for _, item := range items {
		if item.Type == "event" {
			count++
		}
	}
	return count
}

// responseEventID prefers the envelope header's id, which is what an SDK
// correlates against, and falls back to the first mapped event. The header id
// is echoed verbatim into the response body, and unlike an item's event_id
// (parsed with uuid.Parse) it is never format-checked at decode time — so it
// is only safe to echo once confirmed to be the bounded 32-hex-char form Sentry
// actually sends, not whatever a crafted header line happened to contain.
func responseEventID(envelope SentryEnvelope, parsed Envelope) string {
	if isHexEventID(envelope.EventID) {
		return envelope.EventID
	}
	if len(parsed.Events) > 0 {
		return hexEventID(parsed.Events[0])
	}
	return ""
}

// isHexEventID reports whether id is Sentry's wire form for an event id: 32
// lowercase hex characters, no dashes.
func isHexEventID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for i := range len(id) {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func hexEventID(event Event) string {
	return hex.EncodeToString(event.EventID[:])
}

func (h *Handler) writeSpillFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, spill.ErrSpillFull) {
		writeSentryRateLimit(w, 30, "spill full")
		return
	}
	h.reportError(fmt.Errorf("spill append: %w", err))
	writeSentryError(w, http.StatusServiceUnavailable, "durability unavailable")
}

func (h *Handler) chargeSentryRateLimit(w http.ResponseWriter, info KeyInfo, eventCount int) bool {
	retryAfter, ok := h.limiter.Allow(info, eventCount, h.now())
	if ok {
		return true
	}
	seconds := max(int(math.Ceil(retryAfter.Seconds())), 1)
	writeSentryRateLimit(w, seconds, "rate limit exceeded")
	return false
}

// writeSentryRateLimit sends both headers. Retry-After alone is a valid
// fallback, but the structured header lets a compliant SDK back off per data
// category instead of globally.
func writeSentryRateLimit(w http.ResponseWriter, seconds int, detail string) {
	value := strconv.Itoa(seconds)
	w.Header().Set("Retry-After", value)
	w.Header().Set("X-Sentry-Rate-Limits", value+"::"+sentryRateLimitScope)
	writeSentryError(w, http.StatusTooManyRequests, detail)
}

func writeSentryDecodeError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrTooLarge) {
		writeSentryError(w, http.StatusRequestEntityTooLarge, "envelope too large")
		return
	}
	writeSentryError(w, http.StatusBadRequest, err.Error())
}

// sentryErrorResponse pins the rejection wire shape the Sentry SDKs read. It
// is a struct rather than a map so the encoder cannot fail on it, which is
// what makes the discard inside httpjson.Write sound here.
type sentryErrorResponse struct {
	Detail string `json:"detail"`
}

func writeSentryError(w http.ResponseWriter, status int, detail string) {
	httpjson.Write(w, status, sentryErrorResponse{Detail: detail})
}
