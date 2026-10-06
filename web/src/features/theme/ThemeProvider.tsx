import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useState,
  type ReactNode
} from "react";

import {
  applyTheme,
  browserStorage,
  readStoredPreference,
  resolveTheme,
  systemPrefersDark,
  watchSystemTheme,
  writeStoredPreference,
  type ResolvedTheme,
  type ThemePreference
} from "@/lib/theme";

interface ThemeContextValue {
  readonly preference: ThemePreference;
  readonly resolved: ResolvedTheme;
  setPreference: (preference: ThemePreference) => void;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

/**
 * Holds the user's choice and the operating system's current state as two
 * independent values, resolving them on every render.
 *
 * Keeping them separate is what lets an explicit choice outrank the OS: the
 * media subscription stays active so that returning to "system" resolves
 * against the present appearance, but it reaches `resolved` only while "system"
 * is selected.
 */
export function ThemeProvider({ children }: { children: ReactNode }) {
  const [preference, setPreferenceState] = useState<ThemePreference>(() =>
    readStoredPreference(browserStorage())
  );
  const [prefersDark, setPrefersDark] = useState<boolean>(systemPrefersDark);

  useEffect(() => watchSystemTheme(setPrefersDark), []);

  const resolved = resolveTheme(preference, prefersDark);

  // Layout, not passive: an ordinary effect commits after paint, which shows as
  // one frame of the previous theme on every change.
  useLayoutEffect(() => {
    applyTheme(resolved);
  }, [resolved]);

  const setPreference = useCallback((next: ThemePreference) => {
    setPreferenceState(next);
    writeStoredPreference(browserStorage(), next);
  }, []);

  const value = useMemo(
    () => ({ preference, resolved, setPreference }),
    [preference, resolved, setPreference]
  );

  return <ThemeContext value={value}>{children}</ThemeContext>;
}

export function useTheme(): ThemeContextValue {
  const value = useContext(ThemeContext);

  if (value === null) {
    throw new Error("useTheme must be used within a ThemeProvider");
  }

  return value;
}
