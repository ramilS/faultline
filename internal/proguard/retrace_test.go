package proguard

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const shopMapping = `# compiler: R8
# compiler_version: 8.5.35
com.acme.shop.Cart -> a.a:
    java.util.List items -> a
    9:9:void <init>() -> <init>
    10:20:void load(int):100:110 -> c
    21:33:int total(int):201:213 -> b
    35:35:void clear():60:60 -> d
    void untouched() -> e
    void clean() -> f
    void wipe() -> f
com.acme.shop.Checkout -> a.b:
    5:5:int com.acme.shop.Cart.total(int):201:201 -> a
    5:5:void submit(com.acme.shop.Cart):14:14 -> a
    6:9:void pay(int):30:33 -> b
    void pay(java.lang.String) -> b
com.acme.shop.Outer$Inner -> a.c:
    3:3:void run():7:7 -> a
`

func parseAll(t *testing.T, text string) *Mapping {
	t.Helper()
	mapping, err := Parse(strings.NewReader(text), func(string) bool { return true })
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return mapping
}

func TestRetrace(t *testing.T) {
	mapping := parseAll(t, shopMapping)
	for _, test := range []struct {
		name  string
		frame Frame
		want  []Original
	}{
		{"line inside a range maps linearly", Frame{"a.a", "c", 12}, []Original{{Class: "com.acme.shop.Cart", Method: "load", Line: 102, Resolved: true}}},
		{"first line of a range", Frame{"a.a", "c", 10}, []Original{{Class: "com.acme.shop.Cart", Method: "load", Line: 100, Resolved: true}}},
		{"last line of a range", Frame{"a.a", "c", 20}, []Original{{Class: "com.acme.shop.Cart", Method: "load", Line: 110, Resolved: true}}},
		{"single line range", Frame{"a.a", "d", 35}, []Original{{Class: "com.acme.shop.Cart", Method: "clear", Line: 60, Resolved: true}}},
		{"range without original lines keeps the line", Frame{"a.a", "<init>", 9}, []Original{{Class: "com.acme.shop.Cart", Method: "<init>", Line: 9, Resolved: true}}},
		{"method without any line info", Frame{"a.a", "e", 0}, []Original{{Class: "com.acme.shop.Cart", Method: "untouched", Resolved: true}}},
		{"inlined frames come innermost first", Frame{"a.b", "a", 5}, []Original{
			{Class: "com.acme.shop.Cart", Method: "total", Line: 201, Inlined: true, Resolved: true},
			{Class: "com.acme.shop.Checkout", Method: "submit", Line: 14, Resolved: true},
		}},
		{"line outside every range falls back to the class", Frame{"a.a", "c", 99}, []Original{{Class: "com.acme.shop.Cart", Method: "c", Line: 99, Resolved: false}}},
		{"unknown method falls back to the class", Frame{"a.a", "zz", 12}, []Original{{Class: "com.acme.shop.Cart", Method: "zz", Line: 12, Resolved: false}}},
		{"overloads of one original name resolve without a line", Frame{"a.b", "b", 0}, []Original{{Class: "com.acme.shop.Checkout", Method: "pay", Resolved: true}}},
		{"different originals behind one name are ambiguous without a line", Frame{"a.a", "f", 0}, []Original{{Class: "com.acme.shop.Cart", Method: "f", Resolved: false}}},
		{"overload selected by line", Frame{"a.b", "b", 7}, []Original{{Class: "com.acme.shop.Checkout", Method: "pay", Line: 31, Resolved: true}}},
		{"nested class keeps its dollar name", Frame{"a.c", "a", 3}, []Original{{Class: "com.acme.shop.Outer$Inner", Method: "run", Line: 7, Resolved: true}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := mapping.Retrace(test.frame)
			if !ok {
				t.Fatalf("Retrace(%+v) found no class", test.frame)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Retrace(%+v) = %+v, want %+v", test.frame, got, test.want)
			}
		})
	}
}

func TestRetraceReportsAnUnknownClass(t *testing.T) {
	mapping := parseAll(t, shopMapping)

	if got, ok := mapping.Retrace(Frame{"x.y", "m", 3}); ok {
		t.Fatalf("Retrace of an unmapped class = %+v, want not found", got)
	}
}

func TestParseKeepsOnlyTheWantedClasses(t *testing.T) {
	mapping, err := Parse(strings.NewReader(shopMapping), func(obfuscated string) bool { return obfuscated == "a.c" })
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if _, ok := mapping.Retrace(Frame{"a.a", "c", 12}); ok {
		t.Fatal("an unwanted class must not be retained")
	}
	if _, ok := mapping.Retrace(Frame{"a.c", "a", 3}); !ok {
		t.Fatal("the wanted class must be retained")
	}
}

func TestParseMergesRepeatedClassBlocks(t *testing.T) {
	mapping := parseAll(t, "com.x.A -> a:\n    1:1:void one():5:5 -> a\ncom.x.A -> a:\n    2:2:void two():9:9 -> b\n")

	for frame, want := range map[Frame]string{{"a", "a", 1}: "one", {"a", "b", 2}: "two"} {
		got, ok := mapping.Retrace(frame)
		if !ok || len(got) != 1 || got[0].Method != want {
			t.Fatalf("Retrace(%+v) = %+v, %v; want method %q", frame, got, ok, want)
		}
	}
}

func TestParseIgnoresCommentsAndFields(t *testing.T) {
	mapping := parseAll(t, "# header\ncom.x.A -> a:\n    # {\"id\":\"sourceFile\",\"fileName\":\"A.kt\"}\n    int count -> a\n    1:1:void run():3:3 -> b\n")

	got, ok := mapping.Retrace(Frame{"a", "b", 1})
	if !ok || len(got) != 1 || got[0].Method != "run" || got[0].Line != 3 {
		t.Fatalf("Retrace = %+v, %v", got, ok)
	}
}

func TestParseToleratesWindowsLineEndings(t *testing.T) {
	mapping := parseAll(t, strings.ReplaceAll("com.x.A -> a:\n    1:1:void run():3:3 -> b\n", "\n", "\r\n"))

	if got, ok := mapping.Retrace(Frame{"a", "b", 1}); !ok || got[0].Method != "run" {
		t.Fatalf("Retrace = %+v, %v", got, ok)
	}
}

func TestValidate(t *testing.T) {
	for _, test := range []struct {
		name    string
		text    string
		wantErr bool
	}{
		{"a real mapping", shopMapping, false},
		{"no class line", "# only comments\n\n", true},
		{"empty", "", true},
		{"binary noise", "\x00\x01\x02 not a mapping\n", true},
		{"member before any class", "    1:1:void run():3:3 -> b\n", true},
		{"a line beyond the bound", "com.x.A -> a:\n    " + strings.Repeat("x", maxLineBytes+1) + "\n", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(strings.NewReader(test.text))
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, test.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidMapping) {
				t.Fatalf("Validate() error = %v, want ErrInvalidMapping", err)
			}
		})
	}
}

func TestSourceFile(t *testing.T) {
	for class, want := range map[string]string{
		"com.acme.shop.Cart":        "Cart.java",
		"com.acme.shop.Outer$Inner": "Outer.java",
		"Top":                       "Top.java",
		"com.acme.CartKt":           "CartKt.java",
		"com.acme.$Generated":       "$Generated.java",
	} {
		if got := SourceFile(class); got != want {
			t.Errorf("SourceFile(%q) = %q, want %q", class, got, want)
		}
	}
}
