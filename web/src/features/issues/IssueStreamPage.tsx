import { Bug, SearchX } from "lucide-react";
import { useMemo } from "react";
import { Link, useParams } from "react-router-dom";

import { NotFoundPage } from "@/app/NotFoundPage";
import { EmptyState } from "@/components/EmptyState";
import { ErrorState } from "@/components/ErrorState";
import { PageHeader } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import { BulkActionBar } from "@/features/issues/BulkActionBar";
import { IssueFilterBar } from "@/features/issues/IssueFilterBar";
import { IssueTable, IssueTableSkeleton } from "@/features/issues/IssueTable";
import { useBulkTriage } from "@/features/issues/useBulkTriage";
import { useIssueStream } from "@/features/issues/useIssueStream";
import { useUrlFilters } from "@/features/issues/useUrlFilters";
import { useProjects } from "@/features/projects/ProjectsProvider";
import { hasFiltersToClear, isFilteredEmptyState, streamKey, windowColumnLabel } from "@/lib/issueFilters";
import { hasMore } from "@/lib/issueStream";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

export function IssueStreamPage() {
  const { slug } = useParams<{ slug: string }>();

  if (slug === undefined) {
    return <ErrorState title="No project selected" message="This URL is missing a project slug." />;
  }

  return <IssueStreamView slug={slug} />;
}

/** Every project the caller can read, in one stream. */
export function AllIssuesPage() {
  return <IssueStreamView slug={null} />;
}

/** `slug` null is the every-project stream; otherwise one project's. */
function IssueStreamView({ slug }: { slug: string | null }) {
  useDocumentTitle(slug === null ? "All issues" : `Issues · ${slug}`);
  const controls = useUrlFilters();
  const { filters } = controls;
  const { state: projects } = useProjects();
  const projectNames = useMemo(
    () =>
      new Map(projects.status === "ready" ? projects.projects.map((project) => [project.slug, project.name]) : []),
    [projects]
  );
  const { phase, stream, loadingMore, loadMoreError, loadMore, reload, applyUpdates } = useIssueStream(
    slug,
    filters
  );
  const triage = useBulkTriage(streamKey(slug, filters), applyUpdates);

  // A 404 here means this project is outside the caller's scope — the whole
  // screen is the wrong chrome for that, not just this one slot, so it
  // preempts the header and filter bar rather than nesting inside them.
  if (phase.kind === "failed" && phase.notFound) {
    return <NotFoundPage />;
  }

  const issues = stream.issues;

  return (
    <>
      <PageHeader
        title={slug === null ? "All issues" : "Issues"}
        description={
          slug === null
            ? "Errors reported to every project you can access, grouped by cause."
            : `Errors reported to ${projectNames.get(slug) ?? slug}, grouped by cause.`
        }
      />

      <IssueFilterBar slug={slug} controls={controls} />

      {phase.kind === "loading" && <IssueTableSkeleton />}

      {phase.kind === "failed" && <ErrorState message={phase.message} onRetry={reload} />}

      {phase.kind === "ready" && issues.length === 0 && (
        <StreamEmptyState
          slug={slug}
          filtered={isFilteredEmptyState(filters)}
          canClearFilters={hasFiltersToClear(filters)}
          onClearFilters={controls.clear}
        />
      )}

      {phase.kind === "ready" && issues.length > 0 && (
        <>
          <IssueTable
            issues={issues}
            projectNames={slug === null ? projectNames : null}
            selectedIds={triage.selectedIds}
            windowLabel={windowColumnLabel(filters)}
            sort={filters.sort}
            onSortChange={controls.setSort}
            onSelectedChange={triage.setSelected}
            onSelectAllChange={(selected) =>
              triage.setAllSelected(
                issues.map((issue) => issue.id),
                selected
              )
            }
          />

          <div className="flex flex-wrap items-center justify-between gap-3 px-4 py-4 sm:px-6">
            <p className="text-muted-foreground text-xs">
              {issues.length} {issues.length === 1 ? "issue" : "issues"}
              {hasMore(stream) ? " loaded so far" : " — end of the stream"}
            </p>

            {hasMore(stream) && (
              <Button variant="outline" size="sm" onClick={loadMore} disabled={loadingMore}>
                {loadingMore ? "Loading…" : "Load more"}
              </Button>
            )}
          </div>

          {loadMoreError !== null && (
            <p role="alert" className="text-level-error px-4 pb-4 sm:px-6 text-sm">
              {loadMoreError}
            </p>
          )}
        </>
      )}

      <BulkActionBar
        selectedCount={triage.selectedIds.size}
        busy={triage.busy}
        error={triage.error}
        onResolve={() => triage.apply("resolved")}
        onIgnore={() => triage.apply("ignored")}
        onClear={triage.clear}
      />
    </>
  );
}

interface StreamEmptyStateProps {
  slug: string | null;
  filtered: boolean;
  canClearFilters: boolean;
  onClearFilters: () => void;
}

function StreamEmptyState({ slug, filtered, canClearFilters, onClearFilters }: StreamEmptyStateProps) {
  if (filtered) {
    return (
      <EmptyState
        icon={SearchX}
        title="No issues match these filters"
        description={
          slug === null
            ? "Nothing in your projects matches the current filters together."
            : "Nothing in this project matches the current filters together."
        }
        action={
          canClearFilters ? (
            <Button variant="outline" size="sm" onClick={onClearFilters}>
              Clear filters
            </Button>
          ) : undefined
        }
      />
    );
  }

  if (slug === null) {
    return (
      <EmptyState
        icon={Bug}
        title="No issues yet"
        description="Issues appear here as soon as any of your projects receives its first event."
        action={
          <Button asChild variant="outline" size="sm">
            <Link to="/projects">View projects</Link>
          </Button>
        }
      />
    );
  }

  return (
    <EmptyState
      icon={Bug}
      title="No issues yet"
      description="Issues appear here as soon as an SDK configured with this project's DSN reports its first event."
      action={
        <Button asChild variant="outline" size="sm">
          <Link to={`/${slug}/settings/keys`}>View ingest keys</Link>
        </Button>
      }
    />
  );
}
