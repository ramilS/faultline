import { Filter, Tags } from "lucide-react";
import { Link } from "react-router-dom";

import type { IssueDetail, TagFacet } from "@/api";
import { EmptyState } from "@/components/EmptyState";
import { RelativeTime } from "@/components/RelativeTime";

import { DetailSection } from "./DetailSection";
import { streamPathForEnvironment, streamPathForRelease, streamPathForTag } from "./facetLinks";

interface TagFacetsProps {
  slug: string;
  detail: IssueDetail;
}

/**
 * Where the issue happens: environments, releases, and the tag values ranked by
 * how often they appeared. This is the "is it everyone or just Safari" panel —
 * every value links to the stream filtered to it, all-time, so the hypothesis
 * it suggests is one click away from being checked.
 */
export function TagFacets({ slug, detail }: TagFacetsProps) {
  const isEmpty =
    detail.environments.length === 0 && detail.releases.length === 0 && detail.tags.length === 0;

  return (
    <DetailSection title="Tags" description="Aggregated across every event in this issue.">
      {isEmpty ? (
        <EmptyState
          className="py-8"
          icon={Tags}
          title="No tags"
          description="Events for this issue carried no environment, release, or tag values."
        />
      ) : (
        <div className="space-y-4">
          <ValueList
            label="Environment"
            values={detail.environments}
            hrefFor={(value) => streamPathForEnvironment(slug, value)}
          />
          <ValueList label="Release" values={detail.releases} hrefFor={(value) => streamPathForRelease(slug, value)} />
          {detail.tags.map((facet) => (
            <FacetRow key={facet.key} slug={slug} facet={facet} />
          ))}
        </div>
      )}
    </DetailSection>
  );
}

function ValueList({
  label,
  values,
  hrefFor
}: {
  label: string;
  values: readonly string[];
  hrefFor: (value: string) => string;
}) {
  if (values.length === 0) {
    return null;
  }

  return (
    <section>
      <h3 className="text-muted-foreground text-xs font-medium tracking-wide uppercase">{label}</h3>
      <ul className="mt-1.5 flex flex-wrap gap-1.5">
        {values.map((value) => (
          <li key={value}>
            <Link
              to={hrefFor(value)}
              className="border-border hover:bg-accent hover:border-foreground/30 inline-flex items-center gap-1 rounded-md border px-1.5 py-0.5 font-mono text-xs break-all transition-colors"
            >
              <Filter className="text-muted-foreground size-3 shrink-0" aria-hidden="true" />
              {value}
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}

function FacetRow({ slug, facet }: { slug: string; facet: TagFacet }) {
  const total = facet.values.reduce((sum, value) => sum + value.times_seen, 0);

  return (
    <section>
      <h3 className="text-muted-foreground text-xs font-medium tracking-wide uppercase">{facet.key}</h3>
      <ul className="mt-1.5 space-y-1.5">
        {facet.values.map((value) => {
          const share = total === 0 ? 0 : Math.round((value.times_seen / total) * 100);
          return (
            <li key={value.value}>
              <Link
                to={streamPathForTag(slug, facet.key, value.value)}
                className="hover:bg-accent -mx-1.5 grid grid-cols-[1fr_auto] items-baseline gap-x-3 rounded-md px-1.5 py-0.5 transition-colors"
              >
                <span className="inline-flex items-center gap-1 font-mono text-xs break-all">
                  <Filter className="text-muted-foreground size-3 shrink-0" aria-hidden="true" />
                  {value.value}
                </span>
                <span className="text-muted-foreground text-xs tabular-nums">
                  {share}% · {value.times_seen}
                </span>
                <span className="bg-muted col-span-2 mt-1 block h-1 rounded-full">
                  <span className="bg-foreground/50 block h-1 rounded-full" style={{ width: `${share}%` }} />
                </span>
                <span className="text-muted-foreground col-span-2 text-[11px]">
                  last seen <RelativeTime iso={value.last_seen} />
                </span>
              </Link>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
