import { ChevronRight, ExternalLink } from "lucide-react";

import type { SymbolicatedFrame } from "@/api";
import { displayFunction, displayPosition } from "@/stacktrace";
import { cn } from "@/lib/utils";

import { FrameVariables } from "./FrameVariables";
import { frameLocation, hasFrameDetail, isInAppFrame, type FrameLocation } from "./frames";
import { isHighlightable, tokenizeSourceLines, type SourceTokenKind } from "./sourceSyntax";

interface FrameProps {
  frame: SymbolicatedFrame;
  /** Whether the source context and variables show. Ignored for a frame that carries neither. */
  expanded: boolean;
  onToggle: () => void;
  repositoryUrl: string | null;
  sourceStackRoot: string;
}

/**
 * One stack frame: a one-line summary that opens onto its source.
 *
 * In-app and vendor frames are separated by weight, colour *and* a written
 * label — colour alone would leave the distinction invisible to a colour-blind
 * reader, and that distinction is the whole point of the panel.
 */
export function Frame({ frame, expanded, onToggle, repositoryUrl, sourceStackRoot }: FrameProps) {
  const inApp = isInAppFrame(frame);
  const location = frameLocation(frame, sourceStackRoot);
  const hasContext = frame.context.length > 0;
  const hasDetail = hasFrameDetail(frame);
  const summary = (
    <>
      {hasDetail ? (
        <ChevronRight
          className={cn("text-muted-foreground size-3 shrink-0 self-center transition-transform", expanded && "rotate-90")}
          aria-hidden="true"
        />
      ) : (
        <span className="size-3 shrink-0" aria-hidden="true" />
      )}

      <span
        className={cn("font-mono text-xs break-all", inApp ? "text-frame-in-app font-medium" : "text-frame-vendor")}
      >
        {displayFunction(frame)}
      </span>

      {location !== null && <LocationLabel location={location} />}

      {!inApp && (
        <span className="text-frame-vendor border-border ml-auto shrink-0 rounded border px-1 text-[10px] tracking-wide uppercase">
          vendor
        </span>
      )}
    </>
  );
  const summaryClass = "flex w-full flex-wrap items-baseline gap-x-2 gap-y-1 px-3 py-2 text-left";

  return (
    <li className={cn("border-t first:border-t-0", inApp ? "bg-card" : "bg-muted/20")}>
      <div className="flex items-start">
        {hasDetail ? (
          <button type="button" aria-expanded={expanded} onClick={onToggle} className={cn(summaryClass, "hover:bg-muted/40")}>
            {summary}
          </button>
        ) : (
          <div className={summaryClass}>{summary}</div>
        )}
        {repositoryUrl !== null && (
          <a
            href={repositoryUrl}
            target="_blank"
            rel="noopener noreferrer"
            className="text-muted-foreground hover:text-foreground flex shrink-0 items-center gap-1 px-3 py-2 text-xs whitespace-nowrap"
          >
            <ExternalLink className="size-3" aria-hidden="true" />
            Open in repository
          </a>
        )}
      </div>

      {expanded && frame.original !== null && (
        <p className="text-muted-foreground px-3 pb-2 pl-8 font-mono text-[11px] break-all">
          minified {displayPosition(frame.generated)}
        </p>
      )}

      {expanded && hasContext && <SourceContext frame={frame} />}

      {expanded && frame.vars !== null && <FrameVariables vars={frame.vars} />}
    </li>
  );
}

/**
 * The directory is clipped from its start, so the end nearest the file stays
 * in view; `bdi` keeps the path itself left-to-right inside the RTL box that
 * moves the ellipsis to the left.
 */
function LocationLabel({ location }: { location: FrameLocation }) {
  return (
    <span title={location.full} className="flex max-w-full min-w-0 items-baseline font-mono text-xs">
      <span className="text-foreground shrink-0 font-normal whitespace-nowrap">
        {location.file}:{location.line}
      </span>
      {location.directory !== "" && (
        <span className="text-muted-foreground ml-2 min-w-0 truncate [direction:rtl]">
          <bdi>{location.directory}</bdi>
        </span>
      )}
    </span>
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

function SourceContext({ frame }: { frame: SymbolicatedFrame }) {
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
