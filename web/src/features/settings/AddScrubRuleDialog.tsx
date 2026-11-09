import { useState } from "react";

import type { ScrubPreview, ScrubRule } from "@/api";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { messageFor } from "@/errors";

import { MAX_SCRUB_FIELD_LENGTH } from "./scrubFields";
import {
  isPresetAdded,
  MAX_SAMPLE_LENGTH,
  MAX_SCRUB_RULE_NAME_BYTES,
  MAX_SCRUB_RULE_PATTERN_BYTES,
  MAX_SCRUB_RULE_REPLACEMENT_BYTES,
  presetRule,
  SCRUB_PRESETS,
  validateRules
} from "./scrubRules";
import { HighlightedPreview, type RunScrubPreview } from "./ScrubPreviewPanel";

type HideKind = "field" | "preset" | "custom";

interface AddScrubRuleDialogProps {
  open: boolean;
  rules: ScrubRule[];
  runPreview: RunScrubPreview;
  onClose: () => void;
  onAddRule: (rule: ScrubRule) => void;
  /** Present only where the page has field lists to add a name to. */
  onAddField?: (name: string) => void;
}

export function AddScrubRuleDialog({ open, rules, runPreview, onClose, onAddRule, onAddField }: AddScrubRuleDialogProps) {
  const [kind, setKind] = useState<HideKind | null>(null);

  const close = () => {
    setKind(null);
    onClose();
  };

  const kinds: { kind: HideKind; label: string; hint: string }[] = [
    ...(onAddField === undefined
      ? []
      : [{ kind: "field" as const, label: "A field", hint: "Everything stored under a key such as phone or ssn." }]),
    { kind: "preset", label: "A known kind of value", hint: "Email addresses, tokens and keys, wherever they appear." },
    { kind: "custom", label: "A custom pattern", hint: "Your own regular expression." }
  ];

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          close();
        }
      }}
    >
      {open && (
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>What do you want to hide?</DialogTitle>
            <DialogDescription>Hidden values are replaced before an event is stored.</DialogDescription>
          </DialogHeader>

          <div role="group" aria-label="What to hide" className="grid gap-2 sm:grid-cols-3">
            {kinds.map((option) => (
              <button
                key={option.kind}
                type="button"
                aria-pressed={kind === option.kind}
                onClick={() => setKind(option.kind)}
                className="hover:bg-accent aria-pressed:border-primary aria-pressed:bg-accent rounded-md border p-3 text-left"
              >
                <span className="block text-sm font-medium">{option.label}</span>
                <span className="text-muted-foreground block text-xs">{option.hint}</span>
              </button>
            ))}
          </div>

          {kind === "field" && onAddField !== undefined && (
            <FieldByName
              onAdd={(name) => {
                onAddField(name);
                close();
              }}
            />
          )}
          {kind === "preset" && (
            <PresetGallery
              rules={rules}
              onAdd={(rule) => {
                onAddRule(rule);
                close();
              }}
            />
          )}
          {kind === "custom" && (
            <CustomPattern
              runPreview={runPreview}
              onAdd={(rule) => {
                onAddRule(rule);
                close();
              }}
            />
          )}
        </DialogContent>
      )}
    </Dialog>
  );
}

function FieldByName({ onAdd }: { onAdd: (name: string) => void }) {
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);

  const add = () => {
    const trimmed = name.trim();
    if (trimmed === "") {
      setError("Enter a field name.");
      return;
    }
    if (new TextEncoder().encode(trimmed).length > MAX_SCRUB_FIELD_LENGTH) {
      setError(`Field names can be at most ${MAX_SCRUB_FIELD_LENGTH} bytes.`);
      return;
    }
    onAdd(trimmed);
  };

  return (
    <div className="space-y-2">
      <Label htmlFor="hide-field-name">Field name</Label>
      <Input
        id="hide-field-name"
        value={name}
        aria-describedby="hide-field-name-hint"
        aria-invalid={error !== null}
        onChange={(event) => {
          setName(event.target.value);
          setError(null);
        }}
      />
      <p id="hide-field-name-hint" className="text-muted-foreground text-sm">
        Any key containing this name, in any letter case, is replaced with [Filtered]. Saved with the additional
        sensitive fields.
      </p>
      {error !== null && (
        <p role="alert" className="text-destructive text-sm">
          {error}
        </p>
      )}
      <Button type="button" onClick={add}>
        Hide this field
      </Button>
    </div>
  );
}

function PresetGallery({ rules, onAdd }: { rules: ScrubRule[]; onAdd: (rule: ScrubRule) => void }) {
  return (
    <ul aria-label="Known kinds of value" className="space-y-2">
      {SCRUB_PRESETS.map((preset) => {
        const added = isPresetAdded(rules, preset);
        return (
          <li key={preset.name} className="flex items-start justify-between gap-3 rounded-md border p-3">
            <div className="min-w-0 space-y-1">
              <p className="text-sm font-medium">{preset.name}</p>
              <p className="text-muted-foreground text-sm">{preset.description}</p>
              <p className="text-xs">
                Matches <code className="bg-muted rounded px-1 break-all whitespace-pre-wrap">{preset.example}</code>
              </p>
            </div>
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={added}
              aria-label={added ? `${preset.name} added` : `Add ${preset.name}`}
              onClick={() => onAdd(presetRule(preset))}
            >
              {added ? "Added" : "Add"}
            </Button>
          </li>
        );
      })}
    </ul>
  );
}

