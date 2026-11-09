import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";

import { ApiError, type ScrubPreview, type ScrubRule } from "@/api";

import type { RunScrubPreview } from "./ScrubPreviewPanel";
import { ScrubRulesEditor } from "./ScrubRulesEditor";
import { DEFAULT_SAMPLE, SCRUB_PRESETS } from "./scrubRules";

interface HarnessProps {
  initial: ScrubRule[];
  editable?: boolean;
  disabled?: boolean;
  onRules?: (rules: ScrubRule[]) => void;
  runPreview?: RunScrubPreview;
  onAddField?: (name: string) => void;
  hasEvents?: boolean;
}

const filtered: ScrubPreview = { scrubbed: "id [Filtered]", segments: [{ text: "id ", redacted: false }, { text: "[Filtered]", redacted: true }] };

function Harness({ initial, editable = true, disabled = false, onRules, runPreview = vi.fn(async () => filtered), onAddField, hasEvents }: HarnessProps) {
  const [rules, setRules] = useState(initial);
  return (
    <ScrubRulesEditor
      rules={rules}
      editable={editable}
      disabled={disabled}
      runPreview={runPreview}
      onAddField={onAddField}
      hasEvents={hasEvents}
      onChange={(next) => {
        setRules(next);
        onRules?.(next);
      }}
    />
  );
}

function choose(kind: RegExp) {
  fireEvent.click(screen.getByRole("button", { name: "Add rule" }));
  fireEvent.click(screen.getByRole("button", { name: kind }));
}

const slack = SCRUB_PRESETS.find((preset) => preset.name === "Slack token")!;

