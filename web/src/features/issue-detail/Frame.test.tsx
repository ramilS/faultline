import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { SymbolicatedFrame } from "@/api";

import { Frame } from "./Frame";

function frameIn(filename: string, contextLines: string[]): SymbolicatedFrame {
  const position = { filename, line: 10, column: 1, function: "run", in_app: true };
  return {
    generated: position,
    original: null,
    context: contextLines.map((text, index) => ({ line: 9 + index, text, focus: index === 1 })),
    vars: null
  } as unknown as SymbolicatedFrame;
}

describe("Frame collapsing", () => {
  it("shows only the summary line while collapsed, and opens onto the source when clicked", () => {
    let expanded = false;
    const frame = frameIn("src/charge.ts", ["const a = 1;", "  await run(a)"]);
    const { container, rerender } = render(
      <ul>
        <Frame sourceStackRoot="" repositoryUrl={null} frame={frame} expanded={expanded} onToggle={() => (expanded = !expanded)} />
      </ul>
    );

    expect(container.querySelector("pre")).toBeNull();
    const toggle = screen.getByRole("button", { expanded: false });
    fireEvent.click(toggle);
    expect(expanded).toBe(true);

    rerender(
      <ul>
        <Frame sourceStackRoot="" repositoryUrl={null} frame={frame} expanded={expanded} onToggle={() => (expanded = !expanded)} />
      </ul>
    );
    expect(container.querySelector("pre")?.textContent).toContain("await run(a)");
  });

  it("offers nothing to open on a frame that carries no source", () => {
    render(
      <ul>
        <Frame sourceStackRoot="" repositoryUrl={null} frame={frameIn("src/charge.ts", [])} expanded={false} onToggle={() => {}} />
      </ul>
    );

    expect(screen.queryByRole("button")).toBeNull();
  });
});

describe("Frame source context", () => {
  it("sets keywords, strings and comments apart by weight and tone in a JavaScript file", () => {
    render(<ul><Frame sourceStackRoot="" repositoryUrl={null} expanded onToggle={() => {}} frame={frameIn("src/charge.ts", ['return "done"; // finished'])} /></ul>);

    expect(screen.getByText("return").className).toContain("font-semibold");
    expect(screen.getByText("// finished").className).toContain("italic");
  });

  it("shows every line's text exactly as sent, whatever the highlighting", () => {
    const { container } = render(
      <ul>
        <Frame sourceStackRoot="" repositoryUrl={null} expanded onToggle={() => {}} frame={frameIn("src/charge.ts", ["const a = 1;", "  await run(a)"])} />
      </ul>
    );

    const lines = [...container.querySelectorAll("pre code > span")].map((line) => line.textContent);
    expect(lines).toEqual(["9const a = 1;", "10  await run(a)"]);
  });

  it("leaves a Java frame plain rather than colouring it by JavaScript's rules", () => {
    render(<ul><Frame sourceStackRoot="" repositoryUrl={null} expanded onToggle={() => {}} frame={frameIn("Exporter.java", ["return value; // finished"])} /></ul>);

    expect(screen.getByText("return value; // finished").className).not.toContain("italic");
  });

  it("keeps markup in source as text", () => {
    const { container } = render(
      <ul>
        <Frame sourceStackRoot="" repositoryUrl={null} expanded onToggle={() => {}} frame={frameIn("src/x.ts", ['const x = "<img src=x onerror=alert(1)>";'])} />
      </ul>
    );

    expect(container.querySelector("img")).toBeNull();
  });
});

function frameWithVars(vars: Record<string, unknown>): SymbolicatedFrame {
  return {
    generated: { filename: "/app/shop/cart.py", line: 88, column: 0, function: "load", in_app: true },
    original: null,
    context: [],
    vars
  };
}

function variableRow(name: string): HTMLElement {
  const row = screen.getByText(name).closest("tr");
  if (row === null) {
    throw new Error(`no variable row for ${name}`);
  }
  return row;
}

