import { ArrowDown, ArrowUp, Pencil, Trash2 } from "lucide-react";

import type { GroupingRule, GroupingRuleMoveDirection } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { TableCell, TableRow } from "@/components/ui/table";
import { canEditGroupingRule, isMergeAliasRule } from "@/groupingMatcher";

const SYSTEM_RULE_EXPLANATION =
  "Created automatically by an issue merge. Editing or deleting it can permanently strand later events from the merged issue's original signal.";

interface RuleRowProps {
  rule: GroupingRule;
  /** 1-based, matching the priority the admin sees and the move labels. */
  position: number;
  isFirst: boolean;
  isLast: boolean;
  /** A member sees this list read-only; the page header already explains why. */
  canManage: boolean;
  onMove: (rule: GroupingRule, direction: GroupingRuleMoveDirection) => void;
  onEdit: (rule: GroupingRule) => void;
  onDelete: (rule: GroupingRule) => void;
}

export function RuleRow({ rule, position, isFirst, isLast, canManage, onMove, onEdit, onDelete }: RuleRowProps) {
  const isSystemManaged = isMergeAliasRule(rule.template);

  return (
    <TableRow>
      <TableCell className="text-muted-foreground w-0 tabular-nums">{position}</TableCell>

      <TableCell className="max-w-0">
        <code className="block truncate font-mono text-xs" title={rule.matcher}>
          {rule.matcher}
        </code>
      </TableCell>

      <TableCell className="max-w-0">
        <div className="flex items-center gap-2">
          <code className="min-w-0 truncate font-mono text-xs" title={rule.template}>
            {rule.template}
          </code>
          {isSystemManaged && (
            <Badge variant="outline" title={SYSTEM_RULE_EXPLANATION}>
              System · merge alias
            </Badge>
          )}
        </div>
      </TableCell>

      <TableCell className="w-0">
        {/* A member cannot reorder, edit, or delete at all: the page header
            already explains why, so the cell is empty rather than offering
            disabled controls. */}
        {canManage && (
          <div className="flex items-center justify-end gap-1">
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label={`Move rule ${position} up`}
              disabled={isFirst}
              onClick={() => onMove(rule, "up")}
            >
              <ArrowUp aria-hidden="true" />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label={`Move rule ${position} down`}
              disabled={isLast}
              onClick={() => onMove(rule, "down")}
            >
              <ArrowDown aria-hidden="true" />
            </Button>

            {/* A merge alias offers neither control: canEditGroupingRule is the
                single source of truth for edit, and the same rule is load-bearing
                for that merge's future routing, so it is not deletable here
                either. */}
            {canEditGroupingRule(rule.template) && (
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label={`Edit rule ${position}`}
                onClick={() => onEdit(rule)}
              >
                <Pencil aria-hidden="true" />
              </Button>
            )}
            {!isSystemManaged && (
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                className="text-destructive hover:text-destructive"
                aria-label={`Delete rule ${position}`}
                onClick={() => onDelete(rule)}
              >
                <Trash2 aria-hidden="true" />
              </Button>
            )}
          </div>
        )}
      </TableCell>
    </TableRow>
  );
}
