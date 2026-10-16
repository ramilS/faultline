import { Folder } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";

import type { Project } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

type Identity = Pick<Project, "icon_emoji" | "icon_image">;

export function ProjectIdentity({ project }: { project: Identity }) {
  if (project.icon_image) {
    return <img src={project.icon_image} alt="" className="size-7 shrink-0 rounded-md object-contain" />;
  }
  if (project.icon_emoji) {
    return (
      <span aria-hidden="true" className="inline-flex size-7 shrink-0 items-center justify-center overflow-hidden text-xl">
        {project.icon_emoji}
      </span>
    );
  }
  return <Folder aria-hidden="true" className="text-muted-foreground size-7 shrink-0 p-1" />;
}

interface ProjectIdentityEditorProps {
  emoji: string;
  image: string;
  disabled?: boolean;
  onChange: (emoji: string, image: string) => void;
  onBusyChange?: (busy: boolean) => void;
}

export function ProjectIdentityEditor({
  emoji,
  image,
  onChange,
  disabled = false,
  onBusyChange
}: ProjectIdentityEditorProps) {
  const id = useId();
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const selection = useRef(0);
  const emojiValid = validProjectEmoji(emoji);

  useEffect(() => () => {
    selection.current++;
    onBusyChange?.(false);
  }, [onBusyChange]);

  const upload = async (file: File | undefined) => {
    if (!file) return;
    const current = ++selection.current;
    setError(null);
    setBusy(true);
    onBusyChange?.(true);
    try {
      const thumbnail = await makeThumbnail(file);
      if (selection.current === current) onChange(emoji, thumbnail);
    } catch {
      if (selection.current === current) {
        setError("Choose a valid PNG or JPEG image under 5 MiB and 16 million pixels.");
      }
    } finally {
      if (selection.current === current) {
        setBusy(false);
        onBusyChange?.(false);
      }
    }
  };

  return (
    <fieldset disabled={disabled || busy} className="space-y-3">
      <legend className="mb-2 text-sm font-medium">Project identity</legend>
      <div className="flex items-center gap-3 rounded-lg border p-3">
        <ProjectIdentity project={{ icon_emoji: emoji, icon_image: image }} />
        <p className="text-muted-foreground text-xs">Image in the dashboard. Emoji in Telegram.</p>
      </div>
      <div className="space-y-2">
        <Label htmlFor={`${id}-emoji`}>Project emoji</Label>
        <Input
          id={`${id}-emoji`}
          value={emoji}
          maxLength={32}
          onChange={(event) => onChange(event.target.value, image)}
          aria-describedby={`${id}-hint${emojiValid ? "" : ` ${id}-emoji-error`}`}
          aria-invalid={!emojiValid}
        />
        <div className="flex flex-wrap gap-1">
          {["🚀", "🛒", "🌐", "⚡", "🛠️", "📱", "🎯", "💎"].map((preset) => (
            <Button
              key={preset}
              type="button"
              variant={emoji === preset ? "secondary" : "outline"}
              size="sm"
              aria-label={`Use ${preset}`}
              aria-pressed={emoji === preset}
              onClick={() => onChange(preset, image)}
            >
              {preset}
            </Button>
          ))}
        </div>
        <p id={`${id}-hint`} className="text-muted-foreground text-xs">
          Choose a preset or enter a custom emoji, including flags and joined emoji. Leave empty for a folder.
        </p>
        {!emojiValid && <p id={`${id}-emoji-error`} role="alert" className="text-destructive text-xs">Choose one emoji.</p>}
        {emoji && (
          <Button type="button" variant="outline" size="sm" onClick={() => onChange("", image)}>
            Remove emoji
          </Button>
        )}
      </div>
      <div className="space-y-2">
        <Label htmlFor={`${id}-image`}>Project image</Label>
        <Input
          id={`${id}-image`}
          type="file"
          accept="image/png,image/jpeg"
          onChange={(event) => {
            void upload(event.target.files?.[0]);
            event.target.value = "";
          }}
        />
        <p className="text-muted-foreground text-xs">
          PNG or JPEG, up to 5 MiB. Resized to fit 128 × 128 pixels.
        </p>
        {image && (
          <Button type="button" variant="outline" size="sm" onClick={() => onChange(emoji, "")}>
            Remove image
          </Button>
        )}
      </div>
      {busy && <p role="status" className="text-sm">Preparing image…</p>}
      {error && <p role="alert" className="text-destructive text-sm">{error}</p>}
    </fieldset>
  );
}

