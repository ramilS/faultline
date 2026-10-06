import { GitMerge } from "lucide-react";
import { useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";

import { EmptyState } from "@/components/EmptyState";
import { Button } from "@/components/ui/button";

interface MergedIssueNoticeProps {
  slug: string;
  mergedIntoId: number;
}

/**
 * A merged issue is a tombstone, so this route has nothing to show and forwards
 * to the issue that absorbed it.
 *
 * `MergeIssue` flattens existing tombstones onto the new target
 * (`internal/storage/postgres/issues.go:437`), so `merged_into_id` always names
 * a live issue and the forward cannot chain or loop.
 */
export function MergedIssueNotice({ slug, mergedIntoId }: MergedIssueNoticeProps) {
  const navigate = useNavigate();
  const target = `/${slug}/issues/${mergedIntoId}`;

  useEffect(() => {
    toast.info(`That issue was merged into #${mergedIntoId}.`);
    navigate(target, { replace: true });
  }, [navigate, target, mergedIntoId]);

  return (
    <EmptyState
      icon={GitMerge}
      title="This issue was merged"
      description={`Its events now live on issue #${mergedIntoId}. Taking you there.`}
      action={
        <Button variant="outline" size="sm" onClick={() => navigate(target, { replace: true })}>
          Open issue #{mergedIntoId}
        </Button>
      }
    />
  );
}
