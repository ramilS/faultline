import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/apiClient", () => ({
  api: { listAssignableUsers: vi.fn() }
}));

import type { AssignableUser } from "@/api";
import { api } from "@/apiClient";

import { AssigneeMenu } from "./AssigneeMenu";

const listAssignableUsers = vi.mocked(api.listAssignableUsers);

const ADA: AssignableUser = { id: 1, name: "Ada", email: "ada@example.com" };
const GRACE: AssignableUser = { id: 2, name: "Grace", email: "grace@example.com" };

function renderMenu(overrides: Partial<Parameters<typeof AssigneeMenu>[0]> = {}) {
  const onAssign = vi.fn();
  render(
    <AssigneeMenu slug="acme" assignee={null} currentUserId={1} disabled={false} onAssign={onAssign} {...overrides} />
  );
  return { onAssign };
}

function openMenu() {
  fireEvent.pointerDown(screen.getByRole("button", { name: /Assignee/ }), {
    button: 0,
    ctrlKey: false,
    pointerType: "mouse"
  });
}

describe("AssigneeMenu", () => {
  beforeEach(() => {
    listAssignableUsers.mockReset();
  });

  it("names the current assignee on the trigger", () => {
    renderMenu({ assignee: { id: 2, name: "Grace", email: "grace@example.com" } });

    expect(screen.getByRole("button", { name: "Assignee: Grace" })).toBeDefined();
  });

  it("says the issue is unassigned when nobody holds it", () => {
    renderMenu();

    expect(screen.getByRole("button", { name: "Assignee: Unassigned" })).toBeDefined();
  });

  it("does not ask the server for teammates until the menu is opened", () => {
    renderMenu();

    expect(listAssignableUsers).not.toHaveBeenCalled();
  });

  it("lists the project's assignable teammates and marks the signed-in user", async () => {
    listAssignableUsers.mockResolvedValue([ADA, GRACE]);
    renderMenu();

    openMenu();

    expect(await screen.findByRole("menuitemradio", { name: "Ada (you)" })).toBeDefined();
    expect(screen.getByRole("menuitemradio", { name: "Grace" })).toBeDefined();
    expect(listAssignableUsers).toHaveBeenCalledWith("acme");
  });

  it("assigns the issue to the teammate that is picked", async () => {
    listAssignableUsers.mockResolvedValue([ADA, GRACE]);
    const { onAssign } = renderMenu();
    openMenu();

    fireEvent.click(await screen.findByRole("menuitemradio", { name: "Grace" }));

    expect(onAssign).toHaveBeenCalledWith(2);
  });

  it("unassigns when Unassigned is picked", async () => {
    listAssignableUsers.mockResolvedValue([ADA, GRACE]);
    const { onAssign } = renderMenu({ assignee: GRACE });
    openMenu();

    fireEvent.click(await screen.findByRole("menuitemradio", { name: "Unassigned" }));

    expect(onAssign).toHaveBeenCalledWith(null);
  });

  it("does nothing when the current assignee is picked again", async () => {
    listAssignableUsers.mockResolvedValue([ADA, GRACE]);
    const { onAssign } = renderMenu({ assignee: GRACE });
    openMenu();

    fireEvent.click(await screen.findByRole("menuitemradio", { name: "Grace" }));

    expect(onAssign).not.toHaveBeenCalled();
  });

  it("keeps the current assignee selectable when the server no longer lists them as assignable", async () => {
    listAssignableUsers.mockResolvedValue([ADA]);
    renderMenu({ assignee: { id: 9, name: "Former Teammate", email: "gone@example.com" } });

    openMenu();

    expect(await screen.findByRole("menuitemradio", { name: "Former Teammate" })).toBeDefined();
  });

  it("explains a failed load instead of showing an empty list, and retries on the next open", async () => {
    listAssignableUsers.mockRejectedValueOnce(new Error("network down"));
    listAssignableUsers.mockResolvedValueOnce([ADA]);
    renderMenu();

    openMenu();
    expect(await screen.findByText(/Could not load teammates/)).toBeDefined();

    fireEvent.keyDown(screen.getByRole("menu"), { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    openMenu();

    expect(await screen.findByRole("menuitemradio", { name: "Ada (you)" })).toBeDefined();
    expect(listAssignableUsers).toHaveBeenCalledTimes(2);
  });

  it("cannot be opened while a change is in flight", () => {
    renderMenu({ disabled: true });

    expect(screen.getByRole("button", { name: /Assignee/ }).hasAttribute("disabled")).toBe(true);
  });
});
