import { useEffect, useState } from "react";

/**
 * Trailing-edge debounce for a changing value.
 *
 * The issue search previously issued one request per keystroke
 * (`web/src/App.tsx:259` before the redesign), because the query string was a
 * direct dependency of the loader.
 */
export function useDebounce<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState<T>(value);

  useEffect(() => {
    const handle = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(handle);
  }, [value, delayMs]);

  return debounced;
}
