import { Ban, CircleCheck, Copy } from "lucide-react";

import type { Key } from "@/api";
import { RelativeTime } from "@/components/RelativeTime";
import { STACKED_ROWS_BELOW_MD } from "@/components/stackedTable";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useSession } from "@/features/auth/SessionProvider";
import { copyToClipboard } from "@/lib/clipboard";
import { buildDsn } from "@/lib/dsn";
import { cn } from "@/lib/utils";

interface KeyListProps {
  keys: readonly Key[];
  onRevokeRequested: (key: Key) => void;
}

export function KeyList({ keys, onRevokeRequested }: KeyListProps) {
  const { session } = useSession();
  const isOwner = session.state === "authenticated" && session.user.role === "owner";

  return (
    <Table className={STACKED_ROWS_BELOW_MD}>
      <TableHeader>
        <TableRow>
          {/* The DSN is what the page is for and the only column of unbounded
              width, so it takes whatever the others leave. */}
          <TableHead className="w-full">DSN</TableHead>
          <TableHead className="whitespace-nowrap">Rate limit</TableHead>
          <TableHead>Status</TableHead>
          <TableHead className="w-0 text-right">
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {keys.map((key) => (
          <KeyRow key={key.id} keyRecord={key} isOwner={isOwner} onRevokeRequested={onRevokeRequested} />
        ))}
      </TableBody>
    </Table>
  );
}

interface KeyRowProps {
  keyRecord: Key;
  isOwner: boolean;
  onRevokeRequested: (key: Key) => void;
}

function KeyRow({ keyRecord, isOwner, onRevokeRequested }: KeyRowProps) {
  const isRevoked = keyRecord.revoked_at !== null;
  const dsn = buildDsn(keyRecord, window.location.origin);

  return (
    <TableRow className={cn(isRevoked && "text-muted-foreground")}>
      <TableCell>
        <div className="flex items-center gap-1">
          <code
            className={cn("min-w-0 font-mono text-xs break-all", isRevoked && "line-through")}
          >
            {dsn}
          </code>
          {/* A member's DSN and public key are masked server-side, so a copy
              here would hand back dots, not a working credential — the
              control is omitted rather than offered for nothing. */}
          {isOwner && (
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label="Copy DSN"
              onClick={() => void copyToClipboard(dsn, "DSN")}
            >
              <Copy aria-hidden="true" />
            </Button>
          )}
        </div>
        <div className="mt-1 flex items-center gap-1">
          <code
            className={cn(
              "text-muted-foreground min-w-0 font-mono text-[11px] break-all",
              isRevoked && "line-through"
            )}
          >
            {keyRecord.public_key}
          </code>
          {isOwner && (
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label="Copy public key"
              onClick={() => void copyToClipboard(keyRecord.public_key, "Public key")}
            >
              <Copy aria-hidden="true" />
            </Button>
          )}
        </div>
      </TableCell>

      <TableCell className="whitespace-nowrap tabular-nums">
        {keyRecord.rate_limit_rps.toLocaleString("en-US")}/s
        <span className="text-muted-foreground"> · burst {keyRecord.rate_limit_burst.toLocaleString("en-US")}</span>
      </TableCell>

      <TableCell>
        {keyRecord.revoked_at === null ? (
          <Badge variant="outline">
            <CircleCheck className="text-status-resolved" aria-hidden="true" />
            Active
          </Badge>
        ) : (
          <span className="flex items-center gap-2">
            <Badge variant="outline">
              <Ban aria-hidden="true" />
              Revoked
            </Badge>
            <RelativeTime iso={keyRecord.revoked_at} className="text-muted-foreground text-xs" />
          </span>
        )}
      </TableCell>

      <TableCell className="text-right">
        {/* A revoked key is terminal, and a member cannot revoke at all: both
            leave no action, so the cell is empty rather than offering a
            disabled control. The header explanation already says why. */}
        {!isRevoked && isOwner && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="text-destructive hover:text-destructive"
            onClick={() => onRevokeRequested(keyRecord)}
          >
            Revoke
          </Button>
        )}
      </TableCell>
    </TableRow>
  );
}
