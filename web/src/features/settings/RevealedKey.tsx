import { useId } from "react";

import { Check, Copy, TriangleAlert } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { copyToClipboard } from "@/lib/clipboard";
import { buildDsn } from "@/lib/dsn";

interface RevealedKeyProps {
  rawKey: string;
  projectId: number;
  /** Clears the key from component state; nothing persists it anywhere else. */
  onDismiss: () => void;
}

/**
 * The one presentation of `CreatedKey.raw_key`. It lives in React state for the
 * life of this component and is never written to storage, so a reload clears
 * this panel — but the DSN itself is not a one-time reveal: it reappears,
 * unmasked, in the key list below (KeyList.tsx), because the server issues the
 * raw value as the listable `public_key` (internal/project/service.go
 * CreateKey). The copy here must not imply otherwise.
 */
export function RevealedKey({ rawKey, projectId, onDismiss }: RevealedKeyProps) {
  const dsn = buildDsn({ public_key: rawKey, project_id: projectId }, window.location.origin);
  const dsnLabelId = useId();
  const dsnCopyId = useId();
  const rawKeyLabelId = useId();
  const rawKeyCopyId = useId();

  return (
    <Alert className="border-level-warning/40 bg-level-warning-surface">
      <TriangleAlert className="text-level-warning" aria-hidden="true" />
      <AlertTitle>New ingest key created</AlertTitle>
      <AlertDescription>
        <p>
          This panel clears on reload, but the DSN stays available in the key list below. Treat it as a
          credential — anyone holding it can send events to this project until the key is revoked.
        </p>

        <p id={dsnLabelId} className="mt-3 text-xs font-medium">
          DSN
        </p>
        <div className="mt-1 flex w-full items-center gap-2">
          <code
            aria-labelledby={dsnLabelId}
            className="bg-muted min-w-0 flex-1 rounded-md px-3 py-2 font-mono text-xs break-all select-all"
          >
            {dsn}
          </code>
          <Button
            type="button"
            variant="outline"
            size="sm"
            aria-labelledby={`${dsnLabelId} ${dsnCopyId}`}
            onClick={() => void copyToClipboard(dsn, "DSN")}
          >
            <Copy aria-hidden="true" />
            <span id={dsnCopyId}>Copy</span>
          </Button>
        </div>

        <p id={rawKeyLabelId} className="text-muted-foreground mt-3 text-xs font-medium">
          Raw key
        </p>
        <div className="mt-1 flex w-full items-center gap-2">
          <code
            aria-labelledby={rawKeyLabelId}
            className="bg-muted text-muted-foreground min-w-0 flex-1 rounded-md px-3 py-2 font-mono text-xs break-all select-all"
          >
            {rawKey}
          </code>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            aria-labelledby={`${rawKeyLabelId} ${rawKeyCopyId}`}
            onClick={() => void copyToClipboard(rawKey, "Ingest key")}
          >
            <Copy aria-hidden="true" />
            <span id={rawKeyCopyId}>Copy</span>
          </Button>
        </div>

        <Button type="button" variant="ghost" size="sm" className="mt-2" onClick={onDismiss}>
          <Check aria-hidden="true" />
          I have stored it
        </Button>
      </AlertDescription>
    </Alert>
  );
}
