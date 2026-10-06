import { createRef } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";

/** Radix parks a popper here until Floating UI has measured its anchor. */
const UNPOSITIONED_TRANSFORM = "translate(0, -200%)";

function popperWrapper(): HTMLElement {
  const wrapper = document.querySelector<HTMLElement>("[data-radix-popper-content-wrapper]");
  if (wrapper === null) {
    throw new Error("popper content wrapper is not in the document");
  }
  return wrapper;
}

describe("Button", () => {
  it("forwards its ref to the rendered button element", () => {
    const ref = createRef<HTMLButtonElement>();

    render(<Button ref={ref}>Save</Button>);

    expect(ref.current).toBeInstanceOf(HTMLButtonElement);
    expect(ref.current?.textContent).toBe("Save");
  });

  it("forwards its ref to the slotted element under asChild", () => {
    const ref = createRef<HTMLButtonElement>();

    render(
      <Button asChild ref={ref}>
        <a href="/issues">Issues</a>
      </Button>
    );

    expect(ref.current).toBeInstanceOf(HTMLAnchorElement);
  });

  /**
   * The regression this file exists for. It surfaced on React 18, which dropped
   * a ref given to a plain function component: a `Button` without `forwardRef`
   * never reached `PopperAnchor`, and an unanchored popper is never positioned,
   * so Radix leaves it 200% above the viewport. Every dropdown and popover in
   * the dashboard opened into that dead zone — the theme menu and the project
   * switcher read as dead clicks.
   *
   * React 19 carries `ref` as an ordinary prop, so the way back into that dead
   * zone is to stop spreading `...props`. The failure is just as silent, which
   * is why this assertion is worth more than the two above it.
   *
   * Asserting on the transform rather than on the ref is what ties the contract
   * to its consequence — a `Button` that spread its props onto some other
   * element would satisfy the tests above and still fail this one.
   */
  it("anchors a Radix popover so its content is positioned", async () => {
    render(
      <Popover defaultOpen>
        <PopoverTrigger asChild>
          <Button variant="outline">Switch project</Button>
        </PopoverTrigger>
        <PopoverContent>checkout-web</PopoverContent>
      </Popover>
    );

    expect(screen.getByText("checkout-web")).toBeDefined();
    await waitFor(() => {
      expect(popperWrapper().style.transform).not.toBe(UNPOSITIONED_TRANSFORM);
    });
  });
});
