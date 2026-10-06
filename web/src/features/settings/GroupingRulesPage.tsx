import { ListFilter, Plus } from "lucide-react";
import { useCallback, useState } from "react";
import { Navigate, useParams } from "react-router-dom";
import { toast } from "sonner";

import type { GroupingRule, GroupingRuleMoveDirection } from "@/api";
import { NotFoundPage } from "@/app/NotFoundPage";
import { api } from "@/apiClient";
import { EmptyState } from "@/components/EmptyState";
import { ErrorState } from "@/components/ErrorState";
import { PageHeader } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { messageFor } from "@/errors";
import { useSession } from "@/features/auth/SessionProvider";
import { DeleteRuleDialog } from "@/features/settings/DeleteRuleDialog";
import { GroupingRulesHelp } from "@/features/settings/GroupingRulesHelp";
import { RuleFormDialog } from "@/features/settings/RuleFormDialog";
import { RuleList } from "@/features/settings/RuleList";
import { useAsyncResource } from "@/hooks/useAsyncResource";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

export function GroupingRulesPage() {
  const { slug } = useParams<{ slug: string }>();
  // The route pattern cannot match without a slug; the guard keeps the type
  // honest instead of forcing a non-null assertion downstream.
  return slug === undefined ? <Navigate to="/projects" replace /> : <GroupingRulesScreen slug={slug} />;
}

interface GroupingRulesScreenProps {
  slug: string;
}

function GroupingRulesScreen({ slug }: GroupingRulesScreenProps) {
  const { session } = useSession();
  useDocumentTitle(`Grouping rules · ${slug}`);
  const isOwner = session.state === "authenticated" && session.user.role === "owner";
  const { state, reload } = useAsyncResource<GroupingRule[]>(
    useCallback(() => api.listGroupingRules(slug), [slug])
  );
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<GroupingRule | null>(null);
  const [deleting, setDeleting] = useState<GroupingRule | null>(null);

  const openCreateForm = () => {
    setEditing(null);
    setFormOpen(true);
  };

  const openEditForm = (rule: GroupingRule) => {
    setEditing(rule);
    setFormOpen(true);
  };

  const closeForm = () => {
    setFormOpen(false);
    setEditing(null);
  };

  const saveAndClose = async () => {
    closeForm();
    await reload({ silent: true });
  };

  // A reorder failure leaves the list as the server has it, so the message goes
  // to a toast rather than replacing the whole page with an error state.
  const move = async (rule: GroupingRule, direction: GroupingRuleMoveDirection) => {
    try {
      await api.moveGroupingRule(slug, rule.id, direction);
      await reload({ silent: true });
    } catch (caught) {
      toast.error(`Could not reorder the rule: ${messageFor(caught)}`);
    }
  };

  // A 404 here means this project is outside the caller's scope — the whole
  // screen is the wrong chrome for that, not just this one slot, so it
  // preempts the header and new-rule action rather than nesting inside them.
  if (state.status === "failed" && state.notFound) {
    return <NotFoundPage />;
  }

  return (
    <>
      <PageHeader
        title="Grouping rules"
        description={
          isOwner
            ? "Override automatic grouping so related events land in one issue instead of many."
            : "Override automatic grouping so related events land in one issue instead of many. Grouping rules are managed by an owner."
        }
      >
        {isOwner && (
          <Button size="sm" onClick={openCreateForm}>
            <Plus aria-hidden="true" />
            New rule
          </Button>
        )}
      </PageHeader>

      <div className="space-y-4 p-4 sm:p-6">
        <GroupingRulesHelp />

        {state.status === "loading" && (
          <div className="space-y-2">
            {[0, 1, 2].map((row) => (
              <Skeleton key={row} className="h-12 w-full" />
            ))}
          </div>
        )}

        {state.status === "failed" && <ErrorState message={state.message} onRetry={() => void reload()} />}

        {state.status === "ready" && state.value.length === 0 && (
          <EmptyState
            icon={ListFilter}
            title="No custom grouping rules yet"
            description={
              isOwner
                ? "Faultline groups events by their fingerprint, so one runtime error with a hundred message variants becomes a hundred issues. A rule collapses that: match a field — say logger billing — and every matching event is filed under one fingerprint you choose."
                : "Faultline groups events by their fingerprint, so one runtime error with a hundred message variants becomes a hundred issues. An owner needs to add a rule to collapse that."
            }
            action={
              isOwner ? (
                <Button size="sm" onClick={openCreateForm}>
                  <Plus aria-hidden="true" />
                  Add the first rule
                </Button>
              ) : undefined
            }
          />
        )}

        {state.status === "ready" && state.value.length > 0 && (
          <RuleList rules={state.value} onMove={move} onEdit={openEditForm} onDelete={setDeleting} />
        )}
      </div>

      <RuleFormDialog slug={slug} open={formOpen} rule={editing} onClose={closeForm} onSaved={saveAndClose} />

      <DeleteRuleDialog
        slug={slug}
        target={deleting}
        onClose={() => setDeleting(null)}
        onDeleted={reload}
      />
    </>
  );
}
