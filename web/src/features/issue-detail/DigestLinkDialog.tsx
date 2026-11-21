import { useState } from "react";
import { toast } from "sonner";

import type { DigestLink, DigestLinkTTL } from "@/api";
import { api } from "@/apiClient";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog";
import { messageFor } from "@/errors";
import { absoluteTime } from "@/lib/time";

import { CopyButton } from "./CopyButton";

const LIFETIMES: readonly { ttl: DigestLinkTTL; label: string }[] = [
  { ttl: "1h", label: "1 hour" },
  { ttl: "24h", label: "24 hours" },
  { ttl: "7d", label: "7 days" }
];

interface DigestLinkDialogProps {
  issueId: number;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function DigestLinkDialog({ issueId, open, onOpenChange }: DigestLinkDialogProps) {
  const [ttl, setTtl] = useState<DigestLinkTTL>("24h");
  const [link, setLink] = useState<DigestLink | null>(null);
  const [pending, setPending] = useState(false);

  const close = (next: boolean) => {
    if (!next) {
      setLink(null);
    }
    onOpenChange(next);
  };

  const create = async () => {
    setPending(true);
    try {
      setLink(await api.createDigestLink(issueId, ttl));
    } catch (caught) {
      toast.error(`Link could not be created: ${messageFor(caught)}`);
    } finally {
      setPending(false);
    }
  };

  const revokeAll = async () => {
    setPending(true);
    try {
      await api.revokeDigestLinks(issueId);
      setLink(null);
      toast.success("Every temporary link to this issue is revoked");
    } catch (caught) {
      toast.error(`Links could not be revoked: ${messageFor(caught)}`);
    } finally {
      setPending(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Temporary link for AI</DialogTitle>
          <DialogDescription>
            Anyone holding the link can read this issue&apos;s digest without signing in until it expires or is
            revoked. The digest leaves out user identities, request headers and frame variables.
          </DialogDescription>
        </DialogHeader>

        <div role="group" aria-label="Link lifetime" className="flex gap-2">
          {LIFETIMES.map((lifetime) => (
            <Button
              key={lifetime.ttl}
              type="button"
              size="sm"
              variant={ttl === lifetime.ttl ? "default" : "outline"}
              aria-pressed={ttl === lifetime.ttl}
              onClick={() => setTtl(lifetime.ttl)}
            >
              {lifetime.label}
            </Button>
          ))}
        </div>

        {link !== null && (
          <div className="space-y-1">
            <div className="flex items-center gap-2">
              <code className="bg-muted min-w-0 flex-1 rounded-md px-3 py-2 font-mono text-xs break-all select-all">
                {link.url}
              </code>
              <CopyButton value={link.url} label="Temporary link" variant="outline" size="sm" />
            </div>
            <p className="text-muted-foreground text-xs">Expires {absoluteTime(link.expires_at)}</p>
          </div>
        )}

        <DialogFooter>
          <Button type="button" variant="outline" disabled={pending} onClick={() => void revokeAll()}>
            Revoke all links
          </Button>
          <Button type="button" disabled={pending} onClick={() => void create()}>
            Create link
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
