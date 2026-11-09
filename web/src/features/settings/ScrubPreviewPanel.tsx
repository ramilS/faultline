import { useRef, useState } from "react";

import type { ScrubPreview, ScrubPreviewSource, ScrubRule } from "@/api";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { messageFor } from "@/errors";

import { DEFAULT_SAMPLE, MAX_SAMPLE_LENGTH } from "./scrubRules";

export type RunScrubPreview = (rules: ScrubRule[], source: ScrubPreviewSource) => Promise<ScrubPreview>;

type PreviewState =
  | { readonly status: "idle" }
  | { readonly status: "running" }
  | { readonly status: "ready"; readonly preview: ScrubPreview }
  | { readonly status: "failed"; readonly message: string };

function usePreview(runPreview: RunScrubPreview) {
  const [state, setState] = useState<PreviewState>({ status: "idle" });
  const latest = useRef(0);

  const run = async (rules: ScrubRule[], source: ScrubPreviewSource) => {
    const request = ++latest.current;
    setState({ status: "running" });
    try {
      const preview = await runPreview(rules, source);
      if (request === latest.current) {
        setState({ status: "ready", preview });
      }
    } catch (caught) {
      if (request === latest.current) {
        setState({ status: "failed", message: messageFor(caught) });
      }
    }
  };

  return { state, run };
}

interface ScrubPreviewPanelProps {
  rules: ScrubRule[];
  runPreview: RunScrubPreview;
  /** Undefined where there is no project to take an event from. */
  hasEvents?: boolean;
}

export function ScrubPreviewPanel({ rules, runPreview, hasEvents }: ScrubPreviewPanelProps) {
  const [sample, setSample] = useState(DEFAULT_SAMPLE);
  const { state, run } = usePreview(runPreview);

  return (
    <section aria-labelledby="scrub-preview-heading" className="space-y-2">
      <h3 id="scrub-preview-heading" className="text-sm font-medium">
        Preview
      </h3>
      <p className="text-muted-foreground text-sm">
        The server scrubs the sample with the settings on this page, saved or not, and stores nothing. Highlighted
        text is what scrubbing wrote.
      </p>
      <Label htmlFor="scrub-sample">Test sample</Label>
      <textarea
        id="scrub-sample"
        value={sample}
        rows={8}
        maxLength={MAX_SAMPLE_LENGTH}
        onChange={(event) => setSample(event.target.value)}
        className="border-input bg-background focus-visible:ring-ring/50 w-full rounded-md border px-3 py-2 font-mono text-sm shadow-xs outline-none focus-visible:ring-[3px]"
      />
      <div className="flex flex-wrap items-center gap-2">
        <Button type="button" variant="outline" disabled={state.status === "running"} onClick={() => void run(rules, { sample })}>
          Preview sample
        </Button>
        {hasEvents !== undefined && (
          <>
            <Button
              type="button"
              variant="outline"
              disabled={!hasEvents || state.status === "running"}
              onClick={() => void run(rules, { latest_event: true })}
            >
              Use latest event
            </Button>
            {!hasEvents && <span className="text-muted-foreground text-sm">No events yet</span>}
          </>
        )}
      </div>
      {state.status === "failed" && (
        <p role="alert" className="text-destructive text-sm break-words">
          {state.message}
        </p>
      )}
      {state.status === "ready" && <HighlightedPreview preview={state.preview} />}
    </section>
  );
}

export function HighlightedPreview({ preview }: { preview: ScrubPreview }) {
  return (
    <pre
      aria-label="Result"
      className="bg-muted max-h-96 overflow-auto rounded-md p-3 font-mono text-sm break-all whitespace-pre-wrap"
    >
      {preview.segments.map((segment, index) =>
        segment.redacted ? (
          <mark key={index} className="rounded-sm bg-amber-200 px-0.5 text-amber-950 dark:bg-amber-500/30 dark:text-amber-100">
            {segment.text}
          </mark>
        ) : (
          <span key={index}>{segment.text}</span>
        )
      )}
    </pre>
  );
}
