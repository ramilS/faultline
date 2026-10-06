import type { MouseEvent } from "react";
import { Link, useNavigate } from "react-router-dom";

import type { IssueSummary } from "@/api";
import { LevelBadge } from "@/components/LevelBadge";
import { RelativeTime } from "@/components/RelativeTime";
import { Sparkline } from "@/components/Sparkline";
import { StatusBadge } from "@/components/StatusBadge";
import { Checkbox } from "@/components/ui/checkbox";
import { TableCell, TableRow } from "@/components/ui/table";
import { eventCount, eventCountTitle, sparklineCounts, sparklineWindow } from "@/lib/issueFilters";

export interface IssueRowProps {
  issue: IssueSummary;
  slug: string;
  selected: boolean;
  onSelectedChange: (selected: boolean) => void;
}

export function IssueRow({ issue, slug, selected, onSelectedChange }: IssueRowProps) {
  const navigate = useNavigate();
  const to = `/${slug}/issues/${issue.id}`;

  const openIssue = (event: MouseEvent<HTMLTableRowElement>) => {
    // The title link and the checkbox handle their own clicks; a row-level
    // navigation on top of them would fight the control the user aimed at.
    if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
      return;
    }
    if (event.target instanceof Element && event.target.closest("a, button, input, label") !== null) {
      return;
    }
    // Culprits are the thing people copy out of this table; navigating away on
    // the mouse-up that ends a text selection would make that impossible.
    const selection = document.getSelection();
    if (selection !== null && !selection.isCollapsed) {
      return;
    }
    void navigate(to);
  };

  return (
    <TableRow
      onClick={openIssue}
      data-state={selected ? "selected" : undefined}
      className="cursor-pointer [&>td]:py-1.5"
    >
      <TableCell className="w-10 pl-4 sm:pl-6">
        <Checkbox
          checked={selected}
          onCheckedChange={(value) => onSelectedChange(value === true)}
          aria-label={`Select ${issue.title}`}
        />
      </TableCell>

      <TableCell className="w-24 max-sm:hidden">
        <LevelBadge level={issue.level} />
      </TableCell>

      <TableCell className="w-full max-w-0 max-sm:whitespace-normal">
        <Link to={to} className="text-sm font-medium hover:underline max-sm:line-clamp-2 sm:block sm:truncate">
          {issue.title}
        </Link>
        <span className="text-muted-foreground block truncate font-mono text-xs">{issue.culprit}</span>
        {/* Narrow viewports drop the level and status columns so the title keeps
            usable width; the same information moves under it rather than away. */}
        <span className="mt-1 flex items-center gap-1.5 sm:hidden">
          <LevelBadge level={issue.level} />
          <StatusBadge status={issue.status} />
        </span>
      </TableCell>

      <TableCell className="w-28 max-md:hidden">
        <Sparkline counts={sparklineCounts(issue)} window={sparklineWindow(issue)} className="h-5 w-24" />
      </TableCell>

      <TableCell className="w-20 text-right text-sm" title={eventCountTitle(issue)}>
        {eventCount(issue).toLocaleString()}
      </TableCell>

      <TableCell className="w-20 text-right text-sm max-md:hidden">{issue.users_seen.toLocaleString()}</TableCell>

      <TableCell className="text-muted-foreground w-20 text-right text-sm max-sm:pr-4">
        <RelativeTime iso={issue.last_seen} />
      </TableCell>

      <TableCell className="w-36 max-lg:hidden">
        {issue.assignee === null ? (
          <>
            <span className="text-muted-foreground" aria-hidden="true">
              —
            </span>
            <span className="sr-only">Unassigned</span>
          </>
        ) : (
          <span className="block truncate text-sm" title={issue.assignee.email}>
            {issue.assignee.name}
          </span>
        )}
      </TableCell>

      <TableCell className="w-28 pr-6 max-sm:hidden">
        <StatusBadge status={issue.status} />
      </TableCell>
    </TableRow>
  );
}
