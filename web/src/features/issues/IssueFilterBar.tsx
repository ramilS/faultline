import { useEffect, useState } from "react";
import { ListFilter, Search, X } from "lucide-react";

import type { IssueLevel, IssueStatus, TagKeyFacet } from "@/api";
import { api } from "@/apiClient";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuTrigger
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { FacetSelect } from "@/features/issues/FacetSelect";
import { TimeRangePicker } from "@/features/issues/TimeRangePicker";
import type { UrlFilterControls } from "@/features/issues/useUrlFilters";
import { cn } from "@/lib/utils";
import {
  ISSUE_LEVELS,
  ISSUE_SORTS,
  ISSUE_STATUSES,
  nextStatuses,
  parseIssueSort,
  secondaryFilterCount,
  SORT_LABELS,
  type StreamFilters,
  type TagFilter
} from "@/lib/issueFilters";

const STATUS_LABELS: Record<IssueStatus, string> = {
  unresolved: "Unresolved",
  resolved: "Resolved",
  ignored: "Ignored"
};

const LEVEL_LABELS: Record<IssueLevel, string> = {
  fatal: "Fatal",
  error: "Error",
  warning: "Warning",
  info: "Info",
  debug: "Debug"
};

const SECONDARY_FILTERS_ID = "issue-secondary-filters";

const ASSIGNED_ANYONE = "anyone";
const ASSIGNED_OPTIONS: ReadonlyArray<{ value: string; label: string }> = [
  { value: ASSIGNED_ANYONE, label: "Anyone" },
  { value: "me", label: "Assigned to me" },
  { value: "none", label: "Unassigned" }
];

export interface IssueFilterBarProps {
  readonly slug: string;
  readonly controls: UrlFilterControls;
}

