import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

vi.mock("@/apiClient", () => ({
  api: { patchIssue: vi.fn() }
}));

import type { IssueDetail } from "@/api";
import { api } from "@/apiClient";

import { useBulkTriage } from "./useBulkTriage";

const patchIssue = vi.mocked(api.patchIssue);

describe("useBulkTriage", () => {
  it("sends the ignore condition with every selected issue", async () => {
    patchIssue.mockResolvedValue({ issue: { id: 0 } } as IssueDetail);
    const { result } = renderHook(() => useBulkTriage("filters", () => {}));

    act(() => result.current.setAllSelected([4, 9], true));
    act(() => result.current.apply("ignored", { users: 10 }));

    await waitFor(() => expect(result.current.busy).toBe(false));
    expect(patchIssue.mock.calls).toEqual([
      [4, { status: "ignored", ignore_until: { users: 10 } }],
      [9, { status: "ignored", ignore_until: { users: 10 } }]
    ]);
  });
});
