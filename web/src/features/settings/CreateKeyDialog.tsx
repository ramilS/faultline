import { Plus } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";

import type { CreatedKey } from "@/api";
import { api } from "@/apiClient";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { messageFor } from "@/errors";
import {
  DEFAULT_RATE_LIMIT_BURST,
  DEFAULT_RATE_LIMIT_RPS,
  parseRateLimit,
  type RateLimitErrors
} from "@/features/settings/rateLimit";

interface CreateKeyDialogProps {
  slug: string;
  /** Receives the one-time raw key; the caller decides how to reveal it. */
  onCreated: (created: CreatedKey) => Promise<void>;
  trigger?: ReactNode;
}

export function CreateKeyDialog({ slug, onCreated, trigger }: CreateKeyDialogProps) {
  const [open, setOpen] = useState(false);
  const [rps, setRps] = useState(String(DEFAULT_RATE_LIMIT_RPS));
  const [burst, setBurst] = useState(String(DEFAULT_RATE_LIMIT_BURST));
  const [fieldErrors, setFieldErrors] = useState<RateLimitErrors>({});
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const close = () => {
    setOpen(false);
    setRps(String(DEFAULT_RATE_LIMIT_RPS));
    setBurst(String(DEFAULT_RATE_LIMIT_BURST));
    setFieldErrors({});
    setSubmitError(null);
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const parsed = parseRateLimit(rps, burst);
    if (!parsed.ok) {
      setFieldErrors(parsed.errors);
      setSubmitError(null);
      return;
    }

    setFieldErrors({});
    setSubmitError(null);
    setSubmitting(true);
    try {
      const created = await api.createKey(slug, parsed.value);
      await onCreated(created);
      close();
    } catch (caught) {
      setSubmitError(messageFor(caught));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => (next ? setOpen(true) : close())}>
      <DialogTrigger asChild>
        {trigger ?? (
          <Button size="sm">
            <Plus aria-hidden="true" />
            New ingest key
          </Button>
        )}
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>New ingest key</DialogTitle>
            <DialogDescription>
              The rate limit is enforced per key. The sustained rate is the steady state; the burst is how many
              events may arrive at once before the limiter rejects them.
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-4">
            {submitError !== null && (
              <p role="alert" className="text-destructive text-sm">
                {submitError}
              </p>
            )}

            <div className="space-y-2">
              <Label htmlFor="key-rate-limit-rps">Sustained rate (events per second)</Label>
              <Input
                id="key-rate-limit-rps"
                value={rps}
                onChange={(event) => setRps(event.target.value)}
                inputMode="numeric"
                className="font-mono tabular-nums"
                aria-invalid={fieldErrors.rps !== undefined}
                aria-describedby={fieldErrors.rps === undefined ? undefined : "key-rate-limit-rps-error"}
                autoFocus
              />
              {fieldErrors.rps !== undefined && (
                <p id="key-rate-limit-rps-error" role="alert" className="text-destructive text-xs">
                  {fieldErrors.rps}
                </p>
              )}
            </div>

            <div className="space-y-2">
              <Label htmlFor="key-rate-limit-burst">Burst (events)</Label>
              <Input
                id="key-rate-limit-burst"
                value={burst}
                onChange={(event) => setBurst(event.target.value)}
                inputMode="numeric"
                className="font-mono tabular-nums"
                aria-invalid={fieldErrors.burst !== undefined}
                aria-describedby={fieldErrors.burst === undefined ? undefined : "key-rate-limit-burst-error"}
              />
              {fieldErrors.burst !== undefined && (
                <p id="key-rate-limit-burst-error" role="alert" className="text-destructive text-xs">
                  {fieldErrors.burst}
                </p>
              )}
            </div>
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={close}>
              Cancel
            </Button>
            <Button type="submit" disabled={submitting}>
              {submitting ? "Creating…" : "Create key"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
