import { ChevronRight } from "lucide-react";
import { useMemo, useState } from "react";

import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { JsonTree } from "@/components/JsonTree";
import { cn } from "@/lib/utils";

import { CopyButton } from "./CopyButton";
import { SECTION_SCROLL_MARGIN } from "./stickyHeader";

interface RawPayloadProps {
  payload: unknown;
}

/**
 * The envelope exactly as it was ingested, for when the parsed view has lost
 * something. Collapsed by default and scroll-contained, and shown as a tree
 * that folds: payloads run to thousands of lines and would otherwise own the
 * page. Copy still takes the whole thing, as text.
 */
export function RawPayload({ payload }: RawPayloadProps) {
  const [open, setOpen] = useState(false);

  // CollapsibleContent constructs its children whether or not it is open, so
  // an inline JSON.stringify here pretty-prints a payload near the ingest
  // size limit on every parent re-render of a panel that is collapsed by
  // default. The ternary skips stringifying entirely while closed, rather
  // than just caching the result after the first time it runs.
  const serialised = useMemo(() => (open ? JSON.stringify(payload, null, 2) : ""), [open, payload]);

  return (
    // The scroll margin tracks the issue header's measured height rather
    // than a literal: see stickyHeader.ts. Inline because the value is a
    // calc() over a runtime custom property, not one of Tailwind's steps.
    <Collapsible
      id="section-raw-payload"
      open={open}
      onOpenChange={setOpen}
      className="bg-card rounded-xl border"
      style={{ scrollMarginTop: SECTION_SCROLL_MARGIN }}
    >
      <div className="flex items-center gap-1 pr-3">
        <CollapsibleTrigger className="hover:bg-accent/40 flex min-w-0 flex-1 items-center gap-2 rounded-xl px-4 py-3 text-left text-sm font-semibold transition-colors">
          <ChevronRight
            className={cn("size-4 shrink-0 transition-transform", open && "rotate-90")}
            aria-hidden="true"
          />
          Raw event payload
        </CollapsibleTrigger>
        {open && <CopyButton value={serialised} label="Raw payload" />}
      </div>
      <CollapsibleContent>
        <div className="mx-4 mb-4 max-h-96 overflow-auto rounded-md border p-3">
          <JsonTree value={payload} />
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}
