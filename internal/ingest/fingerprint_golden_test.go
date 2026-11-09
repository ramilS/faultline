package ingest

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/native"
	"github.com/ramilS/faultline/internal/pipeline"
	"github.com/ramilS/faultline/internal/storage"
)

const (
	goldenJavaScriptEvent = `{"event_id":"6f1c2a4e8b7d4c0e9a3b5d7f1e2c4a6b","platform":"javascript","exception":{"values":[{"type":"TypeError","value":"Cannot read properties of undefined (reading 'id')","stacktrace":{"frames":[
		{"filename":"app:///static/js/vendor.4f2a.js","function":"invokeGuardedCallback","in_app":false,"lineno":1,"colno":4521},
		{"filename":"app:///static/js/main.9c1e.js","function":"CheckoutForm.handleSubmit","in_app":true,"lineno":1,"colno":88123},
		{"filename":"app:///static/js/main.9c1e.js","function":"<anonymous>","in_app":true,"lineno":1,"colno":90012},
		{"filename":"app:///static/js/main.9c1e.js?v=3","function":"submitOrder","in_app":true,"lineno":1,"colno":91877}]}}]}}`
	goldenJVMEvent = `{"event_id":"0b9a8c7d6e5f4a3b2c1d0e9f8a7b6c5d","platform":"java","exception":{"values":[
		{"type":"java.sql.SQLException","value":"connection reset","module":"java.sql","stacktrace":{"frames":[{"module":"com.zaxxer.hikari.pool.HikariPool","function":"getConnection","in_app":false}]}},
		{"type":"IllegalStateException","value":"order 4211 not found","module":"java.lang","stacktrace":{"frames":[
			{"module":"java.lang.Thread","function":"run","in_app":false},
			{"module":"org.springframework.web.servlet.FrameworkServlet","function":"service","in_app":false},
			{"module":"com.acme.billing.OrderController$$Lambda$412/0x0000000800c4b040","function":"apply","in_app":true},
			{"module":"com.acme.billing.OrderService$1","function":"load$default","in_app":true},
			{"module":"com.acme.billing.OrderRepository$$EnhancerBySpringCGLIB$$5f1a","function":"find","in_app":true},
			{"module":"com.acme.billing.Charges","function":"$anonfun$chargeCard$3","in_app":true}]}}]}}`
	goldenPythonEvent = `{"event_id":"a1b2c3d4e5f60718293a4b5c6d7e8f90","platform":"python","logger":"billing","exception":{"values":[{"type":"KeyError","value":"'customer_id'","module":null,"stacktrace":{"frames":[
		{"module":"django.core.handlers.base","filename":"django/core/handlers/base.py","function":"_get_response","in_app":false,"lineno":197},
		{"module":"shop.views","filename":"shop/views.py","function":"checkout","in_app":true,"lineno":42},
		{"module":"shop.billing","filename":"shop/billing.py","function":"charge","in_app":true,"lineno":118}]}}]}}`
	goldenMessageEvent   = `{"event_id":"11111111222233334444555555555555","logger":"cron","message":"job 9172 exceeded 30s on host 'worker-3'"}`
	goldenNoStackEvent   = `{"event_id":"22222222333344445555666666666666","exception":{"values":[{"type":"ValueError","value":"invalid literal for int() with base 10: 'abc'"}]}}`
	goldenNoInAppEvent   = `{"event_id":"33333333444455556666777777777777","exception":{"values":[{"type":"Error","value":"boom","stacktrace":{"frames":[{"filename":"node:internal/timers","function":"listOnTimeout"},{"filename":"node:internal/timers","function":"processTimers"}]}}]}}`
	goldenUnsymbolicated = `{"event_id":"44444444555566667777888888888888","platform":"cocoa","exception":{"values":[{"type":"EXC_BAD_ACCESS","value":"KERN_INVALID_ADDRESS","stacktrace":{"frames":[
		{"package":"/usr/lib/system/libdyld.dylib","image_addr":"0x1a0000000","instruction_addr":"0x1a0001234","in_app":false},
		{"package":"/private/var/containers/Bundle/Application/X/MyApp.app/MyApp","image_addr":"0x100000000","symbol_addr":"0x10000c000","instruction_addr":"0x10000c014","in_app":true}]}}]}}`
)

