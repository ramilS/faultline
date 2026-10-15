import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const toasts = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));

vi.mock("sonner", () => ({ toast: toasts }));

vi.mock("@/apiClient", () => ({
  api: {
    listNotificationChannels: vi.fn(),
    createNotificationChannel: vi.fn(),
    updateNotificationChannel: vi.fn(),
    deleteNotificationChannel: vi.fn(),
    testNotificationChannel: vi.fn()
  }
}));

import { ApiError, type NotificationChannel } from "@/api";
import { api } from "@/apiClient";

import { NotificationsPage } from "./NotificationsPage";

const listChannels = vi.mocked(api.listNotificationChannels);
const createChannel = vi.mocked(api.createNotificationChannel);
const updateChannel = vi.mocked(api.updateNotificationChannel);
const deleteChannel = vi.mocked(api.deleteNotificationChannel);
const testChannel = vi.mocked(api.testNotificationChannel);

// Radix Select opens on pointerdown and probes pointer capture and scrolling
// APIs that jsdom does not implement.
Element.prototype.hasPointerCapture ??= () => false;
Element.prototype.setPointerCapture ??= () => {};
Element.prototype.releasePointerCapture ??= () => {};
Element.prototype.scrollIntoView ??= () => {};

const telegram: NotificationChannel = {
  id: 3,
  name: "Backend on-call",
  kind: "telegram",
  target: "-1001234567890",
  has_secret: true,
  enabled: true,
  notify_new: true,
  notify_regression: true,
  environments: ["production"],
  min_level: "error",
  last_success_at: "2026-10-15T08:00:00Z",
  last_error: "telegram: HTTP 403: Forbidden: bot was kicked from the group chat",
  last_error_at: "2026-10-15T09:00:00Z",
  created_at: "2026-10-01T08:00:00Z"
};

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/shop/settings/notifications"]}>
      <Routes>
        <Route path="/:slug/settings/notifications" element={<NotificationsPage />} />
      </Routes>
    </MemoryRouter>
  );
}

function chooseOption(comboboxName: string, option: string) {
  fireEvent.pointerDown(screen.getByRole("combobox", { name: comboboxName }), { button: 0, ctrlKey: false, pointerType: "mouse" });
  fireEvent.click(screen.getByRole("option", { name: option }));
}

