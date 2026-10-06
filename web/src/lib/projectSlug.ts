/**
 * Top-level paths that are not project slugs. Adding a route here without
 * adding it to this set would make the shell offer project navigation for a
 * project that does not exist.
 */
const RESERVED = new Set(["projects", "login"]);

/**
 * Extracts the active project slug from a pathname.
 *
 * The shell renders above the `:slug` route segment, so `useParams` cannot see
 * it. Reading the path keeps that derivation in one tested place instead of
 * spreading router-shape assumptions through the layout.
 */
export function projectSlugFromPath(pathname: string): string | null {
  const [first] = pathname.split("?")[0].split("/").filter((segment) => segment !== "");
  if (first === undefined || RESERVED.has(first)) {
    return null;
  }
  return first;
}

/** The longest slug the server accepts. */
export const MAX_SLUG_LENGTH = 64;

/**
 * A slug the server will accept, suggested from a project name: lowercase
 * letters, digits and single hyphens, never leading or trailing one. Empty when
 * the name has nothing to build one from (say, no Latin letters) — the caller
 * asks the user rather than inventing something.
 */
export function slugFromName(name: string): string {
  return name
    .normalize("NFKD")
    .replace(/\p{M}/gu, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+/, "")
    .slice(0, MAX_SLUG_LENGTH)
    .replace(/-+$/, "");
}
