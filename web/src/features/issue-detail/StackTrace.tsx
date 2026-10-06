import { Layers } from "lucide-react";
import { useMemo, useState } from "react";

import type { SymbolicatedException } from "@/api";
import { EmptyState } from "@/components/EmptyState";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { flattenCauses } from "@/stacktrace";

import { CopyButton } from "./CopyButton";
import { DetailSection } from "./DetailSection";
import { Frame } from "./Frame";
import { hiddenFrameCount, initialFrameFilter, stackTraceText, visibleFrames, type FrameFilter } from "./frames";

interface StackTraceProps {
  exception: SymbolicatedException;
}

const TOGGLE_ID = "stack-trace-in-app-only";

/**
 * The exception and its cause chain.
 *
 * The in-app filter is component state seeded from the trace, so mount this
 * with a key tied to the event: a different event deserves its own default
 * rather than inheriting the previous one.
 */
export function StackTrace({ exception }: StackTraceProps) {
  const chain = useMemo(() => flattenCauses(exception), [exception]);
  const [filter, setFilter] = useState<FrameFilter>(() => initialFrameFilter(chain));

  return (
    <DetailSection
      id="section-stack-trace"
      title="Stack trace"
      description={chain.length > 1 ? `${chain.length} exceptions in the cause chain` : undefined}
      action={
        <div className="flex items-center gap-2">
          <CopyButton value={stackTraceText(chain, filter)} label="Stack trace" />
          <Checkbox
            id={TOGGLE_ID}
            checked={filter === "in-app"}
            onCheckedChange={(checked) => setFilter(checked === true ? "in-app" : "all")}
          />
          <Label htmlFor={TOGGLE_ID} className="text-muted-foreground text-xs font-normal">
            In-app frames only
          </Label>
        </div>
      }
    >
      <div className="space-y-5">
        {chain.map((current, index) => (
          <ExceptionFrames key={index} exception={current} isCause={index > 0} filter={filter} />
        ))}
      </div>
    </DetailSection>
  );
}

interface ExceptionFramesProps {
  exception: SymbolicatedException;
  isCause: boolean;
  filter: FrameFilter;
}

function ExceptionFrames({ exception, isCause, filter }: ExceptionFramesProps) {
  const visible = visibleFrames(exception.frames, filter);
  const hidden = hiddenFrameCount(exception.frames, filter);

  return (
    <section>
      <h3 className="text-sm break-words">
        {isCause && <span className="text-muted-foreground mr-1 font-normal">Caused by:</span>}
        <span className="font-mono font-medium">{exception.type}</span>
        {exception.value !== "" && <span className="text-muted-foreground">: {exception.value}</span>}
      </h3>

      {exception.frames.length === 0 ? (
        <EmptyState
          className="py-8"
          icon={Layers}
          title="No frames"
          description="This exception arrived without any stack frames. Send them from the SDK to get a symbolicated trace here."
        />
      ) : (
        <>
          <ul className="mt-2 overflow-hidden rounded-md border">
            {visible.map((frame, index) => (
              <Frame key={index} frame={frame} />
            ))}
          </ul>
          {hidden > 0 && (
            <p className="text-muted-foreground mt-1.5 text-xs">
              {hidden} vendor {hidden === 1 ? "frame" : "frames"} hidden
            </p>
          )}
        </>
      )}
    </section>
  );
}
