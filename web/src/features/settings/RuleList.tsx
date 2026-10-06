import type { GroupingRule, GroupingRuleMoveDirection } from "@/api";
import { STACKED_ROWS_BELOW_MD } from "@/components/stackedTable";
import { Table, TableBody, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useSession } from "@/features/auth/SessionProvider";
import { RuleRow } from "@/features/settings/RuleRow";

interface RuleListProps {
  rules: readonly GroupingRule[];
  onMove: (rule: GroupingRule, direction: GroupingRuleMoveDirection) => void;
  onEdit: (rule: GroupingRule) => void;
  onDelete: (rule: GroupingRule) => void;
}

export function RuleList({ rules, onMove, onEdit, onDelete }: RuleListProps) {
  const { session } = useSession();
  const isOwner = session.state === "authenticated" && session.user.role === "owner";

  return (
    <Table className={STACKED_ROWS_BELOW_MD}>
      <TableHeader>
        <TableRow>
          <TableHead className="w-0">
            <span title="Rules are evaluated in this order within their pass">Priority</span>
          </TableHead>
          <TableHead>Matcher</TableHead>
          <TableHead>Fingerprint template</TableHead>
          <TableHead className="w-0 text-right">
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rules.map((rule, index) => (
          <RuleRow
            key={rule.id}
            rule={rule}
            position={index + 1}
            isFirst={index === 0}
            isLast={index === rules.length - 1}
            canManage={isOwner}
            onMove={onMove}
            onEdit={onEdit}
            onDelete={onDelete}
          />
        ))}
      </TableBody>
    </Table>
  );
}
