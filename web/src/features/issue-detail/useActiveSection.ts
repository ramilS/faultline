import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react";

import type { EventSection } from "./eventSections";
import { publishPinnedHeight, SECTION_NAV_HEIGHT_PROPERTY } from "./stickyHeader";

const SCROLL_INTENT_EVENTS = ["wheel", "touchstart", "keydown"] as const;

export interface ActiveSection {
  activeId: string | undefined;
  select: (id: string) => void;
}

export function useActiveSection(
  sections: readonly EventSection[],
  bar: RefObject<HTMLElement | null>
): ActiveSection {
  const [chosenId, setChosenId] = useState<string | undefined>(undefined);
  const selectedByClick = useRef<string | null>(null);
  const barBottom = usePinnedBarBottom(bar);

  useEffect(() => {
    const visible = new Set<string>();
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            visible.add(entry.target.id);
          } else {
            visible.delete(entry.target.id);
          }
        }
        const clicked = selectedByClick.current;
        if (clicked !== null && visible.has(clicked)) {
          return;
        }
        selectedByClick.current = null;
        const topmost = sections.find((section) => visible.has(section.id));
        if (topmost !== undefined) {
          setChosenId(topmost.id);
        }
      },
      { rootMargin: `-${barBottom}px 0px 0px 0px` }
    );
    for (const section of sections) {
      const target = document.getElementById(section.id);
      if (target !== null) {
        observer.observe(target);
      }
    }
    return () => observer.disconnect();
  }, [sections, barBottom]);

  useEffect(() => {
    const releaseClick = () => {
      selectedByClick.current = null;
    };
    for (const type of SCROLL_INTENT_EVENTS) {
      window.addEventListener(type, releaseClick, { passive: true });
    }
    return () => {
      for (const type of SCROLL_INTENT_EVENTS) {
        window.removeEventListener(type, releaseClick);
      }
    };
  }, []);

  const activeId = sections.some((section) => section.id === chosenId) ? chosenId : sections[0]?.id;

  return {
    activeId,
    select: (id) => {
      selectedByClick.current = id;
      setChosenId(id);
    }
  };
}

function usePinnedBarBottom(bar: RefObject<HTMLElement | null>): number {
  const [bottom, setBottom] = useState(0);

  useLayoutEffect(() => {
    const element = bar.current;
    if (element === null) {
      return;
    }
    const measure = () => {
      publishPinnedHeight(SECTION_NAV_HEIGHT_PROPERTY, element.offsetHeight);
      const top = Number.parseFloat(getComputedStyle(element).top);
      setBottom(Math.round((Number.isFinite(top) ? top : 0) + element.offsetHeight));
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    window.addEventListener("resize", measure);
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", measure);
      publishPinnedHeight(SECTION_NAV_HEIGHT_PROPERTY, null);
    };
  }, [bar]);

  return bottom;
}
