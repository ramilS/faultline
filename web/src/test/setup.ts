import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";

/**
 * jsdom implements neither observer, and Floating UI's `autoUpdate` — which
 * Radix installs for every open popper — constructs both. Without these the
 * component under test throws before it can be asserted on, so these stubs are
 * a prerequisite for the test running at all, not a convenience.
 *
 * They deliberately observe nothing: positions are computed once on open, which
 * is what the tests assert. A test that needed a *re*-position on resize would
 * have to drive these itself rather than trust the stub.
 */
class InertObserver {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
  takeRecords(): [] {
    return [];
  }
}

globalThis.ResizeObserver ??= InertObserver as unknown as typeof ResizeObserver;
globalThis.IntersectionObserver ??=
  InertObserver as unknown as typeof IntersectionObserver;

afterEach(cleanup);
