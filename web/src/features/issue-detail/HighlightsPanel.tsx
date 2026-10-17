import type { Highlight } from "./highlights";

interface HighlightsPanelProps {
  /** Already built from the event's single parse — see `parsedEvent.ts`. */
  highlights: readonly Highlight[];
}

/**
 * One line of who and where, directly above the trace. A strip rather than a
 * card: it is context for reading the trace, not a destination of its own, so
 * it takes no jump-nav entry and no room the trace could use.
 */
export function HighlightsPanel({ highlights }: HighlightsPanelProps) {
  if (highlights.length === 0) {
    return null;
  }
  return (
    <section aria-label="Highlights">
      <dl className="flex flex-wrap gap-x-5 gap-y-1 px-1 text-xs">
      {highlights.map((highlight) => (
        <div key={highlight.label} className="flex min-w-0 items-baseline gap-1.5">
          <dt className="text-muted-foreground shrink-0">{highlight.label}</dt>
          <dd className="max-w-xs truncate font-mono" title={highlight.value}>
            {highlight.value}
          </dd>
        </div>
      ))}
      </dl>
    </section>
  );
}
