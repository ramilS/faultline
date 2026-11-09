import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { SymbolicatedException, SymbolicatedFrame } from "@/api";

import { StackTrace } from "./StackTrace";

function frame(fn: string, inApp: boolean, source: string | null = null): SymbolicatedFrame {
  return {
    generated: { function: fn, filename: `src/${fn}.ts`, line: 3, column: 1, in_app: inApp },
    original: null,
    context: source === null ? [] : [{ line: 3, text: source, focus: true }],
    vars: null
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
    render(<StackTrace exception={TRACE} release={null} sourceLinks={null} />);

    expect(sourceShown()).toEqual(["3total += sku.price"]);
  });

  it("folds vendor frames into a row that opens them in place", () => {
    render(<StackTrace exception={TRACE} release={null} sourceLinks={null} />);

    expect(screen.queryByText("scheduler")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Show 2 vendor frames" }));

    expect(screen.getByText("scheduler")).toBeDefined();
    expect(screen.getByText("runTask")).toBeDefined();
  });

  it("lists every frame unfolded in the full view", () => {
    render(<StackTrace exception={TRACE} release={null} sourceLinks={null} />);

    fireEvent.click(screen.getByRole("button", { name: "Full" }));

    expect(screen.queryByRole("button", { name: /vendor frame/ })).toBeNull();
    expect(screen.getByText("dispatch")).toBeDefined();
    expect(screen.getByText("runTask")).toBeDefined();
  });

  it("shows the whole trace as plain text in the raw view", () => {
    const { container } = render(<StackTrace exception={TRACE} release={null} sourceLinks={null} />);

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

  it("labels unnamed native frames instead of rendering blank rows", () => {
    const unnamedFrame: SymbolicatedFrame = {
      generated: { function: "", filename: "", line: 0, column: 0, in_app: true },
      original: null,
      context: [],
      vars: null
    };
    const exception: SymbolicatedException = {
      type: "kotlin.IllegalArgumentException",
      value: "Key was already used",
      cause: null,
      frames: [unnamedFrame, unnamedFrame]
    };
    const { container } = render(<StackTrace exception={exception} release={null} sourceLinks={null} />);

    expect([...container.querySelectorAll("ul > li")].map((row) => row.textContent)).toEqual([
      "<unknown>",
      "<unknown>"
    ]);

    fireEvent.click(screen.getByRole("button", { name: "Raw" }));
    expect(container.querySelector("pre")?.textContent).toBe(
      "kotlin.IllegalArgumentException: Key was already used\n  at <unknown>\n  at <unknown>"
    );
  });

  it("links in-app frames to the repository at the event's release and leaves vendor frames unlinked", () => {
    render(
      <StackTrace
        exception={TRACE}
        release="shop@1.2"
        sourceLinks={{ source_link_template: "https://git.example/shop/-/blob/{release}/{path}#L{line}", source_stack_root: "", source_root: "" }}
      />
    );

    expect(screen.getAllByRole("link", { name: "Open in repository" }).map((link) => link.getAttribute("href"))).toEqual([
      "https://git.example/shop/-/blob/shop%401.2/src/calcTotal.ts#L3",
      "https://git.example/shop/-/blob/shop%401.2/src/onSubmit.ts#L3"
    ]);
  });

  it("shows no repository link for an event without a release when the template needs one", () => {
    render(
      <StackTrace
        exception={TRACE}
        release={null}
        sourceLinks={{ source_link_template: "https://git.example/shop/-/blob/{release}/{path}", source_stack_root: "", source_root: "" }}
      />
    );

    expect(screen.queryByRole("link")).toBeNull();
  });
});
