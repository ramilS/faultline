import { useEffect, useRef } from "react";

import type { EventSection } from "./eventSections";
import { SECTION_NAV_TOP } from "./stickyHeader";
import { useActiveSection } from "./useActiveSection";

interface EventSectionNavProps {
  sections: readonly EventSection[];
}

export function EventSectionNav({ sections }: EventSectionNavProps) {
  const bar = useRef<HTMLElement>(null);
  const strip = useRef<HTMLDivElement>(null);
  const { activeId, select } = useActiveSection(sections, bar);

  useEffect(() => {
    const scroller = strip.current;
    const chip = scroller?.querySelector<HTMLElement>('[aria-current="location"]');
    if (scroller == null || chip == null) {
      return;
    }
    const chipEnd = chip.offsetLeft + chip.offsetWidth;
    if (chip.offsetLeft < scroller.scrollLeft) {
      scroller.scrollLeft = chip.offsetLeft;
    } else if (chipEnd > scroller.scrollLeft + scroller.clientWidth) {
      scroller.scrollLeft = chipEnd - scroller.clientWidth;
    }
  }, [activeId]);

  return (
    <nav
      ref={bar}
      aria-label="Jump to section"
      className="bg-background/80 sticky z-10 -mx-1 backdrop-blur"
      style={{ top: SECTION_NAV_TOP }}
    >
      <div ref={strip} className="relative flex gap-1.5 overflow-x-auto px-1 py-2 [scrollbar-width:thin]">
        {sections.map((section) => (
          <a
            key={section.id}
            href={`#${section.id}`}
            aria-current={section.id === activeId ? "location" : undefined}
            onClick={() => select(section.id)}
            className="text-muted-foreground hover:bg-accent hover:text-foreground aria-[current=location]:border-primary aria-[current=location]:bg-primary aria-[current=location]:text-primary-foreground inline-flex h-7 shrink-0 items-center rounded-full border px-3 text-xs font-medium whitespace-nowrap transition-colors"
          >
            {section.label}
          </a>
        ))}
      </div>
    </nav>
  );
}
