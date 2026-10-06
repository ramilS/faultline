import type { SymbolicatedFrame } from "@/api";
import { displayFunction, displayLocation, displayPosition } from "@/stacktrace";
import { cn } from "@/lib/utils";

import { hasSourceLocation, isInAppFrame } from "./frames";
import { isHighlightable, tokenizeSourceLines, type SourceTokenKind } from "./sourceSyntax";

interface FrameProps {
  frame: SymbolicatedFrame;
}

/**
 * One stack frame.
 *
 * In-app and vendor frames are separated by weight, colour *and* a written
 * label — colour alone would leave the distinction invisible to a colour-blind
 * reader, and that distinction is the whole point of the panel.
 */
export function Frame({ frame }: FrameProps) {
  const inApp = isInAppFrame(frame);

  return (
    <li className={cn("border-t first:border-t-0", inApp ? "bg-card" : "bg-muted/20")}>
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1 px-3 py-2">
        <span
          className={cn(
            "font-mono text-xs break-all",
            inApp ? "text-frame-in-app font-medium" : "text-frame-vendor"
          )}
        >
          {displayFunction(frame)}
        </span>

        {hasSourceLocation(frame) && (
          <span className="text-muted-foreground font-mono text-xs break-all">{displayLocation(frame)}</span>
        )}

        {!inApp && (
          <span className="text-frame-vendor border-border ml-auto shrink-0 rounded border px-1 text-[10px] tracking-wide uppercase">
            vendor
          </span>
        )}
      </div>

      {frame.original !== null && (
        <p className="text-muted-foreground px-3 pb-2 font-mono text-[11px] break-all">
          minified {displayPosition(frame.generated)}
        </p>
      )}

      {frame.context.length > 0 && <SourceContext frame={frame} />}
    </li>
  );
}

/**
 * Tone and weight only, no hue: colour in this dashboard means severity or
 * status, so syntax is told apart the way a printed listing would be.
 */
const TOKEN_CLASSES: Record<SourceTokenKind, string> = {
  keyword: "font-semibold text-foreground",
  string: "text-foreground/75",
  comment: "text-muted-foreground italic",
  plain: ""
};

function SourceContext({ frame }: FrameProps) {
  const filename = (frame.original ?? frame.generated).filename;
  const highlighted = isHighlightable(filename) ? tokenizeSourceLines(frame.context.map((line) => line.text)) : null;

  return (
    <div className="border-t">
      {/* Lines are spans, not divs, because a <pre> holds phrasing content only.
          `min-w-max` stretches every line to the longest one, so the focused
          line's highlight still reaches the edge after the block scrolls. */}
      <pre className="overflow-x-auto py-1 font-mono text-xs leading-5">
        <code className="block min-w-max">
          {frame.context.map((line, index) => (
            <span
              key={line.line}
              className={cn("flex gap-3 px-3", line.focus && "bg-frame-focus-surface text-foreground font-medium")}
            >
              <span className="text-muted-foreground w-10 shrink-0 text-right tabular-nums select-none">
                {line.line}
              </span>
              <span className="whitespace-pre">
                {highlighted === null
                  ? line.text
                  : highlighted[index].map((token, tokenIndex) => (
                      <span key={tokenIndex} className={TOKEN_CLASSES[token.kind]}>
                        {token.text}
                      </span>
                    ))}
              </span>
            </span>
          ))}
        </code>
      </pre>
    </div>
  );
}
