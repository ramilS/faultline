import { useState } from "react";

import type { ScrubRule } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

import { AddScrubRuleDialog } from "./AddScrubRuleDialog";
import {
  MAX_SCRUB_RULES,
  MAX_SCRUB_RULE_NAME_BYTES,
  MAX_SCRUB_RULE_PATTERN_BYTES,
  MAX_SCRUB_RULE_REPLACEMENT_BYTES
} from "./scrubRules";
import { ScrubPreviewPanel, type RunScrubPreview } from "./ScrubPreviewPanel";

interface ScrubRulesEditorProps {
  rules: ScrubRule[];
  editable: boolean;
  disabled: boolean;
  onChange: (rules: ScrubRule[]) => void;
  runPreview: RunScrubPreview;
  onAddField?: (name: string) => void;
  hasEvents?: boolean;
}

export function ScrubRulesEditor({ rules, editable, disabled, onChange, runPreview, onAddField, hasEvents }: ScrubRulesEditorProps) {
  const [adding, setAdding] = useState(false);
  const locked = !editable || disabled;
  const atLimit = rules.length >= MAX_SCRUB_RULES;

  const update = (index: number, field: keyof ScrubRule, value: string) =>
    onChange(rules.map((rule, position) => (position === index ? { ...rule, [field]: value } : rule)));

  return (
    <section className="space-y-4">
      <div className="space-y-1">
        <h2 className="text-base font-medium">Custom rules</h2>
        <p className="text-muted-foreground text-sm">
          Each rule replaces what its pattern matches in every string of an event, including URLs and breadcrumbs.
          Applies to events received after the change.
        </p>
      </div>

      {rules.length === 0 && (
        <p className="text-muted-foreground rounded-md border border-dashed p-3 text-sm">
          No custom rules. The defaults above still apply.
        </p>
      )}

      {rules.map((rule, index) => {
        const number = index + 1;
        return (
          <fieldset key={index} className="space-y-2 rounded-md border p-3">
            <legend className="sr-only">Rule {number}</legend>
            <div className="grid gap-2 sm:grid-cols-3">
              <div className="space-y-1">
                <Label htmlFor={`rule-${number}-name`}>Rule {number} name</Label>
                <Input
                  id={`rule-${number}-name`}
                  value={rule.name}
                  maxLength={MAX_SCRUB_RULE_NAME_BYTES}
                  disabled={locked}
                  onChange={(event) => update(index, "name", event.target.value)}
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor={`rule-${number}-pattern`}>Rule {number} pattern</Label>
                <Input
                  id={`rule-${number}-pattern`}
                  className="font-mono"
                  value={rule.pattern}
                  maxLength={MAX_SCRUB_RULE_PATTERN_BYTES}
                  disabled={locked}
                  onChange={(event) => update(index, "pattern", event.target.value)}
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor={`rule-${number}-replacement`}>Rule {number} replacement</Label>
                <Input
                  id={`rule-${number}-replacement`}
                  className="font-mono"
                  value={rule.replacement}
                  maxLength={MAX_SCRUB_RULE_REPLACEMENT_BYTES}
                  disabled={locked}
                  onChange={(event) => update(index, "replacement", event.target.value)}
                />
              </div>
            </div>
            {rule.description !== undefined && rule.description !== "" && (
              <p className="text-muted-foreground text-sm">{rule.description}</p>
            )}
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={locked}
              onClick={() => onChange(rules.filter((_, position) => position !== index))}
            >
              Remove rule {number}
            </Button>
          </fieldset>
        );
      })}

      {editable && (
        <div className="space-y-1">
          <Button type="button" variant="outline" disabled={locked || atLimit} onClick={() => setAdding(true)}>
            Add rule
          </Button>
          {atLimit && (
            <p className="text-muted-foreground text-sm">
              The limit of {MAX_SCRUB_RULES} rules is reached. Remove a rule to add another.
            </p>
          )}
        </div>
      )}

      {editable && (
        <>
          <ScrubPreviewPanel rules={rules} runPreview={runPreview} hasEvents={hasEvents} />
          <AddScrubRuleDialog
            open={adding}
            rules={rules}
            runPreview={runPreview}
            onClose={() => setAdding(false)}
            onAddRule={(rule) => onChange([...rules, rule])}
            onAddField={onAddField}
          />
        </>
      )}
    </section>
  );
}