describe("ScrubRulesEditor", () => {
  it("says no custom rules exist and that the defaults still apply", () => {
    render(<Harness initial={[]} />);

    expect(screen.getByText("No custom rules. The defaults above still apply.")).toBeTruthy();
  });

  it("asks what to hide before offering any path", () => {
    render(<Harness initial={[]} onAddField={vi.fn()} />);

    fireEvent.click(screen.getByRole("button", { name: "Add rule" }));

    expect(screen.getByRole("heading", { name: "What do you want to hide?" })).toBeTruthy();
    expect(screen.getByRole("button", { name: /^A field/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: /^A known kind of value/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: /^A custom pattern/ })).toBeTruthy();
  });

  it("hides a field by name without asking for a pattern", () => {
    const onAddField = vi.fn();
    let latest: ScrubRule[] = [];
    render(<Harness initial={[]} onAddField={onAddField} onRules={(rules) => (latest = rules)} />);

    choose(/^A field/);
    fireEvent.change(screen.getByLabelText("Field name"), { target: { value: " phone " } });
    fireEvent.click(screen.getByRole("button", { name: "Hide this field" }));

    expect(onAddField).toHaveBeenCalledWith("phone");
    expect(latest).toEqual([]);
    expect(screen.queryByRole("heading", { name: "What do you want to hide?" })).toBeNull();
  });

  it("refuses a blank field name", () => {
    const onAddField = vi.fn();
    render(<Harness initial={[]} onAddField={onAddField} />);

    choose(/^A field/);
    fireEvent.click(screen.getByRole("button", { name: "Hide this field" }));

    expect(screen.getByRole("alert").textContent).toBe("Enter a field name.");
    expect(onAddField).not.toHaveBeenCalled();
  });

  it("offers no field path where the page has no field lists", () => {
    render(<Harness initial={[]} />);

    fireEvent.click(screen.getByRole("button", { name: "Add rule" }));

    expect(screen.queryByRole("button", { name: /^A field/ })).toBeNull();
  });

  it("adds a preset with its name, pattern and description", () => {
    let latest: ScrubRule[] = [];
    render(<Harness initial={[]} onRules={(rules) => (latest = rules)} />);

    choose(/^A known kind of value/);
    fireEvent.click(screen.getByRole("button", { name: "Add Slack token" }));

    expect(latest).toEqual([{ name: "Slack token", description: slack.description, pattern: slack.pattern, replacement: "" }]);
    expect(screen.getByText(slack.description)).toBeTruthy();
  });

  it("shows every preset with its description and example", () => {
    render(<Harness initial={[]} />);

    choose(/^A known kind of value/);

    const gallery = screen.getByRole("list", { name: "Known kinds of value" });
    for (const preset of SCRUB_PRESETS) {
      expect(gallery.textContent).toContain(preset.description);
      expect(gallery.textContent).toContain(preset.example);
    }
  });

  it("shows a preset already in the list as added", () => {
    render(<Harness initial={[{ name: "Renamed", pattern: slack.pattern, replacement: "" }]} />);

    choose(/^A known kind of value/);

    expect(screen.getByRole("button", { name: "Slack token added" })).toHaveProperty("disabled", true);
    expect(screen.queryByRole("button", { name: "Add Slack token" })).toBeNull();
    expect(screen.getByRole("button", { name: "Add JWT" })).toHaveProperty("disabled", false);
  });

  it("puts the RE2 error from the server under the custom pattern field and adds nothing", async () => {
    const runPreview = vi.fn<RunScrubPreview>(async () => {
      throw new ApiError(400, "invalid data scrubbing configuration: rule 1: pattern is not valid RE2: invalid or unsupported Perl syntax: `(?=`");
    });
    let latest: ScrubRule[] = [];
    render(<Harness initial={[]} runPreview={runPreview} onRules={(rules) => (latest = rules)} />);

    choose(/^A custom pattern/);
    fireEvent.change(screen.getByLabelText("Rule name"), { target: { value: "x" } });
    fireEvent.change(screen.getByLabelText("Pattern"), { target: { value: "a(?=b)" } });
    fireEvent.click(screen.getByRole("button", { name: "Add this rule" }));

    const pattern = screen.getByLabelText("Pattern");
    await waitFor(() => expect(pattern.getAttribute("aria-invalid")).toBe("true"));
    const error = document.getElementById(pattern.getAttribute("aria-describedby")!);
    expect(error?.textContent).toContain("pattern is not valid RE2");
    expect(runPreview).toHaveBeenCalledWith([{ name: "x", pattern: "a(?=b)", replacement: "" }], { sample: "" });
    expect(latest).toEqual([]);
  });

  it("adds a custom rule once the server accepts it", async () => {
    let latest: ScrubRule[] = [];
    render(<Harness initial={[]} onRules={(rules) => (latest = rules)} />);

    choose(/^A custom pattern/);
    fireEvent.change(screen.getByLabelText("Rule name"), { target: { value: "account" } });
    fireEvent.change(screen.getByLabelText("Pattern"), { target: { value: "acct-\\d+" } });
    fireEvent.click(screen.getByRole("button", { name: "Add this rule" }));

    await waitFor(() => expect(latest).toEqual([{ name: "account", pattern: "acct-\\d+", replacement: "" }]));
  });

  it("keeps the pattern syntax help collapsed until asked", () => {
    render(<Harness initial={[]} />);

    choose(/^A custom pattern/);
    expect(screen.queryByText(/acct-1042 becomes/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Pattern syntax" }));

    expect(screen.getByText(/acct-1042 becomes/)).toBeTruthy();
    expect(screen.getByText(/order-77 becomes/)).toBeTruthy();
    expect(screen.getByText(/SESSION=ab12/)).toBeTruthy();
  });

  it("starts the test box with the sample and highlights what the server redacted", async () => {
    const runPreview = vi.fn<RunScrubPreview>(async () => filtered);
    const rules = [{ name: "n", pattern: "secret-\\d+", replacement: "" }];
    render(<Harness initial={rules} runPreview={runPreview} />);

    expect(screen.getByLabelText("Test sample")).toHaveProperty("value", DEFAULT_SAMPLE);
    fireEvent.click(screen.getByRole("button", { name: "Preview sample" }));

    const result = await screen.findByLabelText("Result");
    expect(result.textContent).toBe("id [Filtered]");
    expect(result.querySelector("mark")?.textContent).toBe("[Filtered]");
    expect(runPreview).toHaveBeenCalledWith(rules, { sample: DEFAULT_SAMPLE });
  });

  it("previews the latest event when the project has one", async () => {
    const runPreview = vi.fn<RunScrubPreview>(async () => filtered);
    render(<Harness initial={[]} runPreview={runPreview} hasEvents />);

    fireEvent.click(screen.getByRole("button", { name: "Use latest event" }));

    await screen.findByLabelText("Result");
    expect(runPreview).toHaveBeenCalledWith([], { latest_event: true });
  });

  it("disables the latest event when none is stored", () => {
    render(<Harness initial={[]} hasEvents={false} />);

    expect(screen.getByRole("button", { name: "Use latest event" })).toHaveProperty("disabled", true);
    expect(screen.getByText("No events yet")).toBeTruthy();
  });

  it("shows a failed preview as an alert", async () => {
    const runPreview = vi.fn<RunScrubPreview>(async () => {
      throw new ApiError(500, "internal server error");
    });
    render(<Harness initial={[]} runPreview={runPreview} />);

    fireEvent.click(screen.getByRole("button", { name: "Preview sample" }));

    expect((await screen.findByRole("alert")).textContent).toBe("internal server error");
  });

  it("removes a rule", () => {
    let latest: ScrubRule[] = [{ name: "a", pattern: "a", replacement: "" }];
    render(<Harness initial={latest} onRules={(rules) => (latest = rules)} />);

    fireEvent.click(screen.getByRole("button", { name: "Remove rule 1" }));

    expect(latest).toEqual([]);
  });

  it("is read-only for a member: no add dialog and no preview", () => {
    render(<Harness initial={[{ name: "n", pattern: "p", replacement: "" }]} editable={false} />);

    expect(screen.getByLabelText("Rule 1 pattern")).toHaveProperty("disabled", true);
    expect(screen.getByRole("button", { name: "Remove rule 1" })).toHaveProperty("disabled", true);
    expect(screen.queryByRole("button", { name: "Add rule" })).toBeNull();
    expect(screen.queryByLabelText("Test sample")).toBeNull();
  });

  it("disables adding while scrubbing is switched off", () => {
    render(<Harness initial={[{ name: "n", pattern: "p", replacement: "" }]} disabled />);

    expect(screen.getByRole("button", { name: "Add rule" })).toHaveProperty("disabled", true);
    expect(screen.getByLabelText("Rule 1 pattern")).toHaveProperty("disabled", true);
  });

  it("stops adding rules at the limit and says why", () => {
    const rules = Array.from({ length: 25 }, (_, index) => ({ name: `r${index}`, pattern: "p", replacement: "" }));
    render(<Harness initial={rules} />);

    expect(screen.getByRole("button", { name: "Add rule" })).toHaveProperty("disabled", true);
    expect(screen.getByText("The limit of 25 rules is reached. Remove a rule to add another.")).toBeTruthy();
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

  it("bounds the rule fields", () => {
    render(<Harness initial={[{ name: "n", pattern: "p", replacement: "" }]} />);

    expect(screen.getByLabelText("Rule 1 name")).toHaveProperty("maxLength", 64);
    expect(screen.getByLabelText("Rule 1 pattern")).toHaveProperty("maxLength", 512);
    expect(screen.getByLabelText("Rule 1 replacement")).toHaveProperty("maxLength", 128);
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
