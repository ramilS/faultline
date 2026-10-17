import type { EventSection } from "./eventSections";

interface EventSectionNavProps {
  sections: readonly EventSection[];
}

/**
 * Links only to sections actually present on this event — no dead anchors.
 *
 * No too-few-sections guard: `buildEventSections` always emits stack trace,
 * breadcrumbs and raw payload, so the shortest nav this can be handed is
 * three entries. That floor is pinned in `eventSections.test.ts`, where it
 * can actually fail.
 */
export function EventSectionNav({ sections }: EventSectionNavProps) {
  return (
    <nav aria-label="Jump to section" className="flex flex-wrap gap-x-3 gap-y-1 text-xs">
      {sections.map((section) => (
        <a key={section.id} href={`#${section.id}`} className="text-muted-foreground hover:text-foreground">
          {section.label}
        </a>
      ))}
    </nav>
  );
}
