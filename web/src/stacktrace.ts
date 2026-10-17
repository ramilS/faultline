import type { SymbolicatedException, SymbolicatedFrame, SymbolicatedPosition } from "./api.ts";

/** `file:line:column`, or `file:line` when the frame has no column (a JVM frame). */
export function displayPosition(position: SymbolicatedPosition): string {
  const location = `${position.filename}:${position.line}`;
  return position.column > 0 ? `${location}:${position.column}` : location;
}

/** Human-readable location, preferring the original source location. */
export function displayLocation(frame: SymbolicatedFrame): string {
  return displayPosition(frame.original ?? frame.generated);
}

/** Function name, preferring a non-empty original name over the generated one. */
export function displayFunction(frame: SymbolicatedFrame): string {
  if (frame.original && frame.original.function.trim()) {
    return frame.original.function;
  }
  return frame.generated.function.trim() ? frame.generated.function : "<unknown>";
}

/** The exception and its transitive causes, in received (outermost-first) order. */
export function flattenCauses(exception: SymbolicatedException): SymbolicatedException[] {
  const chain: SymbolicatedException[] = [];
  let current: SymbolicatedException | null = exception;
  while (current) {
    chain.push(current);
    current = current.cause;
  }
  return chain;
}
