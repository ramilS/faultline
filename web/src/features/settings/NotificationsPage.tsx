import { BellRing, Plus } from "lucide-react";
import { useCallback, useState } from "react";
import { Navigate, useParams } from "react-router-dom";
import { toast } from "sonner";

import type { NotificationChannel } from "@/api";
import { NotFoundPage } from "@/app/NotFoundPage";
import { api } from "@/apiClient";
import { EmptyState } from "@/components/EmptyState";
import { ErrorState } from "@/components/ErrorState";
import { PageHeader } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { messageFor } from "@/errors";
import { DeleteNotificationChannelDialog } from "@/features/settings/DeleteNotificationChannelDialog";
import { NotificationChannelDialog } from "@/features/settings/NotificationChannelDialog";
import { NotificationChannelList } from "@/features/settings/NotificationChannelList";
import { useAsyncResource } from "@/hooks/useAsyncResource";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

export function NotificationsPage() {
  const { slug } = useParams<{ slug: string }>();
  // The route pattern cannot match without a slug; the guard keeps the type
  // honest instead of forcing a non-null assertion downstream.
  return slug === undefined ? <Navigate to="/projects" replace /> : <NotificationsScreen slug={slug} />;
}

function NotificationsScreen({ slug }: { slug: string }) {
  useDocumentTitle(`Notifications · ${slug}`);
  const { state, reload } = useAsyncResource<NotificationChannel[]>(
    useCallback(() => api.listNotificationChannels(slug), [slug])
  );
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<NotificationChannel | null>(null);
  const [deleting, setDeleting] = useState<NotificationChannel | null>(null);
  const [testingId, setTestingId] = useState<number | null>(null);

  const openCreateForm = () => {
    setEditing(null);
    setFormOpen(true);
  };

  const openEditForm = (channel: NotificationChannel) => {
    setEditing(channel);
    setFormOpen(true);
  };

  const closeForm = () => {
    setFormOpen(false);
    setEditing(null);
  };

  const saveAndClose = async () => {
    closeForm();
    await reload({ silent: true });
  };

  // A test reports through a toast: it changes nothing stored, and the
  // destination's own refusal (a wrong chat id, a revoked token) is exactly
  // what the owner needs to read.
  const sendTest = async (channel: NotificationChannel) => {
    setTestingId(channel.id);
    try {
      await api.testNotificationChannel(slug, channel.id);
      toast.success(`Test message sent to ${channel.name}.`);
    } catch (caught) {
      toast.error(`Test message to ${channel.name} failed: ${messageFor(caught)}`);
    } finally {
      setTestingId(null);
    }
  };

  if (state.status === "failed" && state.notFound) {
    return <NotFoundPage />;
  }

  return (
    <>
      <PageHeader
        title="Notifications"
        description="Get a Telegram message or a webhook call when an issue first appears or a resolved one comes back."
      >
        <Button size="sm" onClick={openCreateForm}>
          <Plus aria-hidden="true" />
          New channel
        </Button>
      </PageHeader>

      <div className="space-y-4 p-4 sm:p-6">
        {state.status === "loading" && (
          <div className="space-y-2">
            {[0, 1].map((row) => (
              <Skeleton key={row} className="h-14 w-full" />
            ))}
          </div>
        )}

        {state.status === "failed" && <ErrorState message={state.message} onRetry={() => void reload()} />}

        {state.status === "ready" && state.value.length === 0 && (
          <EmptyState
            icon={BellRing}
            title="No notification channels yet"
            description="Add a Telegram chat or a webhook, and Faultline will post there when this project gets a new issue or a regression. Filters per channel keep staging noise out of the production chat."
            action={
              <Button size="sm" onClick={openCreateForm}>
                <Plus aria-hidden="true" />
                Add the first channel
              </Button>
            }
          />
        )}

        {state.status === "ready" && state.value.length > 0 && (
          <NotificationChannelList
            channels={state.value}
            testingId={testingId}
            onTest={(channel) => void sendTest(channel)}
            onEdit={openEditForm}
            onDelete={setDeleting}
          />
        )}
      </div>

      <NotificationChannelDialog slug={slug} open={formOpen} channel={editing} onClose={closeForm} onSaved={saveAndClose} />

      <DeleteNotificationChannelDialog slug={slug} target={deleting} onClose={() => setDeleting(null)} onDeleted={reload} />
    </>
  );
}
