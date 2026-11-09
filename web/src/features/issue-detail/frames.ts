import type { SourceLinkSetting, SymbolicatedException, SymbolicatedFrame } from "../../api.ts";
import { displayFunction, displayLocation } from "../../stacktrace.ts";

/** Which frames a stack trace shows. Vendor frames are noise far more often than signal. */
export type FrameFilter = "in-app" | "all";

/**
 * Symbolication copies `in_app` verbatim from the generated position onto the
 * resolved original (`internal/sourcemap/resolver.go:138`), so the generated
 * position is the single source of truth and the two can never disagree.
 */
export function isInAppFrame(frame: SymbolicatedFrame): boolean {
  return frame.generated.in_app;
}

/**
 * True when the frame resolved to a real file. Events ingested without
 * `filename`/`lineno` carry a bare function name, and rendering their location
 * would print a meaningless `:0:0`.
 */
export function hasSourceLocation(frame: SymbolicatedFrame): boolean {
  return (frame.original ?? frame.generated).filename !== "";
}

export function hasFrameDetail(frame: SymbolicatedFrame): boolean {
  return frame.context.length > 0 || frame.vars !== null;
}

/** One row of a rendered trace: a frame, or a folded run of consecutive vendor frames. */
export type FrameRow =
  | { readonly kind: "frame"; readonly index: number; readonly frame: SymbolicatedFrame }
  | { readonly kind: "vendor-run"; readonly start: number; readonly frames: readonly SymbolicatedFrame[] };

/**
 * The rows a trace renders under `filter`.
 *
 * Under the in-app filter vendor frames are folded rather than dropped: each
 * run of consecutive vendor frames becomes one row in the place it occupied,
 * so the reader still sees where the call went into a library and can open
 * that run alone instead of switching the whole trace to the full view.
 * `index` and `start` are positions in `frames`, stable across filters.
 */
export function frameRows(frames: readonly SymbolicatedFrame[], filter: FrameFilter): readonly FrameRow[] {
  if (filter === "all") {
    return frames.map((frame, index) => ({ kind: "frame", index, frame }));
  }
  const rows: FrameRow[] = [];
  let run: SymbolicatedFrame[] = [];
  let runStart = 0;
  frames.forEach((frame, index) => {
    if (!isInAppFrame(frame)) {
      if (run.length === 0) {
        runStart = index;
      }
      run.push(frame);
      return;
    }
    if (run.length > 0) {
      rows.push({ kind: "vendor-run", start: runStart, frames: run });
      run = [];
    }
    rows.push({ kind: "frame", index, frame });
  });
  if (run.length > 0) {
    rows.push({ kind: "vendor-run", start: runStart, frames: run });
  }
  return rows;
}

/**
 * The frame a trace opens with its source or variables showing, or null when
 * no frame carries either.
 *
 * Frames arrive innermost first, so the first in-app frame is where the
 * application's own code was when it broke — the line a reader came for.
 * Only that one opens: every frame open at once buries it under the source of
 * its callers. A trace with no in-app source falls back to the innermost frame
 * that has any, so a vendor-only trace still opens on something.
 */
export function defaultExpandedFrame(frames: readonly SymbolicatedFrame[]): number | null {
  const inApp = frames.findIndex((frame) => isInAppFrame(frame) && hasFrameDetail(frame));
  if (inApp !== -1) {
    return inApp;
  }
  const any = frames.findIndex(hasFrameDetail);
  return any === -1 ? null : any;
}

function encodePath(path: string): string {
  return path.split("/").map(encodeURIComponent).join("/");
}

function repositoryPath(filename: string, setting: SourceLinkSetting): string {
  const stackRoot = setting.source_stack_root.replace(/\/+$/, "");
  const underStackRoot = stackRoot !== "" && (filename === stackRoot || filename.startsWith(`${stackRoot}/`));
  const relative = underStackRoot ? filename.slice(stackRoot.length) : filename;
  const sourceRoot = setting.source_root.replace(/\/+$/, "");
  const trimmed = relative.replace(/^\/+/, "");
  return sourceRoot === "" ? trimmed : `${sourceRoot}/${trimmed}`;
}

/**
 * The path comes from the original position when symbolication found one,
 * since that is the file the repository holds. The result is checked to be an
 * http(s) URL again even though the server validates the template: it lands in
 * an `href`, and a row edited by hand skips that validation.
 */
export function sourceLink(frame: SymbolicatedFrame, release: string | null, setting: SourceLinkSetting): string | null {
  const template = setting.source_link_template;
  const position = frame.original ?? frame.generated;
  if (template === "" || !isInAppFrame(frame) || position.filename === "" || position.line <= 0) {
    return null;
  }
  if (template.includes("{release}") && (release === null || release === "")) {
    return null;
  }
  const link = template
    .replaceAll("{path}", encodePath(repositoryPath(position.filename, setting)))
    .replaceAll("{line}", String(position.line))
    .replaceAll("{release}", encodeURIComponent(release ?? ""));
  try {
    const protocol = new URL(link).protocol;
    return protocol === "https:" || protocol === "http:" ? link : null;
  } catch {
    return null;
  }
}

/**
 * The filter a trace should open with.
 *
 * Defaulting to in-app is what makes a trace readable, but a chain whose frames
 * are all vendor would then render nothing at all — a blank panel reads as a
 * failure, not as a filter.
 */
export function initialFrameFilter(chain: readonly SymbolicatedException[]): FrameFilter {
  const anyInApp = chain.some((exception) => exception.frames.some(isInAppFrame));
  return anyInApp ? "in-app" : "all";
}

function frameLine(frame: SymbolicatedFrame): string {
  const location = hasSourceLocation(frame) ? ` (${displayLocation(frame)})` : "";
  return `  at ${displayFunction(frame)}${location}`;
}

function rowLine(row: FrameRow): string {
  if (row.kind === "frame") {
    return frameLine(row.frame);
  }
  const count = row.frames.length;
  return `  … ${count} vendor ${count === 1 ? "frame" : "frames"}`;
}

function exceptionText(exception: SymbolicatedException, filter: FrameFilter, isCause: boolean): string {
  const header = exception.value === "" ? exception.type : `${exception.type}: ${exception.value}`;
  const lines = [isCause ? `Caused by: ${header}` : header, ...frameRows(exception.frames, filter).map(rowLine)];
  return lines.join("\n");
}

/**
 * The stack trace as plain text, mirroring what `StackTrace` renders under
 * `filter` — the cause chain, and under the in-app filter each folded vendor
 * run as one marker line in its place — rather than a JSON dump: pasting a
 * trace into a chat is the whole point of this button.
 */
export function stackTraceText(chain: readonly SymbolicatedException[], filter: FrameFilter): string {
  return chain.map((exception, index) => exceptionText(exception, filter, index > 0)).join("\n\n");
}
