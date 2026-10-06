import { Monitor, Moon, Sun, type LucideIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger
} from "@/components/ui/dropdown-menu";
import { useTheme } from "@/features/theme/ThemeProvider";
import { isThemePreference, type ThemePreference } from "@/lib/theme";

/** Menu order. */
const THEME_PREFERENCES: readonly ThemePreference[] = ["light", "dark", "system"];

// Records keyed by the union rather than an array of objects: a fourth
// preference becomes a compile error in both maps instead of an `undefined` at
// one lookup.
export const THEME_LABELS: Record<ThemePreference, string> = {
  light: "Light",
  dark: "Dark",
  system: "System"
};

// `LucideIcon` is the icon type this codebase already uses (`app/navItems.ts:6`).
export const THEME_ICONS: Record<ThemePreference, LucideIcon> = {
  light: Sun,
  dark: Moon,
  system: Monitor
};

interface ThemeRadioGroupProps {
  readonly preference: ThemePreference;
  onSelect: (preference: ThemePreference) => void;
}

/**
 * The three options as radio items. Exported because `MobileNav` renders the
 * same choice inside a submenu — a dropdown does not nest inside another
 * dropdown's trigger, so the option list is shared instead of the component.
 */
export function ThemeRadioGroup({ preference, onSelect }: ThemeRadioGroupProps) {
  return (
    <DropdownMenuRadioGroup
      value={preference}
      onValueChange={(value) => {
        // Radix hands back a plain string; the guard is the same one the stored
        // value goes through, so there is one definition of "valid preference".
        if (isThemePreference(value)) {
          onSelect(value);
        }
      }}
    >
      {THEME_PREFERENCES.map((value) => (
        <DropdownMenuRadioItem key={value} value={value}>
          {THEME_LABELS[value]}
        </DropdownMenuRadioItem>
      ))}
    </DropdownMenuRadioGroup>
  );
}

interface ThemeToggleProps {
  /** `labeled` matches the sidebar's full-width buttons; `icon` is for tight corners. */
  readonly variant: "labeled" | "icon";
}

export function ThemeToggle({ variant }: ThemeToggleProps) {
  const { preference, setPreference } = useTheme();
  const Icon = THEME_ICONS[preference];
  const label = THEME_LABELS[preference];

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        {variant === "labeled" ? (
          <Button
            variant="ghost"
            size="sm"
            className="text-muted-foreground hover:text-foreground w-full justify-start"
            aria-label={`Colour scheme: ${label}`}
          >
            <Icon className="size-4" aria-hidden="true" />
            Theme: {label}
          </Button>
        ) : (
          <Button
            variant="ghost"
            size="icon"
            className="text-muted-foreground hover:text-foreground"
            aria-label={`Colour scheme: ${label}`}
          >
            <Icon className="size-4" aria-hidden="true" />
          </Button>
        )}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-40">
        <ThemeRadioGroup preference={preference} onSelect={setPreference} />
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
