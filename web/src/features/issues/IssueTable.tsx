import { ArrowDown } from "lucide-react";

import type { IssueSort, IssueSummary } from "@/api";
import { Checkbox } from "@/components/ui/checkbox";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { IssueRow } from "@/features/issues/IssueRow";
import { cn } from "@/lib/utils";

export interface IssueTableProps {
  issues: readonly IssueSummary[];
  slug: string;
  selectedIds: ReadonlySet<number>;
  /** The sparkline column's active window, e.g. "14d" or "Range" — see windowColumnLabel. */
  windowLabel: string;
  sort: IssueSort;
  onSortChange: (sort: IssueSort) => void;
  onSelectedChange: (id: number, selected: boolean) => void;
  onSelectAllChange: (selected: boolean) => void;
}

export function IssueTable({
  issues,
  slug,
  selectedIds,
  windowLabel,
  sort,
  onSortChange,
  onSelectedChange,
  onSelectAllChange
}: IssueTableProps) {
  const selectedCount = issues.filter((issue) => selectedIds.has(issue.id)).length;
  const allChecked: boolean | "indeterminate" =
    selectedCount === 0 ? false : selectedCount === issues.length ? true : "indeterminate";

  return (
    <Table>
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          <TableHead className="w-10 pl-4 sm:pl-6">
            <Checkbox
              checked={allChecked}
              onCheckedChange={(value) => onSelectAllChange(value === true)}
              aria-label="Select all loaded issues"
            />
          </TableHead>
          <TableHead className="text-muted-foreground w-24 text-xs font-medium max-sm:hidden">Level</TableHead>
          <TableHead className="text-muted-foreground text-xs font-medium">Issue</TableHead>
          <TableHead className="text-muted-foreground w-28 text-xs font-medium max-md:hidden">{windowLabel}</TableHead>
          <SortableHead label="Events" sortValue="times_seen" activeSort={sort} onSortChange={onSortChange} />
          <SortableHead
            label="Users"
            sortValue="users_seen"
            activeSort={sort}
            onSortChange={onSortChange}
            className="max-md:hidden"
          />
          <SortableHead
            label="Last seen"
            sortValue="last_seen"
            activeSort={sort}
            onSortChange={onSortChange}
            className="max-sm:pr-4"
          />
          <TableHead className="text-muted-foreground w-36 text-xs font-medium max-lg:hidden">Assignee</TableHead>
          <TableHead className="text-muted-foreground w-28 pr-6 text-xs font-medium max-sm:hidden">Status</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {issues.map((issue) => (
          <IssueRow
            key={issue.id}
            issue={issue}
            slug={slug}
            selected={selectedIds.has(issue.id)}
            onSelectedChange={(selected) => onSelectedChange(issue.id, selected)}
          />
        ))}
      </TableBody>
    </Table>
  );
}

interface SortableHeadProps {
  label: string;
  sortValue: IssueSort;
  activeSort: IssueSort;
  onSortChange: (sort: IssueSort) => void;
  /** Responsive hiding class, e.g. "max-lg:hidden" — matched to the neighbouring column it hides alongside. */
  className?: string;
}

/**
 * The API sorts descending only — most recent, most frequent — so the active
 * column reports `descending` and the others report `none` rather than
 * offering a direction the server cannot honour.
 */
function SortableHead({ label, sortValue, activeSort, onSortChange, className }: SortableHeadProps) {
  const active = activeSort === sortValue;

  return (
    <TableHead aria-sort={active ? "descending" : "none"} className={cn("w-20", className)}>
      <button
        type="button"
        onClick={() => onSortChange(sortValue)}
        className={cn(
          // min-h-6 keeps the pointer target at 24px; the bare text height was 16.
          "hover:text-foreground flex min-h-6 w-full items-center justify-end gap-1 rounded-sm text-xs font-medium",
          active ? "text-foreground" : "text-muted-foreground"
        )}
      >
        {label}
        {/* Always rendered so the column keeps its width when sorting moves. */}
        <ArrowDown className={cn("size-3 shrink-0", active ? "opacity-100" : "opacity-0")} aria-hidden="true" />
      </button>
    </TableHead>
  );
}

const SKELETON_ROWS = [0, 1, 2, 3, 4, 5, 6, 7];

export function IssueTableSkeleton() {
  return (
    <div className="space-y-1 px-4 py-3 sm:px-6" role="status" aria-label="Loading issues">
      {SKELETON_ROWS.map((row) => (
        <Skeleton key={row} className="h-11 w-full" />
      ))}
    </div>
  );
}
