import { Pencil, Send, Trash2 } from "lucide-react";

import type { NotificationChannel } from "@/api";
import { RelativeTime } from "@/components/RelativeTime";
import { STACKED_ROWS_BELOW_MD } from "@/components/stackedTable";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { channelHealth } from "@/features/settings/notificationChannelFormModel";

interface NotificationChannelListProps {
  channels: readonly NotificationChannel[];
  /** The channel whose test message is in flight, so its button can say so. */
  testingId: number | null;
  onTest: (channel: NotificationChannel) => void;
  onEdit: (channel: NotificationChannel) => void;
  onDelete: (channel: NotificationChannel) => void;
}

export function NotificationChannelList({ channels, testingId, onTest, onEdit, onDelete }: NotificationChannelListProps) {
  return (
    <Table className={STACKED_ROWS_BELOW_MD}>
      <TableHeader>
        <TableRow>
          <TableHead>Channel</TableHead>
          <TableHead>Sends</TableHead>
          <TableHead>Last delivery</TableHead>
          <TableHead className="w-0 text-right">
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {channels.map((channel) => (
          <TableRow key={channel.id}>
            <TableCell className="max-w-0">
              <div className="flex items-center gap-2">
                <span className="truncate font-medium">{channel.name}</span>
                <Badge variant="outline">{channel.kind === "telegram" ? "Telegram" : "Webhook"}</Badge>
                {!channel.enabled && <Badge variant="secondary">Off</Badge>}
              </div>
              <code className="text-muted-foreground block truncate font-mono text-xs" title={channel.target}>
                {channel.target}
              </code>
            </TableCell>
            <TableCell className="text-muted-foreground text-xs">{describeFilters(channel)}</TableCell>
            <TableCell className="max-w-0 text-xs">
              <DeliveryStatus channel={channel} />
            </TableCell>
            <TableCell className="w-0">
              <div className="flex items-center justify-end gap-1">
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  disabled={testingId !== null}
                  onClick={() => onTest(channel)}
                  aria-label={`Send a test message to ${channel.name}`}
                >
                  <Send aria-hidden="true" />
                  {testingId === channel.id ? "Sending…" : "Test"}
                </Button>
                <Button type="button" variant="ghost" size="icon-sm" aria-label={`Edit ${channel.name}`} onClick={() => onEdit(channel)}>
                  <Pencil aria-hidden="true" />
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  className="text-destructive hover:text-destructive"
                  aria-label={`Delete ${channel.name}`}
                  onClick={() => onDelete(channel)}
                >
                  <Trash2 aria-hidden="true" />
                </Button>
              </div>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function describeFilters(channel: NotificationChannel): string {
  const triggers = [channel.notify_new ? "new issues" : null, channel.notify_regression ? "regressions" : null]
    .filter((trigger) => trigger !== null)
    .join(" and ");
  const environments = channel.environments.length === 0 ? "any environment" : channel.environments.join(", ");
  return `${triggers.charAt(0).toUpperCase()}${triggers.slice(1)} · ${channel.min_level} and above · ${environments}`;
}

function DeliveryStatus({ channel }: { channel: NotificationChannel }) {
  const health = channelHealth(channel);
  switch (health.state) {
    case "idle":
      return <span className="text-muted-foreground">Nothing sent yet</span>;
    case "ok":
      return (
        <span className="text-muted-foreground">
          Delivered <RelativeTime iso={health.at} />
        </span>
      );
    case "failing":
      return (
        <span className="text-destructive block truncate" title={health.message}>
          Failed <RelativeTime iso={health.at} />: {health.message}
        </span>
      );
  }
}
