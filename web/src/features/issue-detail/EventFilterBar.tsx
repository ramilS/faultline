import type { TimeRangeValue } from "@/features/issues/TimeRangePicker";
import { TimeRangePicker } from "@/features/issues/TimeRangePicker";

export interface EventFilterBarProps {
  readonly environments: readonly string[];
  readonly environment: string;
  readonly onEnvironmentChange: (value: string) => void;
  readonly timeRange: TimeRangeValue;
  readonly onTimeRangeChange: (value: TimeRangeValue) => void;
}

/**
 * Scopes the events pager below to one environment and/or time range. Not
 * `IssueFilterBar`: that component's status/level/assigned/tag/sort controls
 * describe a set of issues, not one issue's own events.
 */
export function EventFilterBar({ environments, environment, onEnvironmentChange, timeRange, onTimeRangeChange }: EventFilterBarProps) {
  if (environments.length < 2) {
    // A single-environment project has nothing to filter by; the time range
    // control alone still earns its place.
    return (
      <div className="flex items-center gap-2">
        <TimeRangePicker value={timeRange} onChange={onTimeRangeChange} />
      </div>
    );
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
      <select
        aria-label="Environment"
        value={environment}
        onChange={(event) => onEnvironmentChange(event.target.value)}
        className="border-input h-8 rounded-md border bg-transparent px-2 text-xs"
      >
        <option value="">All environments</option>
        {environments.map((env) => (
          <option key={env} value={env}>
            {env}
          </option>
        ))}
      </select>
      <TimeRangePicker value={timeRange} onChange={onTimeRangeChange} />
    </div>
  );
}
