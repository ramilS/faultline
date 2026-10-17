import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { SymbolicatedException, SymbolicatedFrame } from "@/api";

import { StackTrace } from "./StackTrace";

function frame(fn: string, inApp: boolean, source: string | null = null): SymbolicatedFrame {
  return {
    generated: { function: fn, filename: `src/${fn}.ts`, line: 3, column: 1, in_app: inApp },
    original: null,
    context: source === null ? [] : [{ line: 3, text: source, focus: true }]
  };
}

const TRACE: SymbolicatedException = {
  type: "TypeError",
  value: "Cannot read properties of undefined (reading 'price')",
  cause: null,
  frames: [
    frame("dispatch", false, "vendorLine()"),
    frame("calcTotal", true, "total += sku.price"),
    frame("onSubmit", true, "calcTotal(cart)"),
    frame("scheduler", false),
    frame("runTask", false)
  ]
};

function sourceShown(): string[] {
  return [...document.querySelectorAll("pre code")].map((code) => code.textContent ?? "");
}

describe("StackTrace", () => {
  it("opens with only the innermost in-app frame's source showing", () => {
    render(<StackTrace exception={TRACE} />);

    expect(sourceShown()).toEqual(["3total += sku.price"]);
  });

  it("folds vendor frames into a row that opens them in place", () => {
    render(<StackTrace exception={TRACE} />);

    expect(screen.queryByText("scheduler")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Show 2 vendor frames" }));

    expect(screen.getByText("scheduler")).toBeDefined();
    expect(screen.getByText("runTask")).toBeDefined();
  });

  it("lists every frame unfolded in the full view", () => {
    render(<StackTrace exception={TRACE} />);

    fireEvent.click(screen.getByRole("button", { name: "Full" }));

    expect(screen.queryByRole("button", { name: /vendor frame/ })).toBeNull();
    expect(screen.getByText("dispatch")).toBeDefined();
    expect(screen.getByText("runTask")).toBeDefined();
  });

  it("shows the whole trace as plain text in the raw view", () => {
    const { container } = render(<StackTrace exception={TRACE} />);

    fireEvent.click(screen.getByRole("button", { name: "Raw" }));

    expect(container.querySelector("pre")?.textContent).toBe(
      [
        "TypeError: Cannot read properties of undefined (reading 'price')",
        "  at dispatch (src/dispatch.ts:3:1)",
        "  at calcTotal (src/calcTotal.ts:3:1)",
        "  at onSubmit (src/onSubmit.ts:3:1)",
        "  at scheduler (src/scheduler.ts:3:1)",
        "  at runTask (src/runTask.ts:3:1)"
      ].join("\n")
    );
  });
});
