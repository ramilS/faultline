import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { SymbolicatedFrame } from "@/api";

import { Frame } from "./Frame";

function frameIn(filename: string, contextLines: string[]): SymbolicatedFrame {
  const position = { filename, line: 10, column: 1, function: "run", in_app: true };
  return {
    generated: position,
    original: null,
    context: contextLines.map((text, index) => ({ line: 9 + index, text, focus: index === 1 }))
  } as unknown as SymbolicatedFrame;
}

describe("Frame source context", () => {
  it("sets keywords, strings and comments apart by weight and tone in a JavaScript file", () => {
    render(<ul><Frame frame={frameIn("src/charge.ts", ['return "done"; // finished'])} /></ul>);

    expect(screen.getByText("return").className).toContain("font-semibold");
    expect(screen.getByText("// finished").className).toContain("italic");
  });

  it("shows every line's text exactly as sent, whatever the highlighting", () => {
    const { container } = render(
      <ul>
        <Frame frame={frameIn("src/charge.ts", ["const a = 1;", "  await run(a)"])} />
      </ul>
    );

    const lines = [...container.querySelectorAll("pre code > span")].map((line) => line.textContent);
    expect(lines).toEqual(["9const a = 1;", "10  await run(a)"]);
  });

  it("leaves a Java frame plain rather than colouring it by JavaScript's rules", () => {
    render(<ul><Frame frame={frameIn("Exporter.java", ["return value; // finished"])} /></ul>);

    expect(screen.getByText("return value; // finished").className).not.toContain("italic");
  });

  it("keeps markup in source as text", () => {
    const { container } = render(
      <ul>
        <Frame frame={frameIn("src/x.ts", ['const x = "<img src=x onerror=alert(1)>";'])} />
      </ul>
    );

    expect(container.querySelector("img")).toBeNull();
  });
});