export async function makeThumbnail(file: File): Promise<string> {
  if (!["image/png", "image/jpeg"].includes(file.type) || file.size > 5 * 1024 * 1024) {
    throw new Error("Invalid image");
  }
  const dimensions = readImageDimensions(new Uint8Array(await file.arrayBuffer()), file.type);
  if (!dimensions || !dimensions.width || !dimensions.height || dimensions.width * dimensions.height > 16_000_000) {
    throw new Error("Image too large or invalid");
  }
  const bitmap = await createImageBitmap(file);
  try {
    if (!bitmap.width || !bitmap.height || bitmap.width * bitmap.height > 16_000_000) {
      throw new Error("Image too large");
    }
    const scale = Math.min(1, 128 / bitmap.width, 128 / bitmap.height);
    const canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.round(bitmap.width * scale));
    canvas.height = Math.max(1, Math.round(bitmap.height * scale));
    const context = canvas.getContext("2d");
    if (!context) throw new Error("Canvas unavailable");
    context.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    const value = canvas.toDataURL("image/png");
    if (!value.startsWith("data:image/png;base64,") || value.length > 192 * 1024) {
      throw new Error("Invalid thumbnail");
    }
    return value;
  } finally {
    bitmap.close();
  }
}

export function validProjectEmoji(value: string): boolean {
  if (!value) return true;
  const points = Array.from(value);
  if (points.length > 32) return false;
  if (/^[\u{1F1E6}-\u{1F1FF}]{2}$/u.test(value)) return true;
  if (/^[0-9#*]\uFE0F?\u20E3$/u.test(value)) return true;
  return /^(?:\p{Other_Symbol}(?:[\uFE0E\uFE0F]|[\u{1F3FB}-\u{1F3FF}])*)(?:\u200D\p{Other_Symbol}(?:[\uFE0E\uFE0F]|[\u{1F3FB}-\u{1F3FF}])*)*$/u.test(value) &&
    !points.some((point) => /^[\u{1F1E6}-\u{1F1FF}]$/u.test(point));
}

function readImageDimensions(data: Uint8Array, type: string): { width: number; height: number } | null {
  const view = new DataView(data.buffer, data.byteOffset, data.byteLength);
  if (type === "image/png") {
    if (data.length < 24 || ![137, 80, 78, 71, 13, 10, 26, 10].every((byte, index) => data[index] === byte)) return null;
    if (view.getUint32(8) !== 13 || String.fromCharCode(...data.slice(12, 16)) !== "IHDR") return null;
    return { width: view.getUint32(16), height: view.getUint32(20) };
  }
  if (data.length < 4 || data[0] !== 0xff || data[1] !== 0xd8) return null;
  for (let offset = 2; offset + 4 <= data.length;) {
    if (data[offset] !== 0xff) return null;
    let marker = data[++offset];
    while (marker === 0xff && offset + 1 < data.length) marker = data[++offset];
    offset++;
    if (marker === 0xd9 || marker === 0xda) return null;
    if (offset + 2 > data.length) return null;
    const length = view.getUint16(offset);
    if (length < 2 || offset + length > data.length) return null;
    if ([0xc0, 0xc1, 0xc2, 0xc3, 0xc5, 0xc6, 0xc7, 0xc9, 0xca, 0xcb, 0xcd, 0xce, 0xcf].includes(marker)) {
      if (length < 7) return null;
      return { height: view.getUint16(offset + 3), width: view.getUint16(offset + 5) };
    }
    offset += length;
  }
  return null;
}