// A changed value splits every existing issue of that kind.
func TestFingerprintsOfEventsWithoutDebugFilesAreUnchanged(t *testing.T) {
	unnamedSymbols := func(_ context.Context, _ int64, _ []native.Image, addresses []uint64) ([]string, error) {
		return make([]string, len(addresses)), nil
	}
	cases := []struct {
		name     string
		payload  []byte
		envelope string
		want     string
	}{
		{"javascript", []byte(goldenJavaScriptEvent), "", "ef55352bc93fb371cf3c54a3fb62f8a8be758e3c3ad59ccc7ef53c25f03c7eab"},
		{"jvm", []byte(goldenJVMEvent), "", "f8d01c9ddb4528f55de40cbf894f940415c1ec189567a1f0badc7d44e91c24b4"},
		{"python", []byte(goldenPythonEvent), "", "40debacb0a125f2edf02d749dc785eabb23478ea3417ad39e68ae6e01fb05bdf"},
		{"message", []byte(goldenMessageEvent), "", "84c0c1e7d7830f2a68825e1a682ebee5057deda7bdb08a214f9efd8cfbc217e5"},
		{"exception without stacktrace", []byte(goldenNoStackEvent), "", "713b1285169939640bf6f09a67f7ca788fa71a9ff859874f4dcd874930915727"},
		{"no in-app frames", []byte(goldenNoInAppEvent), "", "6d24ea960461cb48743d29cee21654230091797f3dccb8ff0d7db81d9ad68e0e"},
		{"native without debug files", []byte(goldenUnsymbolicated), "", "f2b337f23d821626e3872bec35390b09cc3a226ba9ef84e9861ef621f2564ce5"},
		{"captured go", nil, "go", "d7211cd1de58007c0d0a42df62eaf3a1ea79eee8f99155b9b309a383039da082"},
		{"captured php", nil, "php", "f309f24076d01cf353094953867983d90e6a72b0a6715d30d4afed74f78d6ab2"},
		{"captured python", nil, "python", "995c4626c4461b6f0daed3fa25147173fc32fc60ecd099fb137802d27fbb43da"},
		{"captured ruby", nil, "ruby", "40eb1a2e362a88587245b21541b2a58ad21233a083d10526a056c8956e5a25f4"},
		{"captured rust", nil, "rust", "e2ce4d77f9b04a1b6751670b53da463c6579e2b31fb86da921010e4b7686ae96"},
		{"captured scala", nil, "scala", "ef1d90f53147023e9a2a0f15ce5c4711c483c5654a2d8b1c5671c696474e6e95"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			payload, envelopeEventID := testCase.payload, ""
			if testCase.envelope != "" {
				payload, envelopeEventID = capturedEventPayload(t, filepath.Join("testdata", "sdk", testCase.envelope, testCase.envelope+".run1.envelope"))
			}
			event, err := mapSentryEventInEnvelope(payload, envelopeEventID, mapReceivedAt)
			if err != nil {
				t.Fatalf("map: %v", err)
			}
			var got string
			DispatchEnvelope(t.Context(), Envelope{Events: []Event{event}}, 5, time.Unix(0, 0).UTC(), NewDeduplicator(), nil, nil, unnamedSymbols,
				func(_ string, _ int64, item pipeline.Item) { got = item.Row.Fingerprint },
				func(int64, []storage.SpanRow) {}, func(err error) { t.Errorf("reported %v", err) })
			if got != testCase.want {
				t.Fatalf("fingerprint = %s, want %s", got, testCase.want)
			}
		})
	}
}
