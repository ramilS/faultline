import { ChevronRight, Layers } from "lucide-react";
import { useMemo, useState } from "react";

import type { SymbolicatedException } from "@/api";
import { EmptyState } from "@/components/EmptyState";
import { cn } from "@/lib/utils";
import { flattenCauses } from "@/stacktrace";

import { CopyButton } from "./CopyButton";
import { DetailSection } from "./DetailSection";
import { Frame } from "./Frame";
import {
  defaultExpandedFrame,
  frameRows,
  initialFrameFilter,
  stackTraceText,
  type FrameFilter,
  type FrameRow
} from "./frames";

interface StackTraceProps {
  exception: SymbolicatedException;
}

/** The structured trace under a frame filter, or the plain text a reader would paste. */
type StackTraceView = FrameFilter | "raw";

const VIEWS: ReadonlyArray<{ value: StackTraceView; label: string }> = [
  { value: "in-app", label: "In-app" },
  { value: "all", label: "Full" },
  { value: "raw", label: "Raw" }
];

function frameKey(exceptionIndex: number, frameIndex: number): string {
  return `${exceptionIndex}:${frameIndex}`;
}

function initiallyExpanded(chain: readonly SymbolicatedException[]): ReadonlySet<string> {
  const keys = new Set<string>();
  chain.forEach((exception, exceptionIndex) => {
    const index = defaultExpandedFrame(exception.frames);
    if (index !== null) {
      keys.add(frameKey(exceptionIndex, index));
    }
  });
  return keys;
}

function toggled(keys: ReadonlySet<string>, key: string): ReadonlySet<string> {
  const next = new Set(keys);
  if (!next.delete(key)) {
    next.add(key);
  }
  return next;
}

/**
 * The exception and its cause chain.
 *
 * The view and the open frames are component state seeded from the trace, so
 * mount this with a key tied to the event: a different event deserves its own
 * defaults rather than inheriting the previous one's.
 */
export function StackTrace({ exception }: StackTraceProps) {
  const chain = useMemo(() => flattenCauses(exception), [exception]);
  const [view, setView] = useState<StackTraceView>(() => initialFrameFilter(chain));
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => initiallyExpanded(chain));
  const [openRuns, setOpenRuns] = useState<ReadonlySet<string>>(() => new Set());
  const copyFilter: FrameFilter = view === "in-app" ? "in-app" : "all";

  return (
    <DetailSection
      id="section-stack-trace"
      title="Stack trace"
      description={chain.length > 1 ? `${chain.length} exceptions in the cause chain` : "Most recent call first"}
      action={
        <div className="flex items-center gap-2">
          <div role="group" aria-label="Stack trace view" className="flex rounded-md border p-0.5">
            {VIEWS.map((option) => (
              <button
                key={option.value}
                type="button"
                aria-pressed={view === option.value}
                onClick={() => setView(option.value)}
                className={cn(
                  "rounded-sm px-2 py-0.5 text-xs transition-colors",
                  view === option.value ? "bg-muted text-foreground font-medium" : "text-muted-foreground hover:text-foreground"
                )}
              >
                {option.label}
              </button>
            ))}
          </div>
          <CopyButton value={stackTraceText(chain, copyFilter)} label="Stack trace" />
        </div>
      }
    >
      {view === "raw" ? (
        <pre className="bg-muted/30 overflow-x-auto rounded-md border p-3 font-mono text-xs leading-5">
          {stackTraceText(chain, "all")}
        </pre>
      ) : (
        <div className="space-y-6">
          {chain.map((current, exceptionIndex) => (
            <ExceptionFrames
              key={exceptionIndex}
              exception={current}
              isCause={exceptionIndex > 0}
              rows={frameRows(current.frames, view)}
              isExpanded={(frameIndex) => expanded.has(frameKey(exceptionIndex, frameIndex))}
              onToggleFrame={(frameIndex) => setExpanded((keys) => toggled(keys, frameKey(exceptionIndex, frameIndex)))}
              isRunOpen={(start) => openRuns.has(frameKey(exceptionIndex, start))}
              onToggleRun={(start) => setOpenRuns((keys) => toggled(keys, frameKey(exceptionIndex, start)))}
            />
          ))}
        </div>
      )}
    </DetailSection>
  );
}

interface ExceptionFramesProps {
  exception: SymbolicatedException;
  isCause: boolean;
  rows: readonly FrameRow[];
  isExpanded: (frameIndex: number) => boolean;
  onToggleFrame: (frameIndex: number) => void;
  isRunOpen: (start: number) => boolean;
  onToggleRun: (start: number) => void;
}

function ExceptionFrames({ exception, isCause, rows, isExpanded, onToggleFrame, isRunOpen, onToggleRun }: ExceptionFramesProps) {
  return (
    <section>
      {isCause && <p className="text-muted-foreground mb-0.5 text-xs">Caused by</p>}
      <h3 className="font-mono text-sm font-semibold break-words">{exception.type}</h3>
      {exception.value !== "" && <p className="text-foreground/80 mt-0.5 text-sm break-words">{exception.value}</p>}

      {exception.frames.length === 0 ? (
        <EmptyState
          className="py-8"
          icon={Layers}
          title="No frames"
          description="This exception arrived without any stack frames. Send them from the SDK to get a symbolicated trace here."
        />
      ) : (
        <ul className="mt-3 overflow-hidden rounded-md border">
          {rows.map((row) =>
            row.kind === "frame" ? (
              <Frame
                key={row.index}
                frame={row.frame}
                expanded={isExpanded(row.index)}
                onToggle={() => onToggleFrame(row.index)}
              />
            ) : (
              <VendorRun
                key={`run-${row.start}`}
                row={row}
                open={isRunOpen(row.start)}
                onToggle={() => onToggleRun(row.start)}
                isExpanded={isExpanded}
                onToggleFrame={onToggleFrame}
              />
            )
          )}
        </ul>
      )}
    </section>
  );
}

interface VendorRunProps {
  row: Extract<FrameRow, { kind: "vendor-run" }>;
  open: boolean;
  onToggle: () => void;
  isExpanded: (frameIndex: number) => boolean;
  onToggleFrame: (frameIndex: number) => void;
}

/** A folded run of library frames, opened in place rather than by switching the whole trace to Full. */
function VendorRun({ row, open, onToggle, isExpanded, onToggleFrame }: VendorRunProps) {
  const count = row.frames.length;

  return (
    <>
      <li className="bg-muted/20 border-t first:border-t-0">
        <button
          type="button"
          aria-expanded={open}
          onClick={onToggle}
          className="text-muted-foreground hover:text-foreground flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs transition-colors"
        >
          <ChevronRight className={cn("size-3 shrink-0 transition-transform", open && "rotate-90")} aria-hidden="true" />
          {open ? "Hide" : "Show"} {count} vendor {count === 1 ? "frame" : "frames"}
        </button>
      </li>
      {open &&
        row.frames.map((frame, offset) => (
          <Frame
            key={row.start + offset}
            frame={frame}
            expanded={isExpanded(row.start + offset)}
            onToggle={() => onToggleFrame(row.start + offset)}
          />
        ))}
    </>
  );
}