const SYNTAX_EXAMPLES = [
  { pattern: "acct-\\d+", replacement: "", effect: "acct-1042 becomes [Filtered]" },
  { pattern: "(order-)\\d+", replacement: "${1}[Filtered]", effect: "order-77 becomes order-[Filtered]" },
  { pattern: "(?i)session=[^&\\s]+", replacement: "", effect: "SESSION=ab12 in a URL becomes [Filtered]" }
];

function CustomPattern({ runPreview, onAdd }: { runPreview: RunScrubPreview; onAdd: (rule: ScrubRule) => void }) {
  const [rule, setRule] = useState<ScrubRule>({ name: "", pattern: "", replacement: "" });
  const [sample, setSample] = useState("");
  const [patternError, setPatternError] = useState<string | null>(null);
  const [checking, setChecking] = useState(false);
  const [preview, setPreview] = useState<ScrubPreview | null>(null);

  const edit = (field: keyof ScrubRule) => (value: string) => {
    setRule((current) => ({ ...current, [field]: value }));
    setPatternError(null);
    setPreview(null);
  };

  const check = async (): Promise<boolean> => {
    const invalid = validateRules([rule]);
    if (invalid !== null) {
      setPatternError(invalid.replace(/^Rule 1: /, ""));
      return false;
    }
    setChecking(true);
    try {
      setPreview(await runPreview([rule], { sample }));
      return true;
    } catch (caught) {
      setPatternError(messageFor(caught));
      return false;
    } finally {
      setChecking(false);
    }
  };

  const add = async () => {
    if (await check()) {
      onAdd(rule);
    }
  };

  return (
    <div className="space-y-3">
      <div className="space-y-1">
        <Label htmlFor="custom-rule-name">Rule name</Label>
        <Input
          id="custom-rule-name"
          value={rule.name}
          maxLength={MAX_SCRUB_RULE_NAME_BYTES}
          onChange={(event) => edit("name")(event.target.value)}
        />
      </div>
      <div className="space-y-1">
        <Label htmlFor="custom-rule-pattern">Pattern</Label>
        <Input
          id="custom-rule-pattern"
          className="font-mono"
          value={rule.pattern}
          maxLength={MAX_SCRUB_RULE_PATTERN_BYTES}
          aria-invalid={patternError !== null}
          aria-describedby={patternError !== null ? "custom-rule-pattern-error" : undefined}
          onChange={(event) => edit("pattern")(event.target.value)}
        />
        {patternError !== null && (
          <p id="custom-rule-pattern-error" role="alert" className="text-destructive text-sm break-words">
            {patternError}
          </p>
        )}
      </div>
      <div className="space-y-1">
        <Label htmlFor="custom-rule-replacement">Replacement</Label>
        <Input
          id="custom-rule-replacement"
          className="font-mono"
          placeholder="[Filtered]"
          value={rule.replacement}
          maxLength={MAX_SCRUB_RULE_REPLACEMENT_BYTES}
          onChange={(event) => edit("replacement")(event.target.value)}
        />
      </div>

      <Collapsible>
        <CollapsibleTrigger className="text-sm font-medium underline-offset-4 hover:underline">
          Pattern syntax
        </CollapsibleTrigger>
        <CollapsibleContent className="text-muted-foreground mt-2 space-y-2 text-sm">
          <p>
            Patterns are RE2 regular expressions: no lookahead or backreferences. In the replacement {"${1}"} is the
            first group, $$ a literal dollar sign, and empty means [Filtered].
          </p>
          <ul className="space-y-1">
            {SYNTAX_EXAMPLES.map((example) => (
              <li key={example.pattern}>
                <code className="bg-muted rounded px-1">{example.pattern}</code>
                {example.replacement !== "" && (
                  <>
                    {" "}
                    → <code className="bg-muted rounded px-1">{example.replacement}</code>
                  </>
                )}
                : {example.effect}
              </li>
            ))}
          </ul>
        </CollapsibleContent>
      </Collapsible>

      <div className="space-y-1">
        <Label htmlFor="custom-rule-sample">Try it on</Label>
        <textarea
          id="custom-rule-sample"
          value={sample}
          rows={3}
          maxLength={MAX_SAMPLE_LENGTH}
          onChange={(event) => setSample(event.target.value)}
          className="border-input bg-background focus-visible:ring-ring/50 w-full rounded-md border px-3 py-2 font-mono text-sm shadow-xs outline-none focus-visible:ring-[3px]"
        />
      </div>
      <div className="flex flex-wrap gap-2">
        <Button type="button" variant="outline" disabled={checking} onClick={() => void check()}>
          Check pattern
        </Button>
        <Button type="button" disabled={checking} onClick={() => void add()}>
          {checking ? "Checking…" : "Add this rule"}
        </Button>
      </div>
      {preview !== null && <HighlightedPreview preview={preview} />}
    </div>
  );
}
