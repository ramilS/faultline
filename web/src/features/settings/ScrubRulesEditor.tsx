import { useState } from "react";

import type { ScrubRule } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

import {
  MAX_SAMPLE_LENGTH,
  MAX_SCRUB_RULES,
  MAX_SCRUB_RULE_NAME_BYTES,
  MAX_SCRUB_RULE_PATTERN_BYTES,
  MAX_SCRUB_RULE_REPLACEMENT_BYTES,
  previewRules,
  SCRUB_PRESETS
} from "./scrubRules";

interface ScrubRulesEditorProps {
  rules: ScrubRule[];
  editable: boolean;
  disabled: boolean;
  onChange: (rules: ScrubRule[]) => void;
}

export function ScrubRulesEditor({ rules, editable, disabled, onChange }: ScrubRulesEditorProps) {
  const [sample, setSample] = useState("");
  const locked = !editable || disabled;
  const atLimit = rules.length >= MAX_SCRUB_RULES;
  const preview = previewRules(rules, sample);

  const update = (index: number, field: keyof ScrubRule, value: string) =>
    onChange(rules.map((rule, position) => (position === index ? { ...rule, [field]: value } : rule)));

  return (
    <section className="space-y-4">
      <div className="space-y-1">
        <h2 className="text-base font-medium">Custom rules</h2>
        <p className="text-muted-foreground text-sm">
          Regular expression (RE2, no lookahead) replaced in every string of an event, including URLs and breadcrumbs.
          In the replacement {"${1}"} is the first group and $$ a literal dollar sign; empty means [Filtered]. Applies
          to events received after the change.
        </p>
      </div>

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
        <div className="flex flex-wrap gap-2">
          <Button
            type="button"
            variant="outline"
            disabled={locked || atLimit}
            onClick={() => onChange([...rules, { name: "", pattern: "", replacement: "" }])}
          >
            Add rule
          </Button>
          {SCRUB_PRESETS.map((preset) => (
            <Button
              key={preset.label}
              type="button"
              variant="outline"
              disabled={locked || atLimit}
              onClick={() => onChange([...rules, preset.rule])}
            >
              Add {preset.label}
            </Button>
          ))}
        </div>
      )}

      <div className="space-y-2">
        <Label htmlFor="scrub-sample">Test against sample text</Label>
        <textarea
          id="scrub-sample"
          value={sample}
          rows={3}
          maxLength={MAX_SAMPLE_LENGTH}
          disabled={locked}
          onChange={(event) => setSample(event.target.value)}
          className="border-input bg-background focus-visible:ring-ring/50 w-full rounded-md border px-3 py-2 font-mono text-sm shadow-xs outline-none focus-visible:ring-[3px] disabled:cursor-not-allowed disabled:opacity-50"
        />
        <p className="text-muted-foreground text-sm">Preview runs in your browser; the server (RE2) decides on save.</p>
        {preview.errors.length > 0 && (
          <div role="alert" className="text-destructive space-y-1 text-sm">
            {preview.errors.map((error, position) => (
              <p key={position}>{error}</p>
            ))}
          </div>
        )}
        <output aria-label="Result" className="block font-mono text-sm break-all whitespace-pre-wrap">
          {preview.output}
        </output>
      </div>
    </section>
  );
}
