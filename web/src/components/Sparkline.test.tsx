import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Sparkline } from "./Sparkline";

const HOURS_IN_14_DAYS = 14 * 24;

function barsOf(container: HTMLElement): SVGRectElement[] {
  // The first rect is the floor drawn under the bars, not a bucket.
  return [...container.querySelectorAll("rect")].slice(1) as SVGRectElement[];
}

describe("Sparkline", () => {
  it("draws a lone event in a long window wide enough to see", () => {
    const counts = Array.from({ length: HOURS_IN_14_DAYS }, (_, index) => (index === 100 ? 1 : 0));

    const { container } = render(<Sparkline counts={counts} window="14 days" />);

    const [bar] = barsOf(container).filter((rect) => Number(rect.getAttribute("height")) > 0);
    expect(Number(bar.getAttribute("width"))).toBeGreaterThanOrEqual(1.5);
  });

  it("keeps a bar in the last bucket inside the drawing", () => {
    const counts = Array.from({ length: HOURS_IN_14_DAYS }, (_, index) => (index === HOURS_IN_14_DAYS - 1 ? 1 : 0));

    const { container } = render(<Sparkline counts={counts} window="14 days" />);

    const [bar] = barsOf(container).filter((rect) => Number(rect.getAttribute("height")) > 0);
    expect(Number(bar.getAttribute("x")) + Number(bar.getAttribute("width"))).toBeLessThanOrEqual(100);
  });

  it("leaves a gap between neighbouring bars when the window is short enough to have room", () => {
    const { container } = render(<Sparkline counts={[3, 3, 3, 3]} window="24 hours" />);

    const [first, second] = barsOf(container);
    const firstEnd = Number(first.getAttribute("x")) + Number(first.getAttribute("width"));
    expect(firstEnd).toBeLessThan(Number(second.getAttribute("x")));
  });

  it("shows a dash rather than an empty chart when there are no buckets", () => {
    const { container } = render(<Sparkline counts={[]} window="24 hours" />);

    expect(container.querySelector("svg")).toBeNull();
    expect(container.textContent).toBe("—");
  });
});