describe("NotificationsPage", () => {
  beforeEach(() => {
    for (const mock of [listChannels, createChannel, updateChannel, deleteChannel, testChannel, toasts.success, toasts.error]) {
      mock.mockReset();
    }
  });

  it("lists channels with their filters and a failing delivery's reason", async () => {
    listChannels.mockResolvedValue([telegram]);

    renderPage();

    expect(await screen.findByText("Backend on-call")).toBeDefined();
    expect(screen.getByText("New issues and regressions · error and above · production")).toBeDefined();
    expect(screen.getByText(/bot was kicked from the group chat/)).toBeDefined();
    expect(listChannels).toHaveBeenCalledWith("shop");
  });

  it("offers to add the first channel when there are none", async () => {
    listChannels.mockResolvedValue([]);

    renderPage();

    expect(await screen.findByText("No notification channels yet")).toBeDefined();
  });

  it("creates a Telegram channel from the form", async () => {
    listChannels.mockResolvedValue([]);
    createChannel.mockResolvedValue(telegram);

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Add the first channel" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Backend on-call" } });
    fireEvent.change(screen.getByLabelText("Chat ID"), { target: { value: "-1001234567890" } });
    fireEvent.change(screen.getByLabelText("Bot token"), { target: { value: "123456789:token" } });
    fireEvent.change(screen.getByLabelText("Environments"), { target: { value: "production, staging" } });
    fireEvent.click(screen.getByRole("checkbox", { name: "Regressions — a resolved issue seen again" }));
    fireEvent.click(screen.getByRole("button", { name: "Add channel" }));

    await waitFor(() =>
      expect(createChannel).toHaveBeenCalledWith("shop", {
        name: "Backend on-call",
        kind: "telegram",
        target: "-1001234567890",
        secret: "123456789:token",
        enabled: true,
        notify_new: true,
        notify_regression: false,
        environments: ["production", "staging"],
        min_level: "error"
      })
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(listChannels).toHaveBeenCalledTimes(2);
  });

  it("creates a webhook channel with its own fields", async () => {
    listChannels.mockResolvedValue([]);
    createChannel.mockResolvedValue({ ...telegram, kind: "webhook" });

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Add the first channel" }));
    chooseOption("Type", "Webhook");
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Incident bot" } });
    fireEvent.change(screen.getByLabelText("URL"), { target: { value: "https://hooks.example/faultline" } });
    chooseOption("Minimum level", "warning");
    fireEvent.click(screen.getByRole("button", { name: "Add channel" }));

    await waitFor(() =>
      expect(createChannel).toHaveBeenCalledWith(
        "shop",
        expect.objectContaining({ kind: "webhook", target: "https://hooks.example/faultline", secret: "", min_level: "warning" })
      )
    );
  });

  it("stops a Telegram channel without a token before calling the server", async () => {
    listChannels.mockResolvedValue([]);

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Add the first channel" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Ops" } });
    fireEvent.change(screen.getByLabelText("Chat ID"), { target: { value: "-100" } });
    fireEvent.click(screen.getByRole("button", { name: "Add channel" }));

    expect(await screen.findByText("Enter the bot token from @BotFather.")).toBeDefined();
    expect(createChannel).not.toHaveBeenCalled();
  });

  it("shows the server's rejection inside the dialog", async () => {
    listChannels.mockResolvedValue([]);
    createChannel.mockRejectedValue(new ApiError(400, "bot token must look like 123456789:AA... as issued by @BotFather", null));

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Add the first channel" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Ops" } });
    fireEvent.change(screen.getByLabelText("Chat ID"), { target: { value: "-100" } });
    fireEvent.change(screen.getByLabelText("Bot token"), { target: { value: "nope" } });
    fireEvent.click(screen.getByRole("button", { name: "Add channel" }));

    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByText(/bot token must look like/)).toBeDefined();
  });

  it("saves an edit without resending the stored token", async () => {
    listChannels.mockResolvedValue([telegram]);
    updateChannel.mockResolvedValue(telegram);

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Edit Backend on-call" }));
    expect(screen.getByRole("combobox", { name: "Type" }).hasAttribute("disabled")).toBe(true);
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Backend" } });
    fireEvent.click(screen.getByRole("button", { name: "Save channel" }));

    await waitFor(() => expect(updateChannel).toHaveBeenCalledTimes(1));
    const [slug, id, input] = updateChannel.mock.calls[0];
    expect([slug, id, input.name]).toEqual(["shop", 3, "Backend"]);
    expect("secret" in input).toBe(false);
  });

  it("reports a successful test message", async () => {
    listChannels.mockResolvedValue([telegram]);
    testChannel.mockResolvedValue(undefined);

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Send a test message to Backend on-call" }));

    await waitFor(() => expect(toasts.success).toHaveBeenCalledWith("Test message sent to Backend on-call."));
    expect(testChannel).toHaveBeenCalledWith("shop", 3);
  });

  it("reports why a test message failed", async () => {
    listChannels.mockResolvedValue([telegram]);
    testChannel.mockRejectedValue(new ApiError(502, "telegram: HTTP 400: Bad Request: chat not found", null));

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Send a test message to Backend on-call" }));

    await waitFor(() =>
      expect(toasts.error).toHaveBeenCalledWith("Test message to Backend on-call failed: telegram: HTTP 400: Bad Request: chat not found")
    );
  });

  it("deletes a channel after confirmation", async () => {
    listChannels.mockResolvedValueOnce([telegram]).mockResolvedValueOnce([]);
    deleteChannel.mockResolvedValue(undefined);

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Delete Backend on-call" }));
    fireEvent.click(screen.getByRole("button", { name: "Delete channel" }));

    await waitFor(() => expect(deleteChannel).toHaveBeenCalledWith("shop", 3));
    expect(await screen.findByText("No notification channels yet")).toBeDefined();
  });
});
