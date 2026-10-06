package grouping

import "testing"

func TestParseTemplateLiteralsAndSubstitutions(t *testing.T) {
	parsed, err := ParseTemplate("db-timeout, {tags.service}, literal-suffix")
	if err != nil {
		t.Fatalf("ParseTemplate: %v", err)
	}
	if len(parsed.Parts) != 3 ||
		parsed.Parts[0] != (TemplatePart{Literal: "db-timeout"}) ||
		parsed.Parts[1] != (TemplatePart{Field: "tags.service"}) ||
		parsed.Parts[2] != (TemplatePart{Literal: "literal-suffix"}) {
		t.Fatalf("parts = %+v", parsed.Parts)
	}
}

func TestParseTemplateRejectsEmptyAndUnknownField(t *testing.T) {
	for _, raw := range []string{"", "   ", "a,,b", "{fingerprint}", "{unknown}", "{unterminated", "{}", "{   }", "literal,"} {
		if _, err := ParseTemplate(raw); err == nil {
			t.Fatalf("ParseTemplate(%q): expected error", raw)
		}
	}
}

func TestResolveJoinsAndHashesLiteralTemplate(t *testing.T) {
	parsed, err := ParseTemplate("payments,payment-failure")
	if err != nil {
		t.Fatalf("ParseTemplate: %v", err)
	}
	hash, ok := parsed.Resolve(EvaluationInput{})
	if !ok {
		t.Fatal("expected literal-only template to resolve")
	}
	want := hashParts("payments", "payment-failure")
	if hash != want {
		t.Fatalf("hash = %q, want %q", hash, want)
	}
}

func TestResolveSubstitutesFieldsFromEvent(t *testing.T) {
	parsed, err := ParseTemplate("jobs,{tags.job}")
	if err != nil {
		t.Fatalf("ParseTemplate: %v", err)
	}
	hash, ok := parsed.Resolve(EvaluationInput{Tags: map[string]string{"job": "invoice-42"}})
	if !ok {
		t.Fatal("expected resolvable template")
	}
	if hash != hashParts("jobs", "invoice-42") {
		t.Fatalf("hash = %q, want hashParts(jobs, invoice-42)", hash)
	}
}

func TestResolveSkipsRuleOnUnresolvableField(t *testing.T) {
	parsed, err := ParseTemplate("jobs,{tags.missing}")
	if err != nil {
		t.Fatalf("ParseTemplate: %v", err)
	}
	if _, ok := parsed.Resolve(EvaluationInput{Tags: map[string]string{}}); ok {
		t.Fatal("expected unresolved template (absent tag)")
	}
	messageTemplate, err := ParseTemplate("{message}")
	if err != nil {
		t.Fatalf("ParseTemplate: %v", err)
	}
	if _, ok := messageTemplate.Resolve(EvaluationInput{Message: ""}); ok {
		t.Fatal("expected unresolved template (empty message)")
	}
}
