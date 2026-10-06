package grouping

import (
	"strconv"
	"strings"
)

// mergeTemplateParts are the literal template parts merge mints for a target
// issue. MergeTemplate joins them with "," for storage in grouping_rules;
// MergeFingerprint hashes them the way ParsedTemplate.Resolve does after
// splitting that stored template back apart on ",".
//
// Deriving both from this one slice is what keeps the stored alias rule and
// the minted issues.fingerprint in agreement. Hashing the *joined* string
// instead (hashParts("merged,issue-9")) yields a DIFFERENT digest from
// hashParts("merged", "issue-9") — hashParts joins with "\n", not "," — and
// the mismatch is silent: a future event resolving through the alias rule
// would compute a fingerprint matching no issue, so the batch writer's issue
// upsert would create a brand-new issue instead of routing to the merge
// target, defeating the whole feature.
func mergeTemplateParts(targetIssueID int64) []string {
	return []string{"merged", "issue-" + strconv.FormatInt(targetIssueID, 10)}
}

// MergeTemplate is the literal grouping-rule template minted for the two
// alias rules created when targetIssueID becomes a merge target.
func MergeTemplate(targetIssueID int64) string {
	return strings.Join(mergeTemplateParts(targetIssueID), ",")
}

// MergeFingerprint is the new issues.fingerprint minted for targetIssueID.
// It must equal what ParseTemplate(MergeTemplate(targetIssueID)).Resolve
// produces, so it is derived from the same mergeTemplateParts slice rather
// than hashed independently.
func MergeFingerprint(targetIssueID int64) string {
	return hashParts(mergeTemplateParts(targetIssueID)...)
}
