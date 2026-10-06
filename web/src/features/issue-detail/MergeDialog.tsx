import { SearchX } from "lucide-react";
import { useEffect, useState } from "react";

import type { IssueSummary } from "@/api";
import { api } from "@/apiClient";
import { EmptyState } from "@/components/EmptyState";
import { ErrorState } from "@/components/ErrorState";
import { LevelBadge } from "@/components/LevelBadge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { messageFor } from "@/errors";
import { useDebounce } from "@/hooks/useDebounce";
import { mergeConfirmationMessage } from "@/issueMerge";

const SEARCH_DEBOUNCE_MS = 250;
const ACKNOWLEDGE_ID = "merge-acknowledge";

type CandidatesState =
  | { readonly status: "idle" }
  | { readonly status: "loading" }
  | { readonly status: "ready"; readonly issues: readonly IssueSummary[] }
  | { readonly status: "failed"; readonly message: string };

interface MergeDialogProps {
  slug: string;
  sourceIssueId: number;
  sourceTimesSeen: number;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Called after the merge succeeds, with the issue that absorbed this one. */
  onMerged: (targetId: number) => void;
}

export function MergeDialog({
  slug,
  sourceIssueId,
  sourceTimesSeen,
  open,
  onOpenChange,
  onMerged
}: MergeDialogProps) {
  const [target, setTarget] = useState<IssueSummary | null>(null);

  // Reopening must not resume a half-finished merge from the previous visit.
  const reset = () => setTarget(null);

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          reset();
        }
        onOpenChange(next);
      }}
    >
      <DialogContent className="sm:max-w-lg">
        {target === null ? (
          <CandidateSearch slug={slug} sourceIssueId={sourceIssueId} onPick={setTarget} />
        ) : (
          <ConfirmMerge
            sourceIssueId={sourceIssueId}
            sourceTimesSeen={sourceTimesSeen}
            target={target}
            onBack={reset}
            onMerged={onMerged}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

interface CandidateSearchProps {
  slug: string;
  sourceIssueId: number;
  onPick: (issue: IssueSummary) => void;
}

function CandidateSearch({ slug, sourceIssueId, onPick }: CandidateSearchProps) {
  const [query, setQuery] = useState("");
  const debouncedQuery = useDebounce(query.trim(), SEARCH_DEBOUNCE_MS);
  const [state, setState] = useState<CandidatesState>({ status: "idle" });

  useEffect(() => {
    if (debouncedQuery === "") {
      setState({ status: "idle" });
      return;
    }

    let active = true;
    setState({ status: "loading" });

    void (async () => {
      try {
        const page = await api.listIssues(slug, { query: debouncedQuery });
        if (active) {
          setState({
            status: "ready",
            issues: page.issues.filter((issue) => issue.id !== sourceIssueId)
          });
        }
      } catch (caught) {
        if (active) {
          setState({ status: "failed", message: messageFor(caught) });
        }
      }
    })();

    return () => {
      active = false;
    };
  }, [slug, sourceIssueId, debouncedQuery]);

  return (
    <>
      <DialogHeader>
        <DialogTitle>Merge into another issue</DialogTitle>
        <DialogDescription>
          Pick the issue that should absorb this one. You will confirm before anything moves.
        </DialogDescription>
      </DialogHeader>

      <div className="space-y-3">
        <div className="space-y-2">
          <Label htmlFor="merge-search">Search issues</Label>
          <Input
            id="merge-search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="title or culprit"
            autoFocus
          />
        </div>

        {state.status === "idle" && (
          <p className="text-muted-foreground text-sm">Type to search this project&rsquo;s issues.</p>
        )}

        {state.status === "loading" && (
          <div className="space-y-2">
            {[0, 1, 2].map((row) => (
              <Skeleton key={row} className="h-12 w-full" />
            ))}
          </div>
        )}

        {state.status === "failed" && <ErrorState className="py-6" message={state.message} />}

        {state.status === "ready" && state.issues.length === 0 && (
          <EmptyState
            className="py-6"
            icon={SearchX}
            title="No matches"
            description="No other issue in this project matches that search."
          />
        )}

        {state.status === "ready" && state.issues.length > 0 && (
          <ul className="max-h-72 space-y-1 overflow-y-auto">
            {state.issues.map((issue) => (
              <li key={issue.id}>
                <button
                  type="button"
                  onClick={() => onPick(issue)}
                  className="hover:bg-accent/60 block w-full rounded-md border px-3 py-2 text-left transition-colors"
                >
                  <span className="flex items-start gap-2">
                    <LevelBadge level={issue.level} className="mt-0.5 shrink-0" />
                    <span className="min-w-0 text-sm break-words">{issue.title}</span>
                  </span>
                  <span className="text-muted-foreground mt-1 block font-mono text-xs break-all">
                    {issue.culprit}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </>
  );
}

interface ConfirmMergeProps {
  sourceIssueId: number;
  sourceTimesSeen: number;
  target: IssueSummary;
  onBack: () => void;
  onMerged: (targetId: number) => void;
}

function ConfirmMerge({ sourceIssueId, sourceTimesSeen, target, onBack, onMerged }: ConfirmMergeProps) {
  const [acknowledged, setAcknowledged] = useState(false);
  const [merging, setMerging] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const confirm = async () => {
    setMerging(true);
    setError(null);
    try {
      await api.mergeIssue(sourceIssueId, target.id);
      onMerged(target.id);
    } catch (caught) {
      setError(messageFor(caught));
    } finally {
      setMerging(false);
    }
  };

  return (
    <>
      <DialogHeader>
        <DialogTitle>Merge is permanent</DialogTitle>
        <DialogDescription>{mergeConfirmationMessage(sourceTimesSeen, target.title)}</DialogDescription>
      </DialogHeader>

      <div className="space-y-3">
        {error !== null && (
          <p role="alert" className="text-level-error text-sm">
            {error}
          </p>
        )}

        <div className="flex items-start gap-2">
          <Checkbox
            id={ACKNOWLEDGE_ID}
            checked={acknowledged}
            onCheckedChange={(checked) => setAcknowledged(checked === true)}
            disabled={merging}
          />
          <Label htmlFor={ACKNOWLEDGE_ID} className="text-sm leading-snug font-normal">
            I understand this cannot be undone.
          </Label>
        </div>
      </div>

      <DialogFooter>
        <Button type="button" variant="outline" onClick={onBack} disabled={merging}>
          Back
        </Button>
        <Button
          type="button"
          variant="destructive"
          onClick={() => void confirm()}
          disabled={!acknowledged || merging}
        >
          {merging ? "Merging…" : "Merge permanently"}
        </Button>
      </DialogFooter>
    </>
  );
}
