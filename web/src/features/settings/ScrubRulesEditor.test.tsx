import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it } from "vitest";

import type { ScrubRule } from "@/api";

import { ScrubRulesEditor } from "./ScrubRulesEditor";

interface HarnessProps {
  initial: ScrubRule[];
  editable?: boolean;
  disabled?: boolean;
  onRules?: (rules: ScrubRule[]) => void;
}

function Harness({ initial, editable = true, disabled = false, onRules }: HarnessProps) {
  const [rules, setRules] = useState(initial);
  return (
    <ScrubRulesEditor
      rules={rules}
      editable={editable}
      disabled={disabled}
      onChange={(next) => {
        setRules(next);
        onRules?.(next);
      }}
    />
  );
}

describe("ScrubRulesEditor", () => {
  it("adds a preset rule", () => {
    let latest: ScrubRule[] = [];
    render(<Harness initial={[]} onRules={(rules) => (latest = rules)} />);

    fireEvent.click(screen.getByRole("button", { name: "Add Telegram bot token" }));

    expect(latest).toHaveLength(1);
    expect(latest[0]).toMatchObject({ name: "Telegram bot token", replacement: "bot[Filtered]" });
    expect(screen.getByLabelText("Rule 1 pattern")).toHaveProperty("value", latest[0]!.pattern);
  });

  it("removes a rule", () => {
    let latest: ScrubRule[] = [{ name: "a", pattern: "a", replacement: "" }];
    render(<Harness initial={latest} onRules={(rules) => (latest = rules)} />);

    fireEvent.click(screen.getByRole("button", { name: "Remove rule 1" }));

    expect(latest).toEqual([]);
  });

  it("previews the rules against sample text", () => {
    render(<Harness initial={[{ name: "n", pattern: "secret-\\d+", replacement: "" }]} />);

    fireEvent.change(screen.getByLabelText("Test against sample text"), { target: { value: "id secret-42 end" } });

    expect(screen.getByLabelText("Result").textContent).toBe("id [Filtered] end");
  });

  it("says the preview is only a browser approximation", () => {
    render(<Harness initial={[]} />);

    expect(screen.getByText("Preview runs in your browser; the server (RE2) decides on save.")).toBeTruthy();
  });

  it("is read-only for a member", () => {
    render(<Harness initial={[{ name: "n", pattern: "p", replacement: "" }]} editable={false} />);

    expect(screen.getByLabelText("Rule 1 pattern")).toHaveProperty("disabled", true);
    expect(screen.queryByRole("button", { name: "Add rule" })).toBeNull();
  });

  it("shows every unparsable pattern in one alert and still previews the other rules", () => {
    render(
      <Harness
        initial={[
          { name: "bad", pattern: "(", replacement: "" },
          { name: "good", pattern: "secret", replacement: "" }
        ]}
      />
    );

    fireEvent.change(screen.getByLabelText("Test against sample text"), { target: { value: "a secret" } });

    expect(screen.getByRole("alert").textContent).toContain('"bad": the browser cannot parse this pattern');
    expect(screen.getByLabelText("Result").textContent).toBe("a [Filtered]");
  });

  it("bounds the sample and the rule fields", () => {
    render(<Harness initial={[{ name: "n", pattern: "p", replacement: "" }]} />);

    expect(screen.getByLabelText("Test against sample text")).toHaveProperty("maxLength", 2000);
    expect(screen.getByLabelText("Rule 1 name")).toHaveProperty("maxLength", 64);
    expect(screen.getByLabelText("Rule 1 pattern")).toHaveProperty("maxLength", 512);
    expect(screen.getByLabelText("Rule 1 replacement")).toHaveProperty("maxLength", 128);
  });

  it("disables every control of a read-only editor but keeps the rules visible", () => {
    render(<Harness initial={[{ name: "n", pattern: "p", replacement: "" }]} editable={false} />);

    expect(screen.queryByRole("button", { name: /^Add / })).toBeNull();
    expect(screen.getByRole("button", { name: "Remove rule 1" })).toHaveProperty("disabled", true);
    expect(screen.getByLabelText("Rule 1 name")).toHaveProperty("disabled", true);
    expect(screen.getByLabelText("Rule 1 replacement")).toHaveProperty("disabled", true);
    expect(screen.getByLabelText("Test against sample text")).toHaveProperty("disabled", true);
  });

  it("disables the controls but keeps them visible while scrubbing is switched off", () => {
    render(<Harness initial={[{ name: "n", pattern: "p", replacement: "" }]} disabled />);

    expect(screen.getByRole("button", { name: "Add rule" })).toHaveProperty("disabled", true);
    expect(screen.getByRole("button", { name: "Add GitHub token" })).toHaveProperty("disabled", true);
    expect(screen.getByRole("button", { name: "Remove rule 1" })).toHaveProperty("disabled", true);
    expect(screen.getByLabelText("Rule 1 pattern")).toHaveProperty("disabled", true);
  });

  it("stops adding rules at the limit", () => {
    const rules = Array.from({ length: 25 }, (_, index) => ({ name: `r${index}`, pattern: "p", replacement: "" }));
    render(<Harness initial={rules} />);

    expect(screen.getByRole("button", { name: "Add rule" })).toHaveProperty("disabled", true);
    expect(screen.getByRole("button", { name: "Add Telegram bot token" })).toHaveProperty("disabled", true);
    expect(screen.getByRole("button", { name: "Add AWS access key ID" })).toHaveProperty("disabled", true);
    expect(screen.getByRole("button", { name: "Add GitHub token" })).toHaveProperty("disabled", true);
  });

  it.each([
    ["name", "Rule 1 name", { name: "renamed", pattern: "p", replacement: "r" }],
    ["pattern", "Rule 1 pattern", { name: "n", pattern: "renamed", replacement: "r" }],
    ["replacement", "Rule 1 replacement", { name: "n", pattern: "p", replacement: "renamed" }]
  ])("reports an edited %s through onChange", (_field, label, expected) => {
    let latest: ScrubRule[] = [];
    render(<Harness initial={[{ name: "n", pattern: "p", replacement: "r" }]} onRules={(rules) => (latest = rules)} />);

    fireEvent.change(screen.getByLabelText(label), { target: { value: "renamed" } });

    expect(latest).toEqual([expected]);
  });

  it("previews chained rules in list order", () => {
    render(
      <Harness
        initial={[
          { name: "a", pattern: "alpha", replacement: "beta" },
          { name: "b", pattern: "beta", replacement: "gamma" }
        ]}
      />
    );

    fireEvent.change(screen.getByLabelText("Test against sample text"), { target: { value: "alpha" } });

    expect(screen.getByLabelText("Result").textContent).toBe("gamma");
  });

  it("names each rule group for screen readers", () => {
    render(
      <Harness
        initial={[
          { name: "a", pattern: "p", replacement: "" },
          { name: "b", pattern: "q", replacement: "" }
        ]}
      />
    );

    expect(screen.getByRole("group", { name: "Rule 1" })).toBeTruthy();
    expect(screen.getByRole("group", { name: "Rule 2" })).toBeTruthy();
  });
});
