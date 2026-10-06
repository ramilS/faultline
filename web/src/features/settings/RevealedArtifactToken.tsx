import { useId } from "react";

import { Check, Copy, TriangleAlert } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { copyToClipboard } from "@/lib/clipboard";

interface RevealedArtifactTokenProps {
  rawToken: string;
  /** Clears the token from component state; nothing persists it anywhere else. */
  onDismiss: () => void;
}

/**
 * The one presentation of `CreatedArtifactToken.raw_token`. Unlike an ingest
 * key's DSN, the server never returns this value again — the list carries only
 * a prefix — so the copy here is honest that leaving loses it.
 */
export function RevealedArtifactToken({ rawToken, onDismiss }: RevealedArtifactTokenProps) {
  const labelId = useId();
  const copyId = useId();
  const uploadUrl = window.location.origin;

  return (
    <Alert className="border-level-warning/40 bg-level-warning-surface">
      <TriangleAlert className="text-level-warning" aria-hidden="true" />
      <AlertTitle>New artifact token created</AlertTitle>
      <AlertDescription>
        <p>
          Copy it now: it is shown once and cannot be retrieved later. Anyone holding it can upload source maps and
          ProGuard mappings to this project until it is revoked.
        </p>

        <p id={labelId} className="mt-3 text-xs font-medium">
          Token
        </p>
        <div className="mt-1 flex w-full items-center gap-2">
          <code
            aria-labelledby={labelId}
            className="bg-muted min-w-0 flex-1 rounded-md px-3 py-2 font-mono text-xs break-all select-all"
          >
            {rawToken}
          </code>
          <Button
            type="button"
            variant="outline"
            size="sm"
            aria-labelledby={`${labelId} ${copyId}`}
            onClick={() => void copyToClipboard(rawToken, "Artifact token")}
          >
            <Copy aria-hidden="true" />
            <span id={copyId}>Copy</span>
          </Button>
        </div>

        <p className="text-muted-foreground mt-3 text-xs">
          Set it as <code className="font-mono">SENTRY_AUTH_TOKEN</code> and point the uploader at{" "}
          <code className="font-mono break-all">{uploadUrl}</code>.
        </p>

        <Button type="button" variant="ghost" size="sm" className="mt-2" onClick={onDismiss}>
          <Check aria-hidden="true" />
          I have stored it
        </Button>
      </AlertDescription>
    </Alert>
  );
}
