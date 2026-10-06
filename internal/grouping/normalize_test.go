package grouping

import "testing"

func TestNormalizeMessage(t *testing.T) {
	for _, test := range []struct {
		name string
		in   string
		want string
	}{
		{"lowercases", "NullPointerException", "nullpointerexception"},
		{"masks integers", "user 42 not found", "user # not found"},
		{"masks decimals and versions", "billing 2.7.1 failed", "billing # failed"},
		{"masks uuid", "req 0197f3ab-1234-4abc-8def-0123456789ab done", "req # done"},
		{"masks long hex token", "hash deadbeef mismatch", "hash # mismatch"},
		{"keeps short non-hex words", "on off log", "on off log"},
		{"masks double quoted literal", `invoice "total mismatch" for 8812`, `invoice "?" for #`},
		{"masks single quoted literal", "value 'abc' rejected", `value "?" rejected`},
		{"collapses whitespace", "a   b\t c\n d", "a b c d"},
		{"is idempotent", "user # not found", "user # not found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeMessage(test.in); got != test.want {
				t.Fatalf("normalizeMessage(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}

func TestNormalizeMessageIsDeterministic(t *testing.T) {
	input := `Timeout after 512 ms calling "acme-service" id=deadbeefcafe0001`
	first := normalizeMessage(input)
	second := normalizeMessage(input)
	if first != second {
		t.Fatalf("normalizeMessage is not deterministic: %q vs %q", first, second)
	}
}

func TestNormalizeFrame(t *testing.T) {
	for _, test := range []struct {
		name     string
		module   string
		function string
		want     string
	}{
		{"plain jvm frame", "com.acme.billing.InvoiceService", "total", "com.acme.billing.InvoiceService.total"},
		{"strips lambda", "com.acme.Job$$Lambda$42", "run", "com.acme.Job.run"},
		{"strips anonymous class suffix", "com.acme.Handler$1", "handle", "com.acme.Handler.handle"},
		{"strips kotlin default", "com.acme.Svc", "process$default", "com.acme.Svc.process"},
		{"strips kotlin suspend impl", "com.acme.Svc", "load$suspendImpl", "com.acme.Svc.load"},
		{"rewrites jdk proxy", "com.sun.proxy.$Proxy42", "invoke", "<proxy>.invoke"},
		{"rewrites cglib proxy", "com.acme.Svc$$EnhancerBySpringCGLIB$$a1b2c3d4", "save", "<proxy>.save"},
		{"strips js webpack query hash", "webpack:///./src/app.js?a1b2c3d4", "onClick", "webpack:///./src/app.js.onClick"},
		{"drops anonymous js function", "src/app.js", "<anonymous>", "src/app.js"},
		{"strips scala anonymous function numbering", "checkout.Capture$package$", "runCheckout$$anonfun$1", "checkout.Capture$package$.runCheckout$$anonfun"},
		{"strips scala adapted closure numbering", "checkout.Capture$package$", "runCheckout$$anonfun$adapted$1", "checkout.Capture$package$.runCheckout$$anonfun$adapted"},
		{"strips scala 2 anonymous function numbering", "com.acme.Billing$", "$anonfun$chargeCard$3", "com.acme.Billing$.$anonfun$chargeCard"},
		{"keeps a trailing number on an ordinary function", "com.acme.Svc", "step$2", "com.acme.Svc.step$2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeFrame(test.module, test.function); got != test.want {
				t.Fatalf("normalizeFrame(%q, %q) = %q, want %q", test.module, test.function, got, test.want)
			}
		})
	}
}

func offsetAt(value uint64) *uint64 { return &value }

func TestFrameKey(t *testing.T) {
	for _, test := range []struct {
		name  string
		frame Frame
		want  string
	}{
		{"named frame is unchanged", Frame{Module: "com.acme.Svc", Function: "run", Package: "/x/MyApp", ImageOffset: offsetAt(0x10)}, "com.acme.Svc.run"},
		{"function alone still wins over the image", Frame{Function: "main", Package: "/x/MyApp"}, "main"},
		{"image with offset", Frame{Package: "/private/var/x/MyApp.app/MyApp", ImageOffset: offsetAt(0xc000)}, "MyApp+0xc000"},
		{"image without offset", Frame{Package: "/private/var/x/MyApp.app/MyApp"}, "MyApp"},
		{"offset zero is kept", Frame{Package: "MyApp", ImageOffset: offsetAt(0)}, "MyApp+0x0"},
		{"windows style path", Frame{Package: `C:\app\native.dll`, ImageOffset: offsetAt(0x1f)}, "native.dll+0x1f"},
		{"anonymous function falls back to the image", Frame{Function: "<anonymous>", Package: "/x/MyApp", ImageOffset: offsetAt(0x20)}, "MyApp+0x20"},
		{"bundle path with a trailing separator", Frame{Package: "/x/MyApp.app/", ImageOffset: offsetAt(0x20)}, "MyApp.app+0x20"},
		{"separator only", Frame{Package: "/", ImageOffset: offsetAt(0x20)}, ""},
		{"offset without an image is ignored", Frame{ImageOffset: offsetAt(0x20)}, ""},
		{"nothing usable", Frame{}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := frameKey(test.frame); got != test.want {
				t.Fatalf("frameKey(%+v) = %q, want %q", test.frame, got, test.want)
			}
		})
	}
}
