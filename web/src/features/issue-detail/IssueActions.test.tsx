import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// IssueActions reads the signed-in user via useSession() to mark them in the
// assignee menu — mocking it here is the smallest way to render the component
// without standing up a real SessionProvider (which probes `/auth/me`). The
// share button under test here doesn't touch the session at all.
vi.mock("@/features/auth/SessionProvider", () => ({
  useSession: () => ({
    session: { state: "authenticated", user: { id: 1, name: "Ada", email: "ada@example.com", role: "member" } }
  })
}));

// The share button reads `window.location.href` at click time rather than
// render time — this is the behaviour under test, so the URL is pinned here
// rather than left to whatever jsdom's default is.
const SHARE_URL = "https://app.example.com/acme/issues/42?event=evt-9";

// CopyButton calls this to perform the actual clipboard write. Mocking it is
// the only way to observe what was copied, following the same pattern
// CopyButton.test.tsx uses.
vi.mock("@/lib/clipboard", () => ({
  copyToClipboard: vi.fn()
}));

import { copyToClipboard } from "@/lib/clipboard";

import type { IssueSummary } from "@/api";

import { IssueActions } from "./IssueActions";

const mockedCopyToClipboard = vi.mocked(copyToClipboard);

function buildIssue(overrides: Partial<IssueSummary> = {}): IssueSummary {
  return {
    id: 42,
    fingerprint: "fp-1",
    title: "TimeoutError: upstream timed out",
    culprit: "billing.charge_customer",
    level: "error",
    status: "unresolved",
    assignee: null,
    times_seen: 12,
    users_seen: 3,
    first_seen: "2026-09-01T00:00:00Z",
    last_seen: "2026-09-09T00:00:00Z",
    ...overrides
  };
}

describe("IssueActions share button", () => {
  const originalLocation = window.location;

  beforeEach(() => {
    mockedCopyToClipboard.mockReset();
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { ...originalLocation, href: SHARE_URL }
    });
  });

  afterEach(() => {
    Object.defineProperty(window, "location", { configurable: true, value: originalLocation });
  });

  it("is present with a discernible name", () => {
    render(<IssueActions issue={buildIssue()} slug="acme" onChanged={() => {}} onMerged={() => {}} />);

    expect(screen.getByRole("button", { name: "Share" })).toBeDefined();
  });

  it("copies the current page URL at click time, not a value captured at render", () => {
    render(<IssueActions issue={buildIssue()} slug="acme" onChanged={() => {}} onMerged={() => {}} />);

    // Change the URL after render, before the click — a render-time read
    // would still copy the stale value the location had when this mounted.
    const laterUrl = "https://app.example.com/acme/issues/42?event=evt-10";
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { ...originalLocation, href: laterUrl }
    });

    fireEvent.click(screen.getByRole("button", { name: "Share" }));

    expect(mockedCopyToClipboard).toHaveBeenCalledWith(laterUrl, "Issue link");
  });
});