export function IssueFilterBar({ slug, controls }: IssueFilterBarProps) {
  const { filters } = controls;
  // Below `sm` only search and the time window stay in view; the rest fold away
  // behind a toggle so the filters do not push the results off the screen.
  const [expanded, setExpanded] = useState(false);
  const foldedCount = secondaryFilterCount(filters);

  function toggleStatus(status: IssueStatus): void {
    controls.setStatuses(nextStatuses(filters.statuses, status));
  }

  function toggleLevel(level: IssueLevel): void {
    const checked = filters.levels.includes(level);
    controls.setLevels(checked ? filters.levels.filter((candidate) => candidate !== level) : [...filters.levels, level]);
  }

  return (
    <div className="flex flex-col gap-2 border-b px-4 py-3 sm:px-6">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-56 flex-1">
          <Search
            className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2"
            aria-hidden="true"
          />
          <Input
            type="search"
            value={controls.queryDraft}
            onChange={(event) => controls.setQueryDraft(event.target.value)}
            placeholder="Search title or culprit"
            aria-label="Search issues"
            className="h-8 pl-8"
          />
        </div>

        <TimeRangePicker
          value={{ statsPeriod: filters.statsPeriod, start: filters.start, end: filters.end }}
          onChange={controls.setTimeRange}
        />

        <Button
          variant="outline"
          size="sm"
          className="h-8 sm:hidden"
          aria-expanded={expanded}
          aria-controls={SECONDARY_FILTERS_ID}
          onClick={() => setExpanded((current) => !current)}
        >
          <ListFilter className="size-4" aria-hidden="true" />
          Filters{foldedCount > 0 && ` (${foldedCount})`}
        </Button>

        <div id={SECONDARY_FILTERS_ID} className={cn("contents", !expanded && "max-sm:hidden")}>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="sm" className="h-8">
                Status ({filters.statuses.length})
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start">
              {ISSUE_STATUSES.map((status) => (
                <DropdownMenuCheckboxItem
                  key={status}
                  checked={filters.statuses.includes(status)}
                  onSelect={(event) => event.preventDefault()}
                  onCheckedChange={() => toggleStatus(status)}
                >
                  {STATUS_LABELS[status]}
                </DropdownMenuCheckboxItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>

          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="sm" className="h-8">
                {filters.levels.length === 0 ? "Level" : `Level (${filters.levels.length})`}
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start">
              {ISSUE_LEVELS.map((level) => (
                <DropdownMenuCheckboxItem
                  key={level}
                  checked={filters.levels.includes(level)}
                  onSelect={(event) => event.preventDefault()}
                  onCheckedChange={() => toggleLevel(level)}
                >
                  {LEVEL_LABELS[level]}
                </DropdownMenuCheckboxItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>

          <Select
            value={filters.assigned === "" ? ASSIGNED_ANYONE : filters.assigned}
            onValueChange={(value) => controls.setAssigned(value === ASSIGNED_ANYONE ? "" : value)}
          >
            <SelectTrigger size="sm" aria-label="Filter by assignee" className="h-8">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {ASSIGNED_OPTIONS.map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {option.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>

          <FacetFilterIfPopulated
            slug={slug}
            tagKey="environment"
            label="Environment"
            selectedValues={filters.environments}
            onChange={controls.setEnvironments}
          />

          <FacetFilterIfPopulated
            slug={slug}
            tagKey="release"
            label="Release"
            selectedValues={filters.releases}
            onChange={controls.setReleases}
          />

          <TagFilterControl slug={slug} tags={filters.tags} onChange={controls.setTags} />

          <Select value={filters.sort} onValueChange={(value) => controls.setSort(parseIssueSort(value))}>
            <SelectTrigger size="sm" aria-label="Sort issues by" className="h-8">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {ISSUE_SORTS.map((sort) => (
                <SelectItem key={sort} value={sort}>
                  Sort: {SORT_LABELS[sort]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>

      {filters.tags.length > 0 && <ActiveTagBadges tags={filters.tags} onChange={controls.setTags} />}
    </div>
  );
}

function tagLabel(tag: TagFilter): string {
  return `${tag.key}:${tag.value}`;
}

function ActiveTagBadges({ tags, onChange }: { tags: StreamFilters["tags"]; onChange: (tags: readonly TagFilter[]) => void }) {
  return (
    <div className="flex flex-wrap gap-1.5" role="group" aria-label="Active tag filters">
      {tags.map((tag) => (
        <Badge key={tagLabel(tag)} variant="secondary" className="gap-1 pr-1 font-mono text-xs">
          {tagLabel(tag)}
          <button
            type="button"
            aria-label={`Remove tag filter ${tagLabel(tag)}`}
            onClick={() => onChange(tags.filter((candidate) => candidate !== tag))}
            className="hover:text-destructive"
          >
            <X className="size-3" aria-hidden="true" />
          </button>
        </Badge>
      ))}
    </div>
  );
}

/**
 * Wraps FacetSelect for a fixed tag key, hiding the control entirely when
 * that key has no values yet: a project that has ingested nothing — or, for
 * release, one whose SDK never sends it — would otherwise get a permanent
 * "No values found" dead end. Issue aggregation writes environment and
 * release as synthetic tag rows (tagDeltasFor in internal/pipeline/
 * aggregate.go), so the control appears once those rows exist.
 */
function FacetFilterIfPopulated({
  slug,
  tagKey,
  label,
  selectedValues,
  onChange
}: {
  slug: string;
  tagKey: string;
  label: string;
  selectedValues: readonly string[];
  onChange: (values: readonly string[]) => void;
}) {
  const [populated, setPopulated] = useState(false);

  useEffect(() => {
    let active = true;
    api
      .listTagValues(slug, tagKey)
      .then((values) => {
        if (active) {
          setPopulated(values.length > 0);
        }
      })
      .catch(() => {
        // A failed vocabulary check just keeps the control hidden; the rest
        // of the filter bar still works.
      });
    return () => {
      active = false;
    };
  }, [slug, tagKey]);

  if (!populated) {
    return null;
  }

  return <FacetSelect slug={slug} tagKey={tagKey} label={label} selectedValues={selectedValues} onChange={onChange} />;
}

/**
 * Adds a tag=key:value filter: pick a key from the project's tag vocabulary,
 * then reuse FacetSelect to multi-select that key's values. Values already
 * chosen for other keys stay untouched — only the active key's slice of
 * `tags` is replaced.
 */
function TagFilterControl({
  slug,
  tags,
  onChange
}: {
  slug: string;
  tags: StreamFilters["tags"];
  onChange: (tags: readonly TagFilter[]) => void;
}) {
  const [keys, setKeys] = useState<readonly TagKeyFacet[]>([]);
  const [activeKey, setActiveKey] = useState("");

  useEffect(() => {
    let active = true;
    api
      .listTagKeys(slug)
      .then((result) => {
        // The server already excludes environment/release from this list —
        // see TagKeys in internal/storage/postgres/issues.go — so this
        // control only ever offers keys without a dedicated filter.
        if (active) {
          setKeys(result);
        }
      })
      .catch(() => {
        // A failed vocabulary fetch just leaves the picker empty; the rest
        // of the filter bar still works.
      });
    return () => {
      active = false;
    };
  }, [slug]);

  if (keys.length === 0) {
    return null;
  }

  const selectedForActiveKey = tags.filter((tag) => tag.key === activeKey).map((tag) => tag.value);

  function setValuesForActiveKey(values: readonly string[]): void {
    const withoutActiveKey = tags.filter((tag) => tag.key !== activeKey);
    onChange([...withoutActiveKey, ...values.map((value) => ({ key: activeKey, value }))]);
  }

  return (
    <div className="flex items-center gap-2">
      <Select value={activeKey} onValueChange={setActiveKey}>
        <SelectTrigger size="sm" aria-label="Tag key to filter by" className="h-8">
          <SelectValue placeholder="Tag…" />
        </SelectTrigger>
        <SelectContent>
          {keys.map((key) => (
            <SelectItem key={key.key} value={key.key}>
              {key.key}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      {activeKey !== "" && (
        <FacetSelect
          slug={slug}
          tagKey={activeKey}
          label={`Tag: ${activeKey}`}
          selectedValues={selectedForActiveKey}
          onChange={setValuesForActiveKey}
        />
      )}
    </div>
  );
}
