const STORAGE_KEY = "faultline.theme";
const DARK_CLASS = "dark";
const DARK_QUERY = "(prefers-color-scheme: dark)";

/** What the user chose. */
export type ThemePreference = "light" | "dark" | "system";

/** What is actually painted. Never "system". */
export type ResolvedTheme = "light" | "dark";

/**
 * The two methods this module needs. `window.localStorage` satisfies it, and so
 * does a two-property object in a test, with no cast either way.
 */
export type PreferenceStore = Pick<Storage, "getItem" | "setItem">;

export function isThemePreference(value: unknown): value is ThemePreference {
  return value === "light" || value === "dark" || value === "system";
}

export function resolveTheme(
  preference: ThemePreference,
  systemPrefersDark: boolean
): ResolvedTheme {
  switch (preference) {
    case "light":
      return "light";
    case "dark":
      return "dark";
    case "system":
      return systemPrefersDark ? "dark" : "light";
  }
}

/**
 * Reading `window.localStorage` can throw before any `getItem` call — Safari
 * with site data disabled rejects the property access itself — so the access is
 * what gets wrapped, not just the reads.
 */
export function browserStorage(): PreferenceStore | null {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

export function readStoredPreference(store: PreferenceStore | null): ThemePreference {
  if (store === null) {
    return "system";
  }

  try {
    const stored = store.getItem(STORAGE_KEY);
    return isThemePreference(stored) ? stored : "system";
  } catch {
    return "system";
  }
}

/**
 * A storage failure costs the preference its persistence and nothing else: the
 * theme still applies for this session. Reporting it would be noise out of
 * proportion to the consequence, so this is the one swallowed error here.
 */
export function writeStoredPreference(
  store: PreferenceStore | null,
  preference: ThemePreference
): void {
  if (store === null) {
    return;
  }

  try {
    store.setItem(STORAGE_KEY, preference);
  } catch {
    // See above.
  }
}

export function systemPrefersDark(): boolean {
  return window.matchMedia(DARK_QUERY).matches;
}

/** Returns a teardown so callers can unsubscribe. */
export function watchSystemTheme(onChange: (prefersDark: boolean) => void): () => void {
  const media = window.matchMedia(DARK_QUERY);
  const listener = (event: MediaQueryListEvent) => onChange(event.matches);

  media.addEventListener("change", listener);
  return () => media.removeEventListener("change", listener);
}

export function applyTheme(resolved: ResolvedTheme): void {
  document.documentElement.classList.toggle(DARK_CLASS, resolved === "dark");
}
