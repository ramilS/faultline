import { Copy } from "lucide-react";
import type { ReactNode } from "react";

import { Button, type ButtonProps } from "@/components/ui/button";
import { copyToClipboard } from "@/lib/clipboard";

interface CopyButtonProps {
  /**
   * The text to copy, or a function producing it. Use a function for
   * anything that can change without this component remounting — e.g. the
   * current URL — so the value is read at click time, not at render time.
   */
  value: string | (() => string);
  label: string;
  variant?: ButtonProps["variant"];
  size?: ButtonProps["size"];
  /**
   * Visible text alongside the icon. Omit for the icon-only affordance used
   * next to a value; when given, it becomes the button's accessible name and
   * the `Copy ${label}` aria-label is dropped so the two never disagree.
   */
  children?: ReactNode;
}

/** Matches the copy button in `features/settings/KeyList.tsx`. */
export function CopyButton({ value, label, variant = "ghost", size = "icon-xs", children }: CopyButtonProps) {
  return (
    <Button
      type="button"
      variant={variant}
      size={size}
      aria-label={children === undefined ? `Copy ${label}` : undefined}
      onClick={() => void copyToClipboard(typeof value === "function" ? value() : value, label)}
    >
      <Copy aria-hidden="true" />
      {children}
    </Button>
  );
}
