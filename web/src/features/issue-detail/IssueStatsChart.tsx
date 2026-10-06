import { ChartColumn } from "lucide-react";
import { useCallback, useState } from "react";
import { Link } from "react-router-dom";

import type { StatsPeriod } from "@/api";
import { api } from "@/apiClient";
import { EmptyState } from "@/components/EmptyState";
import { ErrorState } from "@/components/ErrorState";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { absoluteTime, axisLabel } from "@/lib/time";

import { DetailSection } from "./DetailSection";
import { streamPathForBucket } from "./facetLinks";
import { CHART_HEIGHT, CHART_WIDTH, layoutStatsChart, type StatsChartLayout } from "./statsChart";
import { useAsyncResource } from "@/hooks/useAsyncResource";

const HOUR_MS = 60 * 60 * 1000;
const DAY_MS = 24 * HOUR_MS;

type BucketUnit = "hour" | "day";

const PERIODS: ReadonlyArray<{ value: StatsPeriod; label: string; window: BucketUnit; bucketWidthMs: number }> = [
  { value: "24h", label: "24h", window: "hour", bucketWidthMs: HOUR_MS },
  { value: "14d", label: "14d", window: "day", bucketWidthMs: DAY_MS },
  { value: "90d", label: "90d", window: "day", bucketWidthMs: DAY_MS }
];

interface IssueStatsChartProps {
  slug: string;
  issueId: number;
}

export function IssueStatsChart({ slug, issueId }: IssueStatsChartProps) {
  const [period, setPeriod] = useState<StatsPeriod>("24h");
  const load = useCallback(() => api.getIssueStats(issueId, period), [issueId, period]);
  const { state, reload } = useAsyncResource(load);
  const selectedPeriod = PERIODS.find((entry) => entry.value === period) ?? PERIODS[0];

  return (
    <DetailSection
      title="Events over time"
      action={
        // Buttons rather than Radix Tabs: these switch the data behind one
        // chart, and a tablist that owns no tabpanel misreports the structure
        // to a screen reader.
        <div className="flex items-center gap-1" role="group" aria-label="Stats period">
          {PERIODS.map((entry) => (
            <Button
              key={entry.value}
              variant={entry.value === period ? "secondary" : "ghost"}
              size="xs"
              aria-pressed={entry.value === period}
              onClick={() => setPeriod(entry.value)}
            >
              {entry.label}
            </Button>
          ))}
        </div>
      }
    >
      {state.status === "loading" && <Skeleton className="h-32 w-full" />}

      {state.status === "failed" && <ErrorState className="py-8" message={state.message} onRetry={reload} />}

      {state.status === "ready" && state.value.length === 0 && (
        <EmptyState
          className="py-8"
          icon={ChartColumn}
          title="No data"
          description="The server returned no buckets for this window."
        />
      )}

      {state.status === "ready" && state.value.length > 0 && (
        <BarChart
          slug={slug}
          layout={layoutStatsChart(state.value, selectedPeriod.bucketWidthMs)}
          bucketWindow={selectedPeriod.window}
        />
      )}
    </DetailSection>
  );
}

interface BarChartProps {
  slug: string;
  layout: StatsChartLayout;
  /** Named unit for one bar, e.g. "hour" — used in the tooltip and summary. */
  bucketWindow: BucketUnit;
}

/**
 * Hand-rolled SVG rather than a charting dependency: two chart shapes across
 * the whole dashboard would not repay the bundle or the API surface.
 */
function BarChart({ slug, layout, bucketWindow }: BarChartProps) {
  return (
    <div>
      <svg
        viewBox={`0 0 ${CHART_WIDTH} ${CHART_HEIGHT}`}
        preserveAspectRatio="none"
        className="text-foreground/70 h-32 w-full"
        // `role="group"`, not `role="img"`: an `img` role is exposed to
        // assistive tech as one flat graphic, so its interactive descendants
        // (the per-bar links below) would not reliably reach the
        // accessibility tree — the exact bug this task's links exist to fix.
        // `group` keeps this label as the region's accessible name while
        // still exposing each child link's own `aria-label`.
        role="group"
        aria-label={`${layout.total} events across ${layout.bars.length} ${bucketWindow} buckets, peaking at ${layout.peak}`}
      >
        <rect x={0} y={CHART_HEIGHT - 1.5} width={CHART_WIDTH} height={1.5} fill="currentColor" opacity={0.25} />
        {layout.bars.map((bar) => (
          <Link
            key={bar.bucket}
            to={streamPathForBucket(slug, bar.bucket, bar.bucketEnd)}
            aria-label={`${absoluteTime(bar.bucket)} — ${bar.count} events`}
          >
            <rect x={bar.x} y={bar.y} width={bar.width} height={bar.height} fill="currentColor">
              <title>{`${absoluteTime(bar.bucket)} — ${bar.count}`}</title>
            </rect>
          </Link>
        ))}
      </svg>

      {/* The bars carry no scale of their own, so the span they cover is named
          at both ends. Per-bar values stay in each bar's tooltip and link. */}
      <div className="text-muted-foreground mt-1 flex justify-between text-[11px] tabular-nums" aria-hidden="true">
        <span>{axisLabel(layout.bars[0].bucket, bucketWindow)}</span>
        <span>{axisLabel(layout.bars[layout.bars.length - 1].bucket, bucketWindow)}</span>
      </div>

      <p className="text-muted-foreground mt-2 text-xs tabular-nums">
        {layout.total} events · peak {layout.peak} per {bucketWindow}
      </p>
    </div>
  );
}
