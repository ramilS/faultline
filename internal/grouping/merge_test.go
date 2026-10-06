package grouping

import "testing"

func TestMergeTemplateIsStableForATargetID(t *testing.T) {
	if MergeTemplate(9) != "merged,issue-9" {
		t.Fatalf("MergeTemplate(9) = %q", MergeTemplate(9))
	}
	if MergeTemplate(9) == MergeTemplate(10) {
		t.Fatal("different target ids must produce different templates")
	}
}

func TestMergeFingerprintMatchesResolvedMergeTemplate(t *testing.T) {
	parsed, err := ParseTemplate(MergeTemplate(9))
	if err != nil {
		t.Fatalf("ParseTemplate(MergeTemplate(9)): %v", err)
	}
	resolved, ok := parsed.Resolve(EvaluationInput{})
	if !ok {
		t.Fatal("expected literal merge template to resolve")
	}
	if MergeFingerprint(9) != resolved {
		t.Fatalf("MergeFingerprint(9) = %q, want %q (must match a resolved alias-rule template)", MergeFingerprint(9), resolved)
	}
}

// The merge-minted fingerprint MUST equal what pass-2 evaluation of the
// generated alias rule produces, or merged issues silently split apart.
func TestMergeFingerprintMatchesEvaluatedAliasRule(t *testing.T) {
	const targetID = 9
	deterministic := Result{Fingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Title: "T", Culprit: "c"}
	aliasRule := Rule{
		ID: 1, ProjectID: 1, Position: 1,
		Matcher:  "fingerprint:" + deterministic.Fingerprint,
		Template: MergeTemplate(targetID),
	}

	got := EvaluateRules([]Rule{aliasRule}, EvaluationInput{}, deterministic)
	if got.Fingerprint != MergeFingerprint(targetID) {
		t.Fatalf("evaluated alias rule produced %q, but MergeFingerprint(%d) is %q — "+
			"future events would route to a new issue instead of the merge target",
			got.Fingerprint, targetID, MergeFingerprint(targetID))
	}
}
