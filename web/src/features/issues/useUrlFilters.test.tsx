import { render } from "@testing-library/react";
import { act } from "react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useUrlFilters, type UrlFilterControls } from "./useUrlFilters";

const SEARCH_DEBOUNCE_MS = 300;

interface Probe {
  controls: UrlFilterControls;
  search: string;
}

/**
 * Renders the hook and keeps the latest controls and URL reachable. `act`
 * comes from `react` — `react-dom/test-utils` was removed in React 19.
 */
function renderHook(initialSearch = ""): Probe {
  const probe = { controls: null as UrlFilterControls | null, search: "" };

  function Harness() {
    probe.controls = useUrlFilters();
    probe.search = useLocation().search;
    return null;
  }

  render(
    <MemoryRouter initialEntries={[`/acme/issues${initialSearch}`]}>
      <Harness />
    </MemoryRouter>
  );

  return probe as Probe;
}

describe("useUrlFilters", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("writes the search draft to the URL once the debounce elapses", () => {
    const probe = renderHook();

    act(() => probe.controls.setQueryDraft("boom"));
    expect(probe.search).not.toContain("q=boom");

    act(() => vi.advanceTimersByTime(SEARCH_DEBOUNCE_MS));

    expect(probe.search).toContain("q=boom");
  });

  /**
   * The invariant the debounced writer is built around: it must fire on the
   * debounced text and nothing else. Were it to react to `filters` as well, the
   * commit that clears them would immediately re-run it with the draft still
   * holding the text that was just cleared, and "Clear filters" would undo
   * itself in the same tick. Verified by adding `filters` to that effect's
   * dependencies: that fails this test and the draft-reset one below, because
   * re-committing the query also keeps the URL from ever clearing it.
   */
  it("does not re-commit the debounced query when the filters change elsewhere", () => {
    const probe = renderHook();

    act(() => probe.controls.setQueryDraft("boom"));
    act(() => vi.advanceTimersByTime(SEARCH_DEBOUNCE_MS));
    expect(probe.search).toContain("q=boom");

    act(() => probe.controls.clear());

    expect(probe.search).not.toContain("q=boom");
  });

  it("lets the URL win over a draft the user has stopped editing", () => {
    const probe = renderHook("?q=boom");

    act(() => probe.controls.setQueryDraft("bo"));
    expect(probe.controls.queryDraft).toBe("bo");

    act(() => probe.controls.clear());

    expect(probe.controls.queryDraft).toBe("");
  });
});
