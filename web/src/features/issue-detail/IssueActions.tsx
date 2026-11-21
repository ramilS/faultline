import { Bot, CircleCheck, CircleDot, GitMerge, Link2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import type { IgnoreCondition, IssueChanges, IssueDetail, IssueStatus, IssueSummary } from "@/api";
import { api } from "@/apiClient";
import { IgnoreMenu } from "@/components/IgnoreMenu";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { messageFor } from "@/errors";
import { useSession } from "@/features/auth/SessionProvider";
import { copyLoadedText } from "@/lib/clipboard";

import { AssigneeMenu } from "./AssigneeMenu";
import { CopyButton } from "./CopyButton";
import { DigestLinkDialog } from "./DigestLinkDialog";
import { MergeDialog } from "./MergeDialog";

interface StatusAction {
  status: IssueStatus;
  label: string;
  icon: typeof CircleDot;
}

const RESOLVE: StatusAction = { status: "resolved", label: "Resolve", icon: CircleCheck };
const REOPEN: StatusAction = { status: "unresolved", label: "Reopen", icon: CircleDot };

interface IssueActionsProps {
  issue: IssueSummary;
  slug: string;
  selectedEventId: string | null;
  /** Receives the server's updated issue so the page never guesses the result. */
  onChanged: (detail: IssueDetail) => void;
  onMerged: (targetId: number) => void;
}

export function IssueActions({ issue, slug, selectedEventId, onChanged, onMerged }: IssueActionsProps) {
  const { session } = useSession();
  const [pending, setPending] = useState(false);
  const [mergeOpen, setMergeOpen] = useState(false);
  const [linkOpen, setLinkOpen] = useState(false);

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

  const ignore = (condition: IgnoreCondition | null) => {
    void apply(condition === null ? { status: "ignored" } : { status: "ignored", ignore_until: condition }, "Issue ignored.");
  };

  const statusButton = (action: StatusAction) => (
    <Button
      variant="outline"
      size="sm"
      disabled={pending}
      onClick={() => void apply({ status: action.status }, `Issue marked ${action.status}.`)}
    >
      <action.icon aria-hidden="true" />
      {action.label}
    </Button>
  );

  return (
    <div className="flex flex-wrap items-center gap-2">
      {issue.status !== "resolved" && statusButton(RESOLVE)}
      <IgnoreMenu disabled={pending} onIgnore={ignore} />
      {issue.status !== "unresolved" && statusButton(REOPEN)}

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

      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => void copyLoadedText(() => api.getIssueDigest(issue.id, selectedEventId), "Issue digest for AI")}
      >
        <Bot aria-hidden="true" />
        For AI
      </Button>

      <Button type="button" variant="outline" size="sm" onClick={() => setLinkOpen(true)}>
        <Link2 aria-hidden="true" />
        Temporary link
      </Button>

      <DigestLinkDialog issueId={issue.id} open={linkOpen} onOpenChange={setLinkOpen} />

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
