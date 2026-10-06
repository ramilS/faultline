export interface LevelVariant {
  readonly label: string;
  readonly className: string;
}

const FALLBACK_CLASS = "text-level-info bg-level-info-surface";

/**
 * Maps an event level onto its badge presentation.
 *
 * The ingest edge accepts `fatal | error | warning | info | debug`
 * (`internal/ingest/envelope.go`), but stored rows are data, not a guarantee —
 * an unrecognised value is surfaced verbatim rather than hidden or coerced
 * into a severity it does not have.
 */
export function levelVariant(level: string): LevelVariant {
  switch (level.toLowerCase()) {
    case "fatal":
      return { label: "Fatal", className: "text-level-fatal bg-level-fatal-surface" };
    case "error":
      return { label: "Error", className: "text-level-error bg-level-error-surface" };
    case "warning":
      return { label: "Warning", className: "text-level-warning bg-level-warning-surface" };
    case "info":
      return { label: "Info", className: "text-level-info bg-level-info-surface" };
    case "debug":
      return { label: "Debug", className: "text-level-debug bg-level-debug-surface" };
    default:
      return { label: level === "" ? "Unknown" : level, className: FALLBACK_CLASS };
  }
}
