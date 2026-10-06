import { FileKey } from "lucide-react";
import { useCallback, useState } from "react";
import { Navigate, useParams } from "react-router-dom";

import type { ArtifactToken, CreatedArtifactToken } from "@/api";
import { NotFoundPage } from "@/app/NotFoundPage";
import { api } from "@/apiClient";
import { EmptyState } from "@/components/EmptyState";
import { ErrorState } from "@/components/ErrorState";
import { PageHeader } from "@/components/PageHeader";
import { Skeleton } from "@/components/ui/skeleton";
import { useSession } from "@/features/auth/SessionProvider";
import { ArtifactTokenList } from "@/features/settings/ArtifactTokenList";
import { CreateArtifactTokenDialog } from "@/features/settings/CreateArtifactTokenDialog";
import { RevealedArtifactToken } from "@/features/settings/RevealedArtifactToken";
import { RevokeArtifactTokenDialog } from "@/features/settings/RevokeArtifactTokenDialog";
import { useAsyncResource } from "@/hooks/useAsyncResource";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

export function ArtifactTokensPage() {
  const { slug } = useParams<{ slug: string }>();
  // The route pattern cannot match without a slug; the guard keeps the type
  // honest instead of forcing a non-null assertion downstream.
  return slug === undefined ? <Navigate to="/projects" replace /> : <ArtifactTokensScreen slug={slug} />;
}

function ArtifactTokensScreen({ slug }: { slug: string }) {
  const { session } = useSession();
  const isOwner = session.state === "authenticated" && session.user.role === "owner";
  useDocumentTitle(`Artifact tokens · ${slug}`);
  const { state, reload } = useAsyncResource<ArtifactToken[]>(useCallback(() => api.listArtifactTokens(slug), [slug]));
  const [revealed, setRevealed] = useState<CreatedArtifactToken | null>(null);
  const [revokeTarget, setRevokeTarget] = useState<ArtifactToken | null>(null);

  const revealCreated = async (created: CreatedArtifactToken) => {
    setRevealed(created);
    await reload({ silent: true });
  };

  const forgetRevokedToken = async (revokedTokenId: number) => {
    setRevealed((current) => (current !== null && current.token.id === revokedTokenId ? null : current));
    await reload({ silent: true });
  };

  // A 404 here means this project is outside the caller's scope — the whole
  // screen is the wrong chrome for that, so it preempts the header and action.
  if (state.status === "failed" && state.notFound) {
    return <NotFoundPage />;
  }

  return (
    <>
      <PageHeader
        title="Artifact tokens"
        description={
          isOwner
            ? "Let sentry-cli or the Gradle plugin upload source maps and ProGuard mappings to this project."
            : "Let sentry-cli or the Gradle plugin upload source maps and ProGuard mappings to this project. Artifact tokens are managed by an owner."
        }
      >
        {isOwner && <CreateArtifactTokenDialog slug={slug} onCreated={revealCreated} />}
      </PageHeader>

      <div className="space-y-4 p-4 sm:p-6">
        {revealed !== null && <RevealedArtifactToken rawToken={revealed.raw_token} onDismiss={() => setRevealed(null)} />}

        {state.status === "loading" && (
          <div className="space-y-2">
            {[0, 1, 2].map((row) => (
              <Skeleton key={row} className="h-12 w-full" />
            ))}
          </div>
        )}

        {state.status === "failed" && <ErrorState message={state.message} onRetry={() => void reload()} />}

        {state.status === "ready" && state.value.length === 0 && (
          <EmptyState
            icon={FileKey}
            title="No artifact tokens yet"
            description={
              isOwner
                ? "Uploads of source maps and ProGuard mappings need a token. Create one, then set it as SENTRY_AUTH_TOKEN in the build that uploads them."
                : "Uploads of source maps and ProGuard mappings need a token. An owner needs to create one."
            }
            action={isOwner ? <CreateArtifactTokenDialog slug={slug} onCreated={revealCreated} /> : undefined}
          />
        )}

        {state.status === "ready" && state.value.length > 0 && (
          <ArtifactTokenList tokens={state.value} onRevokeRequested={setRevokeTarget} />
        )}
      </div>

      <RevokeArtifactTokenDialog
        slug={slug}
        target={revokeTarget}
        onClose={() => setRevokeTarget(null)}
        onRevoked={forgetRevokedToken}
      />
    </>
  );
}
