import { DetailSection } from "./DetailSection";
import type { Highlight } from "./highlights";

interface HighlightsPanelProps {
  /** Already built from the event's single parse — see `parsedEvent.ts`. */
  highlights: readonly Highlight[];
}

/** The handful of facts most useful for triage, first on the panel stack — never an empty card. */
export function HighlightsPanel({ highlights }: HighlightsPanelProps) {
  if (highlights.length === 0) {
    return null;
  }
  return (
    <DetailSection id="section-highlights" title="Highlights">
      <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-sm sm:grid-cols-4">
        {highlights.map((highlight) => (
          <div key={highlight.label} className="min-w-0">
            <dt className="text-muted-foreground text-xs">{highlight.label}</dt>
            <dd className="line-clamp-3 font-mono text-xs break-words" title={highlight.value}>
              {highlight.value}
            </dd>
          </div>
        ))}
      </dl>
    </DetailSection>
  );
}
