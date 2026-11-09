import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { EventSection } from "./eventSections";
import { EventSectionNav } from "./EventSectionNav";

const SECTIONS: readonly EventSection[] = [
  { id: "section-stack-trace", label: "Stack trace" },
  { id: "section-breadcrumbs", label: "Breadcrumbs" },
  { id: "section-tags", label: "Tags" },
  { id: "section-raw-payload", label: "Raw payload" }
];

class ControlledIntersectionObserver {
  static current: ControlledIntersectionObserver | null = null;
  readonly targets: Element[] = [];

  constructor(private readonly callback: IntersectionObserverCallback) {
    ControlledIntersectionObserver.current = this;
  }

  observe(target: Element): void {
    this.targets.push(target);
  }

  unobserve(): void {}

  disconnect(): void {}

  takeRecords(): [] {
    return [];
  }

  report(changes: Record<string, boolean>): void {
    const entries = Object.entries(changes).map(
      ([id, isIntersecting]) =>
        ({ target: document.getElementById(id), isIntersecting }) as unknown as IntersectionObserverEntry
    );
    act(() => this.callback(entries, this as unknown as IntersectionObserver));
  }
}

function observer(): ControlledIntersectionObserver {
  const current = ControlledIntersectionObserver.current;
  if (current === null) {
    throw new Error("no IntersectionObserver was created");
  }
  return current;
}

function renderPage(sections: readonly EventSection[] = SECTIONS) {
  return render(
    <>
      <EventSectionNav sections={sections} />
      {sections.map((section) => (
        <section key={section.id} id={section.id} />
      ))}
    </>
  );
}

function currentLink(): HTMLElement | null {
  return document.querySelector('[aria-current="location"]');
}

describe("EventSectionNav", () => {
  beforeEach(() => {
    ControlledIntersectionObserver.current = null;
    vi.stubGlobal("IntersectionObserver", ControlledIntersectionObserver);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("renders a link per supplied section, each pointing at its anchor", () => {
    renderPage();

    expect(screen.getByRole("link", { name: "Stack trace" })).toHaveProperty("hash", "#section-stack-trace");
    expect(screen.getByRole("link", { name: "Tags" })).toHaveProperty("hash", "#section-tags");
    expect(screen.getByRole("link", { name: "Raw payload" })).toHaveProperty("hash", "#section-raw-payload");
  });

  it("is announced as navigation to assistive tech", () => {
    renderPage();

    expect(screen.getByRole("navigation", { name: "Jump to section" })).toBeDefined();
  });

  it("observes every section on the page", () => {
    renderPage();

    expect(observer().targets.map((target) => target.id)).toEqual(SECTIONS.map((section) => section.id));
  });

  it("marks the first section as current before anything has scrolled", () => {
    renderPage();

    expect(currentLink()?.textContent).toBe("Stack trace");
  });

  it("moves the current marker to the topmost section in view as the page scrolls", () => {
    renderPage();

    observer().report({ "section-stack-trace": true, "section-breadcrumbs": true });
    expect(currentLink()?.textContent).toBe("Stack trace");

    observer().report({ "section-stack-trace": false, "section-tags": true });
    expect(currentLink()?.textContent).toBe("Breadcrumbs");

    observer().report({ "section-breadcrumbs": false });
    expect(currentLink()?.textContent).toBe("Tags");

    observer().report({ "section-stack-trace": true });
    expect(currentLink()?.textContent).toBe("Stack trace");
  });

  it("keeps the last current section while nothing is in view", () => {
    renderPage();

    observer().report({ "section-tags": true });
    observer().report({ "section-tags": false });

    expect(currentLink()?.textContent).toBe("Tags");
  });

  it("marks a clicked section current even when a section above it is still in view", () => {
    renderPage();
    observer().report({ "section-tags": true, "section-raw-payload": true });

    fireEvent.click(screen.getByRole("link", { name: "Raw payload" }));
    observer().report({ "section-tags": true });

    expect(currentLink()?.textContent).toBe("Raw payload");
  });

  it("follows the scroll again once the user scrolls after a click", () => {
    renderPage();
    observer().report({ "section-tags": true, "section-raw-payload": true });
    fireEvent.click(screen.getByRole("link", { name: "Raw payload" }));

    fireEvent.wheel(window);
    observer().report({ "section-tags": true });

    expect(currentLink()?.textContent).toBe("Tags");
  });

  it("falls back to the first entry when the current section is gone from the new list", () => {
    const { rerender } = renderPage();
    observer().report({ "section-stack-trace": false, "section-tags": true });

    const next = [SECTIONS[0], SECTIONS[1], SECTIONS[3]];
    rerender(
      <>
        <EventSectionNav sections={next} />
        {next.map((section) => (
          <section key={section.id} id={section.id} />
        ))}
      </>
    );

    expect(currentLink()?.textContent).toBe("Stack trace");
  });

  it("publishes the bar's laid-out height for the sections' scroll margin", () => {
    vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(44);

    const { unmount } = renderPage();
    expect(document.documentElement.style.getPropertyValue("--issue-section-nav-height")).toBe("44px");

    unmount();
    expect(document.documentElement.style.getPropertyValue("--issue-section-nav-height")).toBe("");
  });

  it("publishes no height for an unlaid-out bar, so the scroll margin keeps its fallback", () => {
    renderPage();

    expect(document.documentElement.style.getPropertyValue("--issue-section-nav-height")).toBe("");
  });
});
