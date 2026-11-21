import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { toast } from "sonner";

import { copyLoadedText } from "./clipboard";

class FakeClipboardItem {
  constructor(readonly items: Record<string, Promise<Blob>>) {}
}

function installClipboard(clipboard: Partial<Clipboard>, withItem: boolean) {
  vi.stubGlobal("navigator", { clipboard });
  vi.stubGlobal("ClipboardItem", withItem ? FakeClipboardItem : undefined);
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

describe("copyLoadedText", () => {
  it("hands the pending text to a ClipboardItem so the write stays inside the click", async () => {
    const write = vi.fn(async (items: FakeClipboardItem[]) => {
      expect(await (await items[0].items["text/plain"]).text()).toBe("# Issue 42");
    });
    installClipboard({ write } as unknown as Clipboard, true);

    await copyLoadedText(async () => "# Issue 42", "Digest");

    expect(write).toHaveBeenCalledTimes(1);
    expect(toast.success).toHaveBeenCalledWith("Digest copied to clipboard");
  });

  it("falls back to writeText where ClipboardItem is missing", async () => {
    const writeText = vi.fn(async () => {});
    installClipboard({ writeText }, false);

    await copyLoadedText(async () => "# Issue 42", "Digest");

    expect(writeText).toHaveBeenCalledWith("# Issue 42");
  });

  it("reports why the text failed to load rather than the clipboard's complaint", async () => {
    const write = vi.fn(async (items: FakeClipboardItem[]) => {
      await items[0].items["text/plain"].catch(() => {
        throw new Error("clipboard write refused");
      });
    });
    installClipboard({ write } as unknown as Clipboard, true);

    await copyLoadedText(async () => {
      throw new Error("issue not found");
    }, "Digest");

    expect(toast.error).toHaveBeenCalledWith("Digest could not be copied: issue not found");
  });
});
