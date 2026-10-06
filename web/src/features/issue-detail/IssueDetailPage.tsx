import { useCallback, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";

import { isMergedIssue, type EventSummary } from "@/api";
import { NotFoundPage } from "@/app/NotFoundPage";
import { ErrorState } from "@/components/ErrorState";
import { Skeleton } from "@/components/ui/skeleton";
import { api } from "@/apiClient";
import type { TimeRangeValue } from "@/features/issues/TimeRangePicker";
import { ALL_TIME_WINDOW } from "@/lib/issueFilters";

import { EventFilterBar } from "./EventFilterBar";
import { EventNavigator } from "./EventNavigator";
import { EventPanels } from "./EventPanels";
import { resolveTimeRange } from "./eventWindow";
import { GroupingInfoPanel } from "./GroupingInfoPanel";
import { IssueActions } from "./IssueActions";
import { IssueHeader } from "./IssueHeader";
import { IssueStatsChart } from "./IssueStatsChart";
import { issueIdFromParam } from "./issueIdParam";
import { MergedIssueNotice } from "./MergedIssueNotice";
import { NotAnIssue } from "./NotAnIssue";
import { selectedEventFromParams } from "./selectedEvent";
import { TagFacets } from "./TagFacets";
import { UsersFacet } from "./UsersFacet";
import { useAsyncResource } from "@/hooks/useAsyncResource";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

export function IssueDetailPage() {
  const { slug, issueId } = useParams<{ slug: string; issueId: string }>();
  const numericId = issueIdFromParam(issueId);

  if (slug === undefined || numericId === null) {
    return <NotAnIssue slug={slug} issueId={issueId} />;
  }

  // Keyed so that moving between issues remounts every resource, walk and
  // selection below rather than briefly showing one issue's data under
  // another's header.
  return <IssueDetail key={numericId} slug={slug} issueId={numericId} />;
}

interface IssueDetailProps {
  slug: string;
  issueId: number;
}

function IssueDetail({ slug, issueId }: IssueDetailProps) {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const initialEventId = selectedEventFromParams(searchParams);
  const [selectedEvent, setSelectedEvent] = useState<EventSummary | null>(null);
  const [environment, setEnvironment] = useState("");
  // What the picker displays — a relative choice stays relative here, so its
  // label keeps reading "Last 24 hours" rather than the dates it resolves to.
  const [timeRange, setTimeRange] = useState<TimeRangeValue>(ALL_TIME_WINDOW);
  // What the pager actually queries: `timeRange` frozen into absolute bounds
  // at the moment it was picked. See eventWindow.ts's resolveTimeRange for
  // why the pager must not re-resolve a relative period on its own.
  const [eventWindow, setEventWindow] = useState<TimeRangeValue>(ALL_TIME_WINDOW);

  const handleTimeRangeChange = useCallback((next: TimeRangeValue) => {
    setTimeRange(next);
    setEventWindow(resolveTimeRange(next, new Date()));
  }, []);

  const loadIssue = useCallback(() => api.getIssue(issueId), [issueId]);
  const issue = useAsyncResource(loadIssue);
  useDocumentTitle(
    issue.state.status === "ready" && !isMergedIssue(issue.state.value) ? issue.state.value.issue.title : null
  );

  if (issue.state.status === "loading") {
    return <IssueDetailSkeleton />;
  }

  if (issue.state.status === "failed") {
    // A 404 here means this issue's project is outside the caller's scope —
    // retrying will 404 forever, so this renders the not-found route rather
    // than ErrorState's Retry.
    if (issue.state.notFound) {
      return <NotFoundPage />;
    }
    return <ErrorState message={issue.state.message} onRetry={issue.reload} />;
  }

  const result = issue.state.value;
  if (isMergedIssue(result)) {
    return <MergedIssueNotice slug={slug} mergedIntoId={result.merged_into_id} />;
  }

  return (
    <>
      <IssueHeader
        issue={result.issue}
        slug={slug}
        latestMessage={selectedEvent?.message ?? null}
        actions={
          <IssueActions
            issue={result.issue}
            slug={slug}
            onChanged={issue.replace}
            onMerged={(targetId) => navigate(`/${slug}/issues/${targetId}`, { replace: true })}
          />
        }
      />

      <div className="grid min-w-0 gap-4 p-4 sm:p-6 xl:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
        <div className="min-w-0 space-y-4">
          <EventFilterBar
            environments={result.environments}
            environment={environment}
            onEnvironmentChange={setEnvironment}
            timeRange={timeRange}
            onTimeRangeChange={handleTimeRangeChange}
          />
          <EventNavigator
            issueId={issueId}
            initialEventId={initialEventId}
            onSelect={setSelectedEvent}
            environment={environment}
            timeRange={eventWindow}
          />
          <EventPanels selectedEvent={selectedEvent} />
        </div>

        <div className="min-w-0 space-y-4">
          <IssueStatsChart slug={slug} issueId={issueId} />
          <UsersFacet issueId={issueId} />
          <TagFacets slug={slug} detail={result} />
          <GroupingInfoPanel fingerprint={result.issue.fingerprint} />
        </div>
      </div>
    </>
  );
}

function IssueDetailSkeleton() {
  return (
    <div className="space-y-4 p-4 sm:p-6">
      <Skeleton className="h-6 w-2/3" />
      <Skeleton className="h-4 w-1/3" />
      <Skeleton className="h-9 w-80" />
      <Skeleton className="h-48 w-full" />
    </div>
  );
}
