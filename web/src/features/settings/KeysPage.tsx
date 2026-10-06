import { KeyRound } from "lucide-react";
import { useCallback, useState } from "react";
import { Navigate, useParams } from "react-router-dom";

import type { CreatedKey, Key } from "@/api";
import { NotFoundPage } from "@/app/NotFoundPage";
import { api } from "@/apiClient";
import { EmptyState } from "@/components/EmptyState";
import { ErrorState } from "@/components/ErrorState";
import { PageHeader } from "@/components/PageHeader";
import { Skeleton } from "@/components/ui/skeleton";
import { useSession } from "@/features/auth/SessionProvider";
import { CreateKeyDialog } from "@/features/settings/CreateKeyDialog";
import { KeyList } from "@/features/settings/KeyList";
import { useProjects } from "@/features/projects/ProjectsProvider";
import { RevealedKey } from "@/features/settings/RevealedKey";
import { RevokeKeyDialog } from "@/features/settings/RevokeKeyDialog";
import { SdkSetupCard } from "@/features/settings/SdkSetupCard";
import { useAsyncResource } from "@/hooks/useAsyncResource";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import { buildDsn } from "@/lib/dsn";

export function KeysPage() {
  const { slug } = useParams<{ slug: string }>();
  // The route pattern cannot match without a slug; the guard keeps the type
  // honest instead of forcing a non-null assertion downstream.
  return slug === undefined ? <Navigate to="/projects" replace /> : <KeysScreen slug={slug} />;
}

interface KeysScreenProps {
  slug: string;
}

function KeysScreen({ slug }: KeysScreenProps) {
  const { session } = useSession();
  useDocumentTitle(`Ingest keys · ${slug}`);
  const isOwner = session.state === "authenticated" && session.user.role === "owner";
  const { state, reload } = useAsyncResource<Key[]>(useCallback(() => api.listKeys(slug), [slug]));
  const [revealed, setRevealed] = useState<CreatedKey | null>(null);
  const [revokeTarget, setRevokeTarget] = useState<Key | null>(null);
  const { state: projects } = useProjects();
  const platform =
    projects.status === "ready" ? projects.projects.find((project) => project.slug === slug)?.platform : undefined;

  const revealCreated = async (created: CreatedKey) => {
    setRevealed(created);
    await reload({ silent: true });
  };

  const forgetRevokedKey = async (revokedKeyId: number) => {
    setRevealed((current) => (current !== null && current.key.id === revokedKeyId ? null : current));
    await reload({ silent: true });
  };

  // A 404 here means this project is outside the caller's scope — the whole
  // screen is the wrong chrome for that, not just this one slot, so it
  // preempts the header and create-key action rather than nesting inside them.
  if (state.status === "failed" && state.notFound) {
    return <NotFoundPage />;
  }

  return (
    <>
      <PageHeader
        title="Ingest keys"
        description={
          isOwner
            ? "Point any Sentry SDK at this project by setting its DSN."
            : "Point any Sentry SDK at this project by setting its DSN. Ingest keys are managed by an owner."
        }
      >
        {isOwner && <CreateKeyDialog slug={slug} onCreated={revealCreated} />}
      </PageHeader>

      <div className="space-y-4 p-4 sm:p-6">
        {revealed !== null && (
          <RevealedKey
            rawKey={revealed.raw_key}
            projectId={revealed.key.project_id}
            onDismiss={() => setRevealed(null)}
          />
        )}

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
            icon={KeyRound}
            title="No ingest keys yet"
            description={
              isOwner
                ? "Nothing can send events to this project until a key exists. Create one, copy its DSN into your SDK configuration, and the first error will appear in the issue stream."
                : "Nothing can send events to this project until a key exists. An owner needs to create one."
            }
            action={isOwner ? <CreateKeyDialog slug={slug} onCreated={revealCreated} /> : undefined}
          />
        )}

        {state.status === "ready" && state.value.length > 0 && (
          <KeyList keys={state.value} onRevokeRequested={setRevokeTarget} />
        )}

        {state.status === "ready" && platform !== undefined && (
          <SdkSetupCard platform={platform} dsn={usableDsn(state.value, isOwner)} />
        )}
      </div>

      <RevokeKeyDialog
        slug={slug}
        target={revokeTarget}
        onClose={() => setRevokeTarget(null)}
        onRevoked={forgetRevokedKey}
      />
    </>
  );
}

/**
 * The DSN to put in a snippet: an active key's, and only for an owner — the
 * server masks a member's keys, so a snippet built from one would hand back
 * dots, not a working credential.
 */
function usableDsn(keys: readonly Key[], isOwner: boolean): string | null {
  const active = keys.find((key) => key.revoked_at === null);
  return isOwner && active !== undefined ? buildDsn(active, window.location.origin) : null;
}
