import { BellOff, CircleCheck, CircleDot, GitMerge } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import type { IssueChanges, IssueDetail, IssueStatus, IssueSummary } from "@/api";
import { api } from "@/apiClient";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { messageFor } from "@/errors";
import { useSession } from "@/features/auth/SessionProvider";

import { AssigneeMenu } from "./AssigneeMenu";
import { CopyButton } from "./CopyButton";
import { MergeDialog } from "./MergeDialog";

const STATUS_ACTIONS: ReadonlyArray<{ status: IssueStatus; label: string; icon: typeof CircleDot }> = [
  { status: "resolved", label: "Resolve", icon: CircleCheck },
  { status: "ignored", label: "Ignore", icon: BellOff },
  { status: "unresolved", label: "Reopen", icon: CircleDot }
];

interface IssueActionsProps {
  issue: IssueSummary;
  slug: string;
  /** Receives the server's updated issue so the page never guesses the result. */
  onChanged: (detail: IssueDetail) => void;
  onMerged: (targetId: number) => void;
}

export function IssueActions({ issue, slug, onChanged, onMerged }: IssueActionsProps) {
  const { session } = useSession();
  const [pending, setPending] = useState(false);
  const [mergeOpen, setMergeOpen] = useState(false);

  // The page renders inside RequireSession, so the signed-in user is already
  // resolved; re-probing /auth/me here would be a redundant request.
  const currentUserId = session.state === "authenticated" ? session.user.id : null;

  const apply = async (changes: IssueChanges, successMessage: string) => {
    setPending(true);
    try {
      onChanged(await api.patchIssue(issue.id, changes));
      toast.success(successMessage);
    } catch (caught) {
      toast.error(messageFor(caught));
    } finally {
      setPending(false);
    }
  };

  const assign = (assigneeId: number | null) => {
    const message =
      assigneeId === null ? "Issue unassigned." : assigneeId === currentUserId ? "Issue assigned to you." : "Issue assigned.";
    void apply({ assignee_id: assigneeId }, message);
  };

  return (
    <div className="flex flex-wrap items-center gap-2">
      {STATUS_ACTIONS.map((action) => (
        <Button
          key={action.status}
          variant="outline"
          size="sm"
          disabled={pending || issue.status === action.status}
          onClick={() => void apply({ status: action.status }, `Issue marked ${action.status}.`)}
        >
          <action.icon aria-hidden="true" />
          {action.label}
        </Button>
      ))}

      <Separator orientation="vertical" className="data-[orientation=vertical]:h-6 max-sm:hidden" />

      <AssigneeMenu
        slug={slug}
        assignee={issue.assignee}
        currentUserId={currentUserId}
        disabled={pending}
        onAssign={assign}
      />

      <Separator orientation="vertical" className="data-[orientation=vertical]:h-6 max-sm:hidden" />

      <Button variant="outline" size="sm" disabled={pending} onClick={() => setMergeOpen(true)}>
        <GitMerge aria-hidden="true" />
        Merge into…
      </Button>

      <Separator orientation="vertical" className="data-[orientation=vertical]:h-6 max-sm:hidden" />

      {/*
        `value` is a function rather than the URL string itself: this bar
        stays mounted while EventNavigator steps through events and rewrites
        the `event` query param, so reading `window.location.href` now
        (render time) would go stale the moment the user moves on. The
        function defers that read to the click itself.
      */}
      <CopyButton value={() => window.location.href} label="Issue link" variant="outline" size="sm">
        Share
      </CopyButton>

      <MergeDialog
        slug={slug}
        sourceIssueId={issue.id}
        sourceTimesSeen={issue.times_seen}
        open={mergeOpen}
        onOpenChange={setMergeOpen}
        onMerged={(targetId) => {
          setMergeOpen(false);
          onMerged(targetId);
        }}
      />
    </div>
  );
}
