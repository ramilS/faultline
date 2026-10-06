import { useEffect } from "react";
import { useLocation } from "react-router-dom";

/**
 * A client-side navigation keeps the previous page's scroll offset, so opening
 * an issue from the bottom of a long stream lands mid-page. Only a changed
 * path resets it: filters and event stepping rewrite the query string, and
 * jump-to-section links rewrite the hash, and both must stay where they are.
 */
export function ScrollToTop() {
  const { pathname } = useLocation();

  useEffect(() => {
    window.scrollTo(0, 0);
  }, [pathname]);

  return null;
}
