import type { SymbolicatedException, SymbolicatedFrame } from "../../api.ts";
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

export function visibleFrames(
  frames: readonly SymbolicatedFrame[],
  filter: FrameFilter
): readonly SymbolicatedFrame[] {
  return filter === "all" ? frames : frames.filter(isInAppFrame);
}

export function hiddenFrameCount(frames: readonly SymbolicatedFrame[], filter: FrameFilter): number {
  return frames.length - visibleFrames(frames, filter).length;
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

function exceptionText(exception: SymbolicatedException, filter: FrameFilter, isCause: boolean): string {
  const header = exception.value === "" ? exception.type : `${exception.type}: ${exception.value}`;
  const lines = [isCause ? `Caused by: ${header}` : header, ...visibleFrames(exception.frames, filter).map(frameLine)];
  return lines.join("\n");
}

/**
 * The stack trace as plain text, mirroring what `StackTrace` currently
 * renders (the in-app filter and the cause chain) rather than a JSON dump —
 * pasting a trace into a chat is the whole point of this button.
 */
export function stackTraceText(chain: readonly SymbolicatedException[], filter: FrameFilter): string {
  return chain.map((exception, index) => exceptionText(exception, filter, index > 0)).join("\n\n");
}
