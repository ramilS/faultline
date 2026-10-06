import { useEffect } from "react";

const PRODUCT = "Faultline";

/** Page name first, so a tab strip of narrow tabs still shows the part that differs. */
export function documentTitle(page: string | null): string {
  const trimmed = page?.trim() ?? "";
  return trimmed === "" ? PRODUCT : `${trimmed} · ${PRODUCT}`;
}

/**
 * Names the browser tab, history entry and screen-reader page announcement
 * after the current screen. A null title means the screen has not resolved
 * what it is called yet, and shows the product name meanwhile.
 */
export function useDocumentTitle(page: string | null): void {
  useEffect(() => {
    document.title = documentTitle(page);
    return () => {
      document.title = PRODUCT;
    };
  }, [page]);
}
