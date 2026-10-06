import { ChevronDown, Info } from "lucide-react";
import { useState } from "react";

import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { cn } from "@/lib/utils";

/**
 * The evaluation order is the one thing an admin must understand before
 * reordering anything, so it stays on the page rather than in external docs.
 */
export function GroupingRulesHelp() {
  const [open, setOpen] = useState(false);

  return (
    <Collapsible open={open} onOpenChange={setOpen} className="rounded-lg border">
      <CollapsibleTrigger className="hover:bg-accent/40 focus-visible:ring-ring/50 flex w-full items-center gap-2 rounded-lg px-4 py-3 text-left text-sm font-medium transition-colors focus-visible:ring-[3px] focus-visible:outline-none">
        <Info className="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
        How grouping rules are evaluated
        <ChevronDown
          className={cn("text-muted-foreground ml-auto size-4 shrink-0 transition-transform", open && "rotate-180")}
          aria-hidden="true"
        />
      </CollapsibleTrigger>

      <CollapsibleContent className="text-muted-foreground space-y-3 px-4 pt-1 pb-4 text-sm">
        <p>
          Every incoming event is matched in two passes: first the text-field rules (error type, message, logger,
          tags) top to bottom, then the fingerprint-alias rules — system-managed, created by issue merges — top to
          bottom against whatever the first pass produced.
        </p>
        <p>
          Within either pass the first matching rule wins and later rules in that same pass are never consulted.
          Reordering therefore only changes precedence <em>within</em> a pass, never between the two.
        </p>
        <p>
          A template is comma-separated literals and <code className="font-mono">{"{field}"}</code> substitutions:{" "}
          <code className="font-mono">{"db-timeout,{tags.service}"}</code> combines the literal{" "}
          <code className="font-mono">db-timeout</code> with the event&apos;s{" "}
          <code className="font-mono">service</code> tag, so one issue is opened per service.
        </p>
      </CollapsibleContent>
    </Collapsible>
  );
}
