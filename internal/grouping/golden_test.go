package grouping

import "testing"

// A changed value splits every existing issue of that kind.
func TestFingerprintGoldenValues(t *testing.T) {
	cases := []struct {
		name  string
		input GroupingInput
		want  string
	}{
		{"javascript", GroupingInput{Exception: &Exception{Type: "TypeError", Value: "x is undefined", Frames: []Frame{
			{Module: "app:///static/js/main.9c1e.js?v=3", Function: "submitOrder", InApp: true},
			{Module: "app:///static/js/main.9c1e.js", Function: "<anonymous>", InApp: true},
			{Module: "app:///static/js/vendor.4f2a.js", Function: "invokeGuardedCallback"},
		}}}, "5404c2f34d1b617dbf090ae224d24cc1e060d682dbe13e178d655672c7d028fb"},
		{"jvm", GroupingInput{Exception: &Exception{Type: "IllegalStateException", Frames: []Frame{
			{Module: "com.acme.billing.Charges", Function: "$anonfun$chargeCard$3", InApp: true},
			{Module: "com.acme.billing.OrderRepository$$EnhancerBySpringCGLIB$$5f1a", Function: "find", InApp: true},
			{Module: "com.acme.billing.OrderService$1", Function: "load$default", InApp: true},
			{Module: "com.acme.billing.OrderController$$Lambda$412/0x0000000800c4b040", Function: "apply", InApp: true},
			{Module: "java.lang.Thread", Function: "run"},
		}}}, "f8d01c9ddb4528f55de40cbf894f940415c1ec189567a1f0badc7d44e91c24b4"},
		{"python", GroupingInput{Logger: "billing", Exception: &Exception{Type: "KeyError", Value: "'customer_id'", Frames: []Frame{
			{Module: "shop.billing", Function: "charge", InApp: true},
			{Module: "shop.views", Function: "checkout", InApp: true},
			{Module: "django.core.handlers.base", Function: "_get_response"},
		}}}, "40debacb0a125f2edf02d749dc785eabb23478ea3417ad39e68ae6e01fb05bdf"},
		{"native offset", nativeCrash(
			Frame{Package: "/private/var/a/MyApp.app/MyApp", ImageOffset: new(uint64(0xc000)), InApp: true},
			Frame{Package: "/usr/lib/system/libdyld.dylib", ImageOffset: new(uint64(0x1234))},
		), "f2b337f23d821626e3872bec35390b09cc3a226ba9ef84e9861ef621f2564ce5"},
		{"message", GroupingInput{Logger: "cron", Message: "job 9172 exceeded 30s"}, "0a3f73073205961a6547477b56dcbdca4429d7e9e844e503adba16176784583a"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := Fingerprint(testCase.input).Fingerprint; got != testCase.want {
				t.Fatalf("fingerprint = %s, want %s", got, testCase.want)
			}
		})
	}
}
