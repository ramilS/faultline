/**
 * Below `md` a table of five columns cannot fit a phone, and scrolling
 * sideways hides the columns that matter most: the DSN, the email, the row's
 * actions. These classes, passed to `<Table className>`, turn each row into a
 * stacked block instead so every cell stays reachable, and wrap normally
 * because the base cell style forbids wrapping. The header row is kept
 * for assistive technology but not drawn; the cell contents (a badge, a
 * button, a value) already say what they are.
 */
export const STACKED_ROWS_BELOW_MD = [
  "max-md:block",
  "max-md:[&_thead]:sr-only",
  "max-md:[&_tbody]:block",
  "max-md:[&_tr]:block max-md:[&_tr]:py-3",
  "max-md:[&_td]:block max-md:[&_td]:w-auto max-md:[&_td]:max-w-none max-md:[&_td]:px-0 max-md:[&_td]:py-1 max-md:[&_td]:text-left max-md:[&_td]:whitespace-normal"
].join(" ");
