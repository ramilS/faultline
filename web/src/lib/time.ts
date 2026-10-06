const PLACEHOLDER = "—";

const SECOND = 1000;
const MINUTE = 60 * SECOND;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;
const WEEK = 7 * DAY;

const absoluteFormat = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "medium"
});

function parse(iso: string): Date | null {
  const parsed = new Date(iso);
  return Number.isNaN(parsed.getTime()) ? null : parsed;
}

/**
 * A short age for dense tables: `now`, `45s`, `12m`, `3h`, `6d`, `8w`.
 *
 * Always rounds down, so a value never claims more elapsed time than has
 * passed. Future timestamps read as `now` rather than showing a negative age —
 * server and browser clocks drift, and "-3s" in a column looks like a defect.
 */
export function compactAge(iso: string, now: Date = new Date()): string {
  const at = parse(iso);
  if (at === null) {
    return PLACEHOLDER;
  }

  const elapsed = now.getTime() - at.getTime();
  if (elapsed < SECOND) {
    return "now";
  }
  if (elapsed < MINUTE) {
    return `${Math.floor(elapsed / SECOND)}s`;
  }
  if (elapsed < HOUR) {
    return `${Math.floor(elapsed / MINUTE)}m`;
  }
  if (elapsed < DAY) {
    return `${Math.floor(elapsed / HOUR)}h`;
  }
  if (elapsed < WEEK) {
    return `${Math.floor(elapsed / DAY)}d`;
  }
  return `${Math.floor(elapsed / WEEK)}w`;
}

/** The full instant, for tooltips and anywhere precision beats brevity. */
export function absoluteTime(iso: string): string {
  const at = parse(iso);
  return at === null ? PLACEHOLDER : absoluteFormat.format(at);
}

const hourAxisFormat = (timeZone?: string) =>
  new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit", hourCycle: "h23", timeZone });
const dayAxisFormat = (timeZone?: string) =>
  new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", timeZone });

/**
 * A short chart-axis label for one bucket: the time of day for hourly buckets,
 * the date for daily ones. `timeZone` exists so tests are not at the mercy of
 * the machine's zone; the dashboard itself always uses the viewer's.
 */
export function axisLabel(iso: string, unit: "hour" | "day", timeZone?: string): string {
  const at = parse(iso);
  if (at === null) {
    return PLACEHOLDER;
  }
  return (unit === "hour" ? hourAxisFormat(timeZone) : dayAxisFormat(timeZone)).format(at);
}
