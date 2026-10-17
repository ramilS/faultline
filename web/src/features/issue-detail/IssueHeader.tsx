import { ChevronRight, RotateCcw } from "lucide-react";
import { useLayoutEffect, useRef, type ReactNode } from "react";
import { Link } from "react-router-dom";

import type { IssueSummary, Regression } from "@/api";
import { LevelBadge } from "@/components/LevelBadge";
import { RelativeTime } from "@/components/RelativeTime";
import { Sparkline } from "@/components/Sparkline";
import { StatusBadge } from "@/components/StatusBadge";

import { STICKY_HEADER_HEIGHT_PROPERTY, stickyHeaderHeightValue } from "./stickyHeader";

interface IssueHeaderProps {
  issue: IssueSummary;
  slug: string;
  /** The current event's message, or null before the navigator's first page lands. */
  latestMessage: string | null;
  /** Why the issue is open again, when it is open because it came back. */
  regression: Regression | null;
  /** The action bar. Rendered inside the header band so it stays reachable. */
  actions: ReactNode;
}

/**
 * The page's own header rather than the shared `PageHeader`: an issue title is
 * frequently long, and truncating it at the top of its own detail page hides
 * the one thing the page is about.
 */
export function IssueHeader({ issue, slug, latestMessage, regression, actions }: IssueHeaderProps) {
  const trimmedMessage = latestMessage?.trim() ?? "";
  const hasDistinctMessage = trimmedMessage !== "" && trimmedMessage !== issue.title.trim();
  const banner = useRef<HTMLElement>(null);

  // Publishes this header's height so jump-to-section anchors can clear it.
  // Re-measured on resize because the height moves with the viewport width
  // and with the message line appearing once an event is selected. Only a
  // pinned header covers anything: below `lg` it scrolls away, and the shell's
  // own top bar is what anchors must clear, so nothing is published and the
  // fallback applies.
  useLayoutEffect(() => {
    const element = banner.current;
    if (element === null) {
      return;
    }
    const publish = () => {
      const pinned = getComputedStyle(element).position === "sticky";
      const measured = pinned ? stickyHeaderHeightValue(element.offsetHeight) : null;
      if (measured === null) {
        document.documentElement.style.removeProperty(STICKY_HEADER_HEIGHT_PROPERTY);
      } else {
        document.documentElement.style.setProperty(STICKY_HEADER_HEIGHT_PROPERTY, measured);
      }
    };
    publish();
    const observer = new ResizeObserver(publish);
    observer.observe(element);
    return () => {
      observer.disconnect();
      document.documentElement.style.removeProperty(STICKY_HEADER_HEIGHT_PROPERTY);
    };
  }, []);

  return (
    <header
      ref={banner}
      // Pinned only where it has the viewport to itself and can spare the
      // room: below `lg` the shell's top bar occupies top-0 at a higher
      // z-index and would cover it, and on a window shorter than 760px a
      // header this tall would leave too little to read the trace in.
      className="bg-background/80 z-10 border-b px-4 py-3 backdrop-blur sm:px-6 [@media(min-width:1024px)_and_(min-height:760px)]:sticky [@media(min-width:1024px)_and_(min-height:760px)]:top-0"
    >
      <nav aria-label="Breadcrumb" className="text-muted-foreground text-xs">
        <ol className="m-0 flex list-none items-center gap-1 p-0">
          <li className="flex items-center gap-1">
            <Link to="/projects" className="hover:text-foreground transition-colors">
              Projects
            </Link>
            <ChevronRight className="size-3" aria-hidden="true" />
          </li>
          <li className="flex items-center gap-1">
            <Link to={`/${slug}/issues`} className="hover:text-foreground transition-colors">
              {slug}
            </Link>
            <ChevronRight className="size-3" aria-hidden="true" />
          </li>
          <li>
            <span className="text-foreground font-mono" aria-current="page">
              #{issue.id}
            </span>
          </li>
        </ol>
      </nav>

      <div className="mt-2 flex flex-wrap items-start justify-between gap-x-6 gap-y-3">
        <div className="min-w-0 flex-1">
          <h1 className="text-lg font-semibold tracking-tight break-words">{issue.title}</h1>
          {hasDistinctMessage && (
            <p className="text-foreground/80 mt-0.5 line-clamp-2 text-sm break-words">{latestMessage}</p>
          )}
          {/* Below sm the actions must stay reachable above the fold, so a long
              culprit is clamped rather than left to run the header past the
              viewport — unlike the title, it is detail, not identity. */}
          <p className="text-muted-foreground mt-1 line-clamp-2 font-mono text-xs break-all sm:line-clamp-none">
            {issue.culprit}
          </p>
          <div className="text-muted-foreground mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
            <LevelBadge level={issue.level} />
            <StatusBadge status={issue.status} />
            {regression !== null && <RegressionBadge regression={regression} />}
            <span>
              First seen <RelativeTime iso={issue.first_seen} className="text-foreground" />
            </span>
            <span>
              Last seen <RelativeTime iso={issue.last_seen} className="text-foreground" />
            </span>
          </div>
        </div>

        <div className="flex shrink-0 items-end gap-5">
          <dl className="flex gap-5">
            <Stat label="Events" value={issue.times_seen} />
            <Stat label="Users" value={issue.users_seen} />
          </dl>
          {/* GET /api/v1/issues/{id} takes no window filters, so this is always
              the legacy, windowless shape — counts_24h is only optional on the
              type because IssueSummary is shared with the (sometimes windowed)
              list response. Hidden below sm: the chart panel a screen below
              renders the same 24h counts properly, so this 32px isn't worth the
              space on a phone. */}
          <Sparkline counts={issue.counts_24h ?? []} window="24 hours" className="hidden h-8 w-32 sm:block" />
        </div>
      </div>

      <div className="mt-3">{actions}</div>
    </header>
  );
}

/**
 * Whether this is an old problem back again, and since which release — the
 * first thing a reader arriving from a regression alert needs, because it
 * points at what that release changed.
 */
function RegressionBadge({ regression }: { regression: Regression }) {
  return (
    <span className="text-level-warning bg-level-warning-surface inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 font-medium">
      <RotateCcw className="size-3 shrink-0" aria-hidden="true" />
      {regression.release === null ? "Regressed" : `Regressed in ${regression.release}`}
      <RelativeTime iso={regression.at} className="font-normal" />
    </span>
  );
}

/** A lifetime count, large enough to read the scale of the problem at a glance. */
function Stat({ label, value }: { label: string; value: number }) {
  return (
    <div className="text-right">
      <dt className="text-muted-foreground text-xs">{label}</dt>
      <dd className="text-lg leading-tight font-semibold tabular-nums">{value.toLocaleString()}</dd>
    </div>
  );
}
