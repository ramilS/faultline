import type { IgnoreCondition, IgnoreUntil } from "../api.ts";

export type IgnoreLabelStyle = "full" | "compact";

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });

function formatUntil(iso: string): string {
  return dateFormat.format(new Date(iso));
}

export function ignoreUntilLabel(
  ignore: IgnoreUntil,
  style: IgnoreLabelStyle,
  now: Date = new Date(),
  formatDate: (iso: string) => string = formatUntil
): string {
  if (ignore.kind === "duration") {
    if (new Date(ignore.until) <= now) {
      return style === "full" ? "Ignored · comes back on its next event" : "back on next event";
    }
    const until = formatDate(ignore.until);
    return style === "full" ? `Ignored until ${until}` : `until ${until}`;
  }
  const count = ignore.count.toLocaleString();
  const soFar = ignore.so_far.toLocaleString();
  return style === "full"
    ? `Ignored until ${count} more ${ignore.kind} · ${soFar} so far`
    : `${soFar} of ${count} ${ignore.kind}`;
}

export interface IgnoreChoice {
  readonly label: string;
  readonly condition: IgnoreCondition;
}

export const IGNORE_CHOICE_GROUPS: ReadonlyArray<readonly IgnoreChoice[]> = [
  [
    { label: "For 1 hour", condition: { duration: "1h" } },
    { label: "For 24 hours", condition: { duration: "24h" } },
    { label: "For 7 days", condition: { duration: "7d" } },
    { label: "For 30 days", condition: { duration: "30d" } }
  ],
  [
    { label: "Until 10 more events", condition: { events: 10 } },
    { label: "Until 100 more events", condition: { events: 100 } },
    { label: "Until 1,000 more events", condition: { events: 1000 } }
  ],
  [
    { label: "Until 10 more users", condition: { users: 10 } },
    { label: "Until 100 more users", condition: { users: 100 } },
    { label: "Until 1,000 more users", condition: { users: 1000 } }
  ]
];
