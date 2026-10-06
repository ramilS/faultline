import { Ban, CircleCheck } from "lucide-react";

import type { ArtifactToken } from "@/api";
import { RelativeTime } from "@/components/RelativeTime";
import { STACKED_ROWS_BELOW_MD } from "@/components/stackedTable";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useSession } from "@/features/auth/SessionProvider";
import { cn } from "@/lib/utils";

import { tokenPrefixLabel } from "./artifactTokenLabel";

interface ArtifactTokenListProps {
  tokens: readonly ArtifactToken[];
  onRevokeRequested: (token: ArtifactToken) => void;
}

export function ArtifactTokenList({ tokens, onRevokeRequested }: ArtifactTokenListProps) {
  const { session } = useSession();
  const isOwner = session.state === "authenticated" && session.user.role === "owner";

  return (
    <Table className={STACKED_ROWS_BELOW_MD}>
      <TableHeader>
        <TableRow>
          <TableHead className="w-full">Name</TableHead>
          <TableHead>Created</TableHead>
          <TableHead className="whitespace-nowrap">Last used</TableHead>
          <TableHead>Status</TableHead>
          <TableHead className="w-0 text-right">
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {tokens.map((token) => (
          <TokenRow key={token.id} token={token} isOwner={isOwner} onRevokeRequested={onRevokeRequested} />
        ))}
      </TableBody>
    </Table>
  );
}

interface TokenRowProps {
  token: ArtifactToken;
  isOwner: boolean;
  onRevokeRequested: (token: ArtifactToken) => void;
}

function TokenRow({ token, isOwner, onRevokeRequested }: TokenRowProps) {
  const isRevoked = token.revoked_at !== null;

  return (
    <TableRow className={cn(isRevoked && "text-muted-foreground")}>
      <TableCell>
        <span className={cn("font-medium", isRevoked && "line-through")}>{token.name}</span>
        <code className="text-muted-foreground mt-0.5 block font-mono text-[11px]">{tokenPrefixLabel(token.token_prefix)}</code>
      </TableCell>

      <TableCell>
        <RelativeTime iso={token.created_at} className="text-muted-foreground text-xs" />
      </TableCell>

      <TableCell>
        {token.last_used_at === null ? (
          <span className="text-muted-foreground text-xs">Never</span>
        ) : (
          <RelativeTime iso={token.last_used_at} className="text-muted-foreground text-xs" />
        )}
      </TableCell>

      <TableCell>
        {token.revoked_at === null ? (
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
            <RelativeTime iso={token.revoked_at} className="text-muted-foreground text-xs" />
          </span>
        )}
      </TableCell>

      <TableCell className="text-right">
        {/* A revoked token is terminal, and a member cannot revoke at all: both
            leave no action, so the cell is empty rather than a disabled control. */}
        {!isRevoked && isOwner && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="text-destructive hover:text-destructive"
            aria-label={`Revoke ${token.name}`}
            onClick={() => onRevokeRequested(token)}
          >
            Revoke
          </Button>
        )}
      </TableCell>
    </TableRow>
  );
}
