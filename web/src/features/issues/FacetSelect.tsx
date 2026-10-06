import { useEffect, useRef, useState } from "react";
import { ChevronDown, X } from "lucide-react";

import type { TagValue } from "@/api";
import { api } from "@/apiClient";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { useDebounce } from "@/hooks/useDebounce";
import { escapeFilterValue } from "@/lib/issueFilters";

const SEARCH_DEBOUNCE_MS = 200;

export interface FacetSelectProps {
  readonly slug: string;
  /** The tag key this facet reads from — "environment", "release", or a custom tag's key. */
  readonly tagKey: string;
  /** Accessible name and visible label for the control, e.g. "Environment". */
  readonly label: string;
  readonly selectedValues: readonly string[];
  readonly onChange: (values: readonly string[]) => void;
}

/**
 * Multi-select autocomplete over one project tag key's values, fed by
 * GET /projects/{slug}/tags/{key}/values.
 *
 * The search box's own debounce is a distinct concern from the trailing
 * filter debounce elsewhere in the stream: that one delays committing a
 * value the user will still act on; this one also has to make sure a slow
 * response to an earlier keystroke can never land after, and overwrite, the
 * response to a later one. Aborting the previous request on every new
 * keystroke (rather than only checking staleness after the fact) also stops
 * the abandoned request from doing pointless work.
 */
export function FacetSelect({ slug, tagKey, label, selectedValues, onChange }: FacetSelectProps) {
  const [open, setOpen] = useState(false);
  const [searchDraft, setSearchDraft] = useState("");
  const debouncedSearch = useDebounce(searchDraft, SEARCH_DEBOUNCE_MS);
  const [values, setValues] = useState<readonly TagValue[]>([]);
  const [loadFailed, setLoadFailed] = useState(false);

  const inFlight = useRef<AbortController | null>(null);

  useEffect(() => {
    if (!open) {
      return;
    }
    inFlight.current?.abort();
    const controller = new AbortController();
    inFlight.current = controller;
    setLoadFailed(false);

    // A literal "*" is a wildcard to this endpoint, matching everything
    // instead of nothing, so the user's search text is escaped the same way
    // a selected filter value is before it goes on the wire.
    const escapedSearch = escapeFilterValue(debouncedSearch);
    api
      .listTagValues(slug, tagKey, escapedSearch === "" ? undefined : escapedSearch, controller.signal)
      .then((results) => {
        setValues(results);
      })
      .catch((error: unknown) => {
        if (error instanceof DOMException && error.name === "AbortError") {
          return;
        }
        setLoadFailed(true);
      });

    return () => controller.abort();
  }, [open, slug, tagKey, debouncedSearch]);

  function toggle(value: string): void {
    const next = selectedValues.includes(value)
      ? selectedValues.filter((existing) => existing !== value)
      : [...selectedValues, value];
    onChange(next);
  }

  const triggerLabel = selectedValues.length === 0 ? label : `${label} (${selectedValues.length})`;

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) {
          setSearchDraft("");
        }
      }}
    >
      <PopoverTrigger asChild>
        <Button variant="outline" size="sm" className="h-8">
          {triggerLabel}
          <ChevronDown className="size-3.5 opacity-60" aria-hidden="true" />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-64 p-0">
        <Command shouldFilter={false}>
          {selectedValues.length > 0 && (
            <div className="flex flex-wrap gap-1 border-b p-2">
              {selectedValues.map((value) => (
                <Badge key={value} variant="secondary" className="gap-1 pr-1">
                  {value}
                  <button
                    type="button"
                    aria-label={`Remove ${label.toLowerCase()} filter ${value}`}
                    onClick={() => toggle(value)}
                    className="hover:text-destructive"
                  >
                    <X className="size-3" aria-hidden="true" />
                  </button>
                </Badge>
              ))}
            </div>
          )}
          <CommandInput
            value={searchDraft}
            onValueChange={setSearchDraft}
            placeholder={`Search ${label.toLowerCase()}`}
            aria-label={`Search ${label.toLowerCase()} values`}
          />
          <CommandList>
            {loadFailed && <CommandEmpty>Couldn't load values.</CommandEmpty>}
            {!loadFailed && values.length === 0 && <CommandEmpty>No values found.</CommandEmpty>}
            <CommandGroup>
              {values.map((item) => {
                const isSelected = selectedValues.includes(item.value);
                return (
                  <CommandItem
                    key={item.value}
                    value={item.value}
                    aria-selected={isSelected}
                    onSelect={() => toggle(item.value)}
                    className="gap-2"
                  >
                    <Checkbox checked={isSelected} aria-hidden="true" tabIndex={-1} />
                    <span className="flex-1 truncate">{item.value}</span>
                    <span className="text-muted-foreground text-xs">{item.times_seen}</span>
                  </CommandItem>
                );
              })}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
