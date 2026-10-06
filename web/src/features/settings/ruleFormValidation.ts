// Relative import rather than the "@/" alias: this module is covered by a
// node --test suite, which has no alias resolution.
import { validateMatcherInputs, type MatcherInputs } from "../../groupingMatcher.ts";

export interface RuleFormErrors {
  readonly tagKey: string | null;
  readonly pattern: string | null;
  readonly template: string | null;
}

export const NO_RULE_FORM_ERRORS: RuleFormErrors = { tagKey: null, pattern: null, template: null };

// A value that cannot itself fail validateMatcherInputs, used to silence one
// field while probing the other.
const FILLED = "x";

/**
 * validateMatcherInputs is the single source of truth for both matcher rules
 * but reports only the first failure, with no indication of which input caused
 * it. Probing it once per field — with the other field filled in — attributes
 * each message to the input the user must fix, without restating either
 * condition here where the two copies could drift.
 */
export function ruleFormErrors(inputs: MatcherInputs, template: string): RuleFormErrors {
  return {
    tagKey: validateMatcherInputs({ ...inputs, pattern: FILLED }),
    pattern: validateMatcherInputs({ ...inputs, tagKey: FILLED }),
    template: template.trim() === "" ? "Template is required." : null
  };
}

export function hasRuleFormError(errors: RuleFormErrors): boolean {
  return errors.tagKey !== null || errors.pattern !== null || errors.template !== null;
}
