import { useState } from "react";
import { CalendarClock } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { buildCustomRange } from "@/features/issues/timeRange";
import { ALL_TIME_WINDOW, STATS_PERIODS, type StatsPeriod } from "@/lib/issueFilters";

const PERIOD_LABELS: Record<StatsPeriod, string> = {
  "24h": "Last 24 hours",
  "7d": "Last 7 days",
  "14d": "Last 14 days",
  "30d": "Last 30 days",
  "90d": "Last 90 days"
};

export interface TimeRangeValue {
  readonly statsPeriod: StatsPeriod | "";
  readonly start: string;
  readonly end: string;
}

export interface TimeRangePickerProps {
  readonly value: TimeRangeValue;
  readonly onChange: (next: TimeRangeValue) => void;
}

function toDateInputValue(rfc3339: string): string {
  return rfc3339 === "" ? "" : rfc3339.slice(0, 10);
}

function currentRangeLabel(value: TimeRangeValue): string {
  if (value.start !== "" && value.end !== "") {
    return `${toDateInputValue(value.start)} – ${toDateInputValue(value.end)}`;
  }
  return value.statsPeriod === "" ? "All time" : PERIOD_LABELS[value.statsPeriod];
}

export function TimeRangePicker({ value, onChange }: TimeRangePickerProps) {
  const [open, setOpen] = useState(false);
  const [fromDraft, setFromDraft] = useState(toDateInputValue(value.start));
  const [toDraft, setToDraft] = useState(toDateInputValue(value.end));
  const [rangeError, setRangeError] = useState<string | null>(null);

  function updateFromDraft(next: string): void {
    setFromDraft(next);
    setRangeError(null);
  }

  function updateToDraft(next: string): void {
    setToDraft(next);
    setRangeError(null);
  }

  function applyPreset(period: StatsPeriod): void {
    onChange({ statsPeriod: period, start: "", end: "" });
    setOpen(false);
  }

  function applyAllTime(): void {
    onChange({ ...ALL_TIME_WINDOW });
    setOpen(false);
  }

  function applyCustomRange(): void {
    if (fromDraft === "" || toDraft === "") {
      return;
    }
    const result = buildCustomRange(fromDraft, toDraft);
    if (!result.ok) {
      setRangeError(result.error);
      return;
    }
    onChange({ statsPeriod: "", start: result.range.start, end: result.range.end });
    setOpen(false);
  }

  const label = currentRangeLabel(value);
  const presetActive = (period: StatsPeriod) => value.statsPeriod === period && value.start === "";
  const allTimeActive = value.statsPeriod === "" && value.start === "" && value.end === "";

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (next) {
          setFromDraft(toDateInputValue(value.start));
          setToDraft(toDateInputValue(value.end));
          setRangeError(null);
        }
      }}
    >
      <PopoverTrigger asChild>
        <Button variant="outline" size="sm" aria-label={`Time range: ${label}`} className="h-8 gap-1.5">
          <CalendarClock className="size-3.5" aria-hidden="true" />
          {label}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-72 space-y-3">
        <div className="flex flex-col gap-1" role="group" aria-label="Preset time ranges">
          {STATS_PERIODS.map((period) => (
            <Button
              key={period}
              type="button"
              variant={presetActive(period) ? "secondary" : "ghost"}
              size="sm"
              aria-pressed={presetActive(period)}
              className="justify-start"
              onClick={() => applyPreset(period)}
            >
              {PERIOD_LABELS[period]}
            </Button>
          ))}
          <Button
            type="button"
            variant={allTimeActive ? "secondary" : "ghost"}
            size="sm"
            aria-pressed={allTimeActive}
            className="justify-start"
            onClick={applyAllTime}
          >
            All time
          </Button>
        </div>

        <div className="border-t pt-3">
          <p className="text-muted-foreground mb-2 text-xs font-medium">Custom range</p>
          <div className="flex items-end gap-2">
            <div className="flex flex-1 flex-col gap-1">
              <Label htmlFor="time-range-from">From</Label>
              <Input
                id="time-range-from"
                type="date"
                value={fromDraft}
                max={toDraft || undefined}
                onChange={(event) => updateFromDraft(event.target.value)}
                className="h-8 px-2 text-sm"
                aria-invalid={rangeError !== null}
                aria-describedby={rangeError === null ? undefined : "time-range-error"}
              />
            </div>
            <div className="flex flex-1 flex-col gap-1">
              <Label htmlFor="time-range-to">To</Label>
              <Input
                id="time-range-to"
                type="date"
                value={toDraft}
                min={fromDraft || undefined}
                onChange={(event) => updateToDraft(event.target.value)}
                className="h-8 px-2 text-sm"
                aria-invalid={rangeError !== null}
                aria-describedby={rangeError === null ? undefined : "time-range-error"}
              />
            </div>
          </div>
          {rangeError !== null && (
            <p id="time-range-error" role="alert" className="text-destructive mt-2 text-xs">
              {rangeError}
            </p>
          )}
          <Button
            type="button"
            size="sm"
            className="mt-2 w-full"
            disabled={fromDraft === "" || toDraft === ""}
            onClick={applyCustomRange}
          >
            Apply range
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}