describe("Frame local variables", () => {
  it("opens a frame that carries variables but no source, and lists each variable by name", () => {
    let expanded = false;
    const frame = frameWithVars({ user_id: 42, password: "[Filtered]", items: [1, 2, 3] });
    const { rerender } = render(
      <ul>
        <Frame frame={frame} expanded={expanded} onToggle={() => (expanded = !expanded)} sourceStackRoot="" repositoryUrl={null} />
      </ul>
    );
    expect(screen.queryByText("user_id")).toBeNull();

    fireEvent.click(screen.getByRole("button", { expanded: false }));
    rerender(
      <ul>
        <Frame frame={frame} expanded={expanded} onToggle={() => (expanded = !expanded)} sourceStackRoot="" repositoryUrl={null} />
      </ul>
    );

    expect(screen.getByRole("table", { name: "Local variables" })).toBeDefined();
    expect(variableRow("user_id").textContent).toContain("42");
    expect(variableRow("password").textContent).toContain("[Filtered]");
    expect(variableRow("items").textContent).toContain("[…] 3 items");
  });

  it("keeps a nested value collapsed until it is opened", () => {
    render(
      <ul>
        <Frame frame={frameWithVars({ cart: { sku: "A-1", qty: 2 } })} expanded onToggle={() => {}} sourceStackRoot="" repositoryUrl={null} />
      </ul>
    );
    expect(screen.queryByText("sku")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: /\{…\} 2 keys/ }));

    expect(variableRow("sku").textContent).toContain("A-1");
    expect(variableRow("qty").textContent).toContain("2");
  });

  it("cuts a huge value and says it was truncated", () => {
    const { container } = render(
      <ul>
        <Frame frame={frameWithVars({ blob: "x".repeat(1 << 20) })} expanded onToggle={() => {}} sourceStackRoot="" repositoryUrl={null} />
      </ul>
    );

    expect(screen.getByText(/truncated/).textContent).toContain("1047576 more characters");
    expect((container.textContent ?? "").length).toBeLessThan(5000);
  });

  it("shows markup in a value as text", () => {
    const { container } = render(
      <ul>
        <Frame frame={frameWithVars({ name: "<img src=x onerror=alert(1)>" })} expanded onToggle={() => {}} sourceStackRoot="" repositoryUrl={null} />
      </ul>
    );

    expect(container.querySelector("img")).toBeNull();
    expect(variableRow("name").textContent).toContain("<img src=x onerror=alert(1)>");
  });

  it("shows no variables section for a frame that sent none", () => {
    render(
      <ul>
        <Frame expanded onToggle={() => {}} sourceStackRoot="" repositoryUrl={null} frame={frameIn("src/charge.ts", ["run()"])} />
      </ul>
    );

    expect(screen.queryByRole("table", { name: "Local variables" })).toBeNull();
  });
});

describe("Frame repository link", () => {
  it("opens the repository in a new tab without handing it the dashboard", () => {
    render(
      <ul>
        <Frame
          frame={frameIn("src/shop/cart.py", [])}
          expanded={false}
          onToggle={() => {}}
          sourceStackRoot=""
          repositoryUrl="https://github.com/acme/shop/blob/main/src/shop/cart.py#L88"
        />
      </ul>
    );

    const link = screen.getByRole("link", { name: "Open in repository" });
    expect(link.getAttribute("href")).toBe("https://github.com/acme/shop/blob/main/src/shop/cart.py#L88");
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")).toBe("noopener noreferrer");
  });

  it("renders no link or empty button when there is no repository URL", () => {
    render(
      <ul>
        <Frame frame={frameIn("src/charge.ts", [])} expanded={false} onToggle={() => {}} sourceStackRoot="" repositoryUrl={null} />
      </ul>
    );

    expect(screen.queryByRole("link")).toBeNull();
    expect(screen.queryByRole("button")).toBeNull();
  });
});

describe("Frame location", () => {
  const path = "/Users/ramil/Documents/my/muslicast/research/playback/PlaybackController.kt";

  function renderAt(sourceStackRoot: string) {
    const frame = { ...frameIn(path, []), generated: { filename: path, line: 1029, column: 21, function: "play", in_app: true } };
    return render(
      <ul>
        <Frame frame={frame as SymbolicatedFrame} expanded={false} onToggle={() => {}} sourceStackRoot={sourceStackRoot} repositoryUrl={null} />
      </ul>
    );
  }

  it("leads with the file and line and keeps the whole location in the tooltip", () => {
    renderAt("");

    const file = screen.getByText("PlaybackController.kt:1029");
    expect(file.className).toContain("whitespace-nowrap");
    expect(file.parentElement?.getAttribute("title")).toBe(`${path}:1029:21`);
    expect(file.parentElement?.textContent).toBe(
      "PlaybackController.kt:1029/Users/ramil/Documents/my/muslicast/research/playback/"
    );
  });

  it("shows the directory relative to the stack root", () => {
    renderAt("/Users/ramil/Documents/my/muslicast/");

    expect(screen.getByText("PlaybackController.kt:1029").parentElement?.textContent).toBe(
      "PlaybackController.kt:1029research/playback/"
    );
  });

  it("shows no location for a frame without a file", () => {
    const { container } = render(
      <ul>
        <Frame frame={frameIn("", [])} expanded={false} onToggle={() => {}} sourceStackRoot="" repositoryUrl={null} />
      </ul>
    );

    expect(container.querySelector("[title]")).toBeNull();
    expect(container.textContent).toBe("run");
  });
});
