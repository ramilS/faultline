import { act, render } from "@testing-library/react";
import { MemoryRouter, useNavigate } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ScrollToTop } from "./ScrollToTop";

let navigateTo: (to: string) => void = () => {};

function Harness() {
  const navigate = useNavigate();
  navigateTo = (to) => void navigate(to);
  return <ScrollToTop />;
}

describe("ScrollToTop", () => {
  const scrollTo = vi.fn();

  beforeEach(() => {
    window.scrollTo = scrollTo;
  });

  afterEach(() => {
    scrollTo.mockReset();
  });

  it("returns to the top of the page when the path changes", () => {
    render(
      <MemoryRouter initialEntries={["/shop/issues"]}>
        <Harness />
      </MemoryRouter>
    );
    scrollTo.mockClear();

    act(() => navigateTo("/shop/issues/7"));

    expect(scrollTo).toHaveBeenCalledWith(0, 0);
  });

  it("keeps the scroll position when only the query string changes", () => {
    render(
      <MemoryRouter initialEntries={["/shop/issues"]}>
        <Harness />
      </MemoryRouter>
    );
    scrollTo.mockClear();

    act(() => navigateTo("/shop/issues?status=resolved"));

    expect(scrollTo).not.toHaveBeenCalled();
  });

  it("keeps the scroll position when only the hash changes", () => {
    render(
      <MemoryRouter initialEntries={["/shop/issues/7"]}>
        <Harness />
      </MemoryRouter>
    );
    scrollTo.mockClear();

    act(() => navigateTo("/shop/issues/7#section-stack-trace"));

    expect(scrollTo).not.toHaveBeenCalled();
  });
});
