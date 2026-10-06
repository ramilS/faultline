import { useState, type FormEvent } from "react";

import type { CreateGroupingRuleInput, GroupingRule } from "@/api";
import { api } from "@/apiClient";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { DialogFooter } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { messageFor } from "@/errors";
import {
  hasRuleFormError,
  ruleFormErrors,
  NO_RULE_FORM_ERRORS
} from "@/features/settings/ruleFormValidation";
import { buildMatcher, parseMatcher, previewMatcher, type MatcherField, type MatcherInputs } from "@/groupingMatcher";

const FIELD_OPTIONS: ReadonlyArray<{ value: MatcherField; label: string }> = [
  { value: "error.type", label: "Error type" },
  { value: "message", label: "Message" },
  { value: "logger", label: "Logger" },
  { value: "tags", label: "Tag" }
];

interface RuleFormProps {
  slug: string;
  /** null creates a rule; a rule edits it in place. */
  rule: GroupingRule | null;
  onSaved: () => Promise<void>;
  onCancel: () => void;
}

function initialInputs(rule: GroupingRule | null): MatcherInputs {
  return rule === null ? { field: "error.type", pattern: "", tagKey: "" } : parseMatcher(rule.matcher);
}

export function RuleForm({ slug, rule, onSaved, onCancel }: RuleFormProps) {
  const [inputs, setInputs] = useState<MatcherInputs>(() => initialInputs(rule));
  const [template, setTemplate] = useState(rule?.template ?? "");
  // A fingerprint rule cannot be displayed by a select whose fingerprint option
  // is hidden, so editing one forces the advanced field into view.
  const [advanced, setAdvanced] = useState(() => initialInputs(rule).field === "fingerprint");
  const [errors, setErrors] = useState(NO_RULE_FORM_ERRORS);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const clearErrors = () => {
    setErrors(NO_RULE_FORM_ERRORS);
    setSubmitError(null);
  };

  const updateInputs = (changes: Partial<MatcherInputs>) => {
    setInputs((current) => ({ ...current, ...changes }));
    clearErrors();
  };

  const updateTemplate = (value: string) => {
    setTemplate(value);
    clearErrors();
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const found = ruleFormErrors(inputs, template);
    if (hasRuleFormError(found)) {
      setErrors(found);
      return;
    }

    const input: CreateGroupingRuleInput = { matcher: buildMatcher(inputs), template };
    setSubmitting(true);
    setSubmitError(null);
    try {
      if (rule === null) {
        await api.createGroupingRule(slug, input);
      } else {
        await api.updateGroupingRule(slug, rule.id, input);
      }
      await onSaved();
    } catch (caught) {
      setSubmitError(messageFor(caught));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <form onSubmit={submit}>
      <div className="space-y-4 py-4">
        {submitError !== null && (
          <p role="alert" className="text-destructive text-sm">
            {submitError}
          </p>
        )}

        <div className="space-y-2">
          <Label htmlFor="grouping-rule-field">Field</Label>
          <Select value={inputs.field} onValueChange={(value) => updateInputs({ field: value as MatcherField })}>
            <SelectTrigger id="grouping-rule-field" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {FIELD_OPTIONS.map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {option.label}
                </SelectItem>
              ))}
              {advanced && <SelectItem value="fingerprint">Fingerprint alias</SelectItem>}
            </SelectContent>
          </Select>
        </div>

        {inputs.field === "tags" && (
          <div className="space-y-2">
            <Label htmlFor="grouping-rule-tag-key">Tag key</Label>
            <Input
              id="grouping-rule-tag-key"
              value={inputs.tagKey}
              onChange={(event) => updateInputs({ tagKey: event.target.value })}
              placeholder="service"
              className="font-mono"
              aria-invalid={errors.tagKey !== null}
              aria-describedby={errors.tagKey === null ? undefined : "grouping-rule-tag-key-error"}
            />
            {errors.tagKey !== null && (
              <p id="grouping-rule-tag-key-error" role="alert" className="text-destructive text-xs">
                {errors.tagKey}
              </p>
            )}
          </div>
        )}

        <div className="space-y-2">
          <Label htmlFor="grouping-rule-pattern">Pattern</Label>
          <Input
            id="grouping-rule-pattern"
            value={inputs.pattern}
            onChange={(event) => updateInputs({ pattern: event.target.value })}
            placeholder={inputs.field === "fingerprint" ? "64-character fingerprint hex" : "exact value or *wildcard*"}
            className="font-mono"
            aria-invalid={errors.pattern !== null}
            aria-describedby={errors.pattern === null ? undefined : "grouping-rule-pattern-error"}
          />
          {errors.pattern !== null && (
            <p id="grouping-rule-pattern-error" role="alert" className="text-destructive text-xs">
              {errors.pattern}
            </p>
          )}
        </div>

        <div className="space-y-2">
          <Label htmlFor="grouping-rule-template">Fingerprint template</Label>
          <Input
            id="grouping-rule-template"
            value={template}
            onChange={(event) => updateTemplate(event.target.value)}
            placeholder="db-timeout,{tags.service}"
            className="font-mono"
            aria-invalid={errors.template !== null}
            aria-describedby={errors.template === null ? undefined : "grouping-rule-template-error"}
          />
          {errors.template !== null && (
            <p id="grouping-rule-template-error" role="alert" className="text-destructive text-xs">
              {errors.template}
            </p>
          )}
          <p className="text-muted-foreground text-xs">
            Comma-separated literals and <code className="font-mono">{"{field}"}</code> substitutions.
          </p>
        </div>

        <RulePreview inputs={inputs} template={template} />

        <div className="flex items-start gap-2">
          <Checkbox
            id="grouping-rule-advanced"
            // Radix renders a button, which takes its accessible name from its
            // content rather than from an associated <label>, so the name has
            // to be wired explicitly.
            aria-labelledby="grouping-rule-advanced-label"
            checked={advanced}
            onCheckedChange={(checked) => {
              const enabled = checked === true;
              setAdvanced(enabled);
              // Hiding the option while it is selected would leave the select
              // pointing at a value with no matching item.
              if (!enabled && inputs.field === "fingerprint") {
                updateInputs({ field: "error.type" });
              }
            }}
          />
          <Label
            id="grouping-rule-advanced-label"
            htmlFor="grouping-rule-advanced"
            className="text-muted-foreground text-xs leading-snug font-normal"
          >
            Show the fingerprint-alias field (advanced — these are normally generated by an issue merge)
          </Label>
        </div>
      </div>

      <DialogFooter>
        <Button type="button" variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" disabled={submitting}>
          {submitting ? "Saving…" : rule === null ? "Add rule" : "Save rule"}
        </Button>
      </DialogFooter>
    </form>
  );
}

interface RulePreviewProps {
  inputs: MatcherInputs;
  template: string;
}

/** Exactly what submission will send, including automatic pattern quoting. */
function RulePreview({ inputs, template }: RulePreviewProps) {
  return (
    <div className="bg-muted/50 space-y-1 rounded-md border p-3 text-xs">
      <p className="text-muted-foreground">Preview</p>
      <p className="font-mono break-all">
        {previewMatcher(inputs)} <span aria-hidden="true">→</span>{" "}
        <span className="sr-only">groups as</span>
        {template === "" ? "<template>" : template}
      </p>
    </div>
  );
}
