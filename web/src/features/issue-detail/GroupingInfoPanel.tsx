import { ChevronDown } from "lucide-react";

import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";

import { DetailSection } from "./DetailSection";

interface GroupingInfoPanelProps {
  fingerprint: string;
}

/**
 * Shows the issue's grouping key. This is deliberately not "why events
 * grouped here" — the grouping pipeline (internal/grouping) does not record
 * which rule, if any, produced the final hash, only the hash itself
 * (internal/grouping/evaluate.go). Claiming an explanation we don't have
 * would be worse than not showing this panel at all.
 *
 * This is issue-level data (issues.fingerprint), not per-event, so it is
 * rendered once from IssueDetailPage rather than inside the per-event panel
 * stack in EventPanels — repeating it per event would wrongly imply it can
 * vary across events in the same issue.
 */
export function GroupingInfoPanel({ fingerprint }: GroupingInfoPanelProps) {
  return (
    <Collapsible>
      <DetailSection
        title="Grouping information"
        action={
          <CollapsibleTrigger className="text-muted-foreground hover:text-foreground inline-flex items-center gap-1 text-xs">
            Details
            <ChevronDown className="size-3.5" aria-hidden="true" />
          </CollapsibleTrigger>
        }
      >
        <p className="text-muted-foreground text-xs">
          This issue's grouping key. Events sharing this key belong to this issue.
        </p>
        <CollapsibleContent className="mt-2">
          <code className="block rounded-md border px-2 py-1.5 font-mono text-xs break-all">{fingerprint}</code>
        </CollapsibleContent>
      </DetailSection>
    </Collapsible>
  );
}
