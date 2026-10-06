import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ALL_TIME_WINDOW } from "@/lib/issueFilters";

import { EventFilterBar } from "./EventFilterBar";

describe("EventFilterBar", () => {
  it("lists the issue's environments plus an all-environments choice, and reports a change", () => {
    const onEnvironmentChange = vi.fn();

    render(
      <EventFilterBar
        environments={["production", "staging"]}
        environment=""
        onEnvironmentChange={onEnvironmentChange}
        timeRange={ALL_TIME_WINDOW}
        onTimeRangeChange={() => {}}
      />
    );

    const select = screen.getByRole("combobox", { name: "Environment" });
    const options = within(select).getAllByRole("option");
    expect(options.map((option) => option.textContent)).toEqual(["All environments", "production", "staging"]);

    fireEvent.change(select, { target: { value: "staging" } });

    expect(onEnvironmentChange).toHaveBeenCalledWith("staging");
  });

  it("omits the environment select for a single-environment issue but keeps the time range control", () => {
    render(
      <EventFilterBar
        environments={["production"]}
        environment=""
        onEnvironmentChange={() => {}}
        timeRange={ALL_TIME_WINDOW}
        onTimeRangeChange={() => {}}
      />
    );

    expect(screen.queryByRole("combobox", { name: "Environment" })).toBeNull();
    expect(screen.getByRole("button", { name: /Time range:/ })).toBeDefined();
  });

  it("omits the environment select for an issue with no environments recorded", () => {
    render(
      <EventFilterBar
        environments={[]}
        environment=""
        onEnvironmentChange={() => {}}
        timeRange={ALL_TIME_WINDOW}
        onTimeRangeChange={() => {}}
      />
    );

    expect(screen.queryByRole("combobox", { name: "Environment" })).toBeNull();
    expect(screen.getByRole("button", { name: /Time range:/ })).toBeDefined();
  });
});
