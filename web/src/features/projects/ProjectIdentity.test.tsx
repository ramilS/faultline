import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { makeThumbnail, ProjectIdentity, ProjectIdentityEditor, validProjectEmoji } from "./ProjectIdentity";

const image = "data:image/png;base64,aWNvbg==";

function Editor({ disabled = false }: { disabled?: boolean }) {
  const [emoji, setEmoji] = useState("🚀");
  const [icon, setIcon] = useState(image);
  return (
    <ProjectIdentityEditor
      emoji={emoji}
      image={icon}
      disabled={disabled}
      onChange={(nextEmoji, nextIcon) => { setEmoji(nextEmoji); setIcon(nextIcon); }}
    />
  );
}

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("project identity", () => {
  it("accepts one emoji and rejects text or multiple emoji", () => {
    for (const value of ["", "🚀", "👩🏽‍💻", "🇫🇷", "1️⃣"]) expect(validProjectEmoji(value)).toBe(true);
    for (const value of ["admin", "🚀🚀", "<b>"]) expect(validProjectEmoji(value)).toBe(false);
    render(<Editor />);
    fireEvent.change(screen.getByLabelText("Project emoji"), { target: { value: "🚀🚀" } });
    expect(screen.getByText("Choose one emoji.")).toBeDefined();
  });

  it("prefers image, falls back to emoji, then folder", () => {
    const { container, rerender } = render(<ProjectIdentity project={{ icon_image: image, icon_emoji: "🚀" }} />);
    expect(container.querySelector("img")?.getAttribute("src")).toBe(image);
    expect(screen.queryByText("🚀")).toBeNull();
    rerender(<ProjectIdentity project={{ icon_emoji: "🚀" }} />);
    expect(screen.getByText("🚀")).toBeDefined();
    rerender(<ProjectIdentity project={{}} />);
    expect(container.querySelector("svg")).not.toBeNull();
  });

  it("keeps the independent value while clearing and selecting custom or preset emoji", () => {
    const { container } = render(<Editor />);
    fireEvent.click(screen.getByRole("button", { name: "Remove emoji" }));
    expect((screen.getByLabelText("Project emoji") as HTMLInputElement).value).toBe("");
    expect(container.querySelector("img")).not.toBeNull();
    fireEvent.change(screen.getByLabelText("Project emoji"), { target: { value: "👩🏽‍💻" } });
    fireEvent.click(screen.getByRole("button", { name: "Remove image" }));
    expect(container.querySelector("img")).toBeNull();
    expect((screen.getByLabelText("Project emoji") as HTMLInputElement).value).toBe("👩🏽‍💻");
    fireEvent.click(screen.getByRole("button", { name: "Use 🛒" }));
    expect((screen.getByLabelText("Project emoji") as HTMLInputElement).value).toBe("🛒");
  });

  it("disables every editing control for members", () => {
    const { container } = render(<Editor disabled />);
    expect(container.querySelector("fieldset")?.disabled).toBe(true);
    expect((screen.getByLabelText("Project emoji") as HTMLInputElement).matches(":disabled")).toBe(true);
    expect((screen.getByLabelText("Project image") as HTMLInputElement).matches(":disabled")).toBe(true);
  });

  it("keeps the previous preview on an invalid upload and allows retry", async () => {
    const { container } = render(<Editor />);
    fireEvent.change(screen.getByLabelText("Project image"), { target: { files: [new File(["bad"], "bad.svg", { type: "image/svg+xml" })] } });
    expect(await screen.findByRole("alert")).toBeDefined();
    expect(container.querySelector("img")?.getAttribute("src")).toBe(image);
    await waitFor(() => expect(container.querySelector("fieldset")?.disabled).toBe(false));
  });

  it("resizes large images without stretching and releases the decoded bitmap", async () => {
    const close = vi.fn();
    const bitmap = { width: 512, height: 256, close };
    vi.stubGlobal("createImageBitmap", vi.fn().mockResolvedValue(bitmap));
    const drawImage = vi.fn();
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({ drawImage } as unknown as CanvasRenderingContext2D);
    vi.spyOn(HTMLCanvasElement.prototype, "toDataURL").mockImplementation(function (this: HTMLCanvasElement) {
      expect(this.width).toBe(128);
      expect(this.height).toBe(64);
      return image;
    });
    const jpegHeader = new Uint8Array([0xff, 0xd8, 0xff, 0xc0, 0, 11, 8, 0, 1, 0, 1, 1, 0x11, 0, 0]);
    expect(await makeThumbnail(new File([jpegHeader], "icon.jpg", { type: "image/jpeg" }))).toBe(image);
    expect(drawImage).toHaveBeenCalledWith(bitmap, 0, 0, 128, 64);
    expect(close).toHaveBeenCalledOnce();
  });

  it("rejects huge dimensions before decoding pixels", async () => {
    const decoder = vi.fn();
    vi.stubGlobal("createImageBitmap", decoder);
    const header = new Uint8Array(24);
    header.set([137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, 73, 72, 68, 82]);
    new DataView(header.buffer).setUint32(16, 100_000);
    new DataView(header.buffer).setUint32(20, 100_000);
    await expect(makeThumbnail(new File([header], "huge.png", { type: "image/png" }))).rejects.toThrow();
    expect(decoder).not.toHaveBeenCalled();
  });
});
