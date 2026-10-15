import { useState, type FormEvent, type ReactNode } from "react";

import type { NotificationChannel, NotificationChannelKind, NotificationLevel } from "@/api";
import { api } from "@/apiClient";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { DialogFooter } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { messageFor } from "@/errors";
import {
  channelFormErrors,
  hasChannelFormError,
  initialChannelFormValues,
  NO_CHANNEL_FORM_ERRORS,
  NOTIFICATION_LEVELS,
  toChannelInput,
  type ChannelFormValues
} from "@/features/settings/notificationChannelFormModel";

const KIND_LABELS: Record<NotificationChannelKind, string> = { telegram: "Telegram", webhook: "Webhook" };

interface NotificationChannelFormProps {
  slug: string;
  /** null creates a channel; a channel edits it in place. */
  channel: NotificationChannel | null;
  onSaved: () => Promise<void>;
  onCancel: () => void;
}

export function NotificationChannelForm({ slug, channel, onSaved, onCancel }: NotificationChannelFormProps) {
  const isEditing = channel !== null;
  const [values, setValues] = useState<ChannelFormValues>(() => initialChannelFormValues(channel));
  const [errors, setErrors] = useState(NO_CHANNEL_FORM_ERRORS);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const update = (changes: Partial<ChannelFormValues>) => {
    setValues((current) => ({ ...current, ...changes }));
    setErrors(NO_CHANNEL_FORM_ERRORS);
    setSubmitError(null);
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const found = channelFormErrors(values, isEditing);
    if (hasChannelFormError(found)) {
      setErrors(found);
      return;
    }
    const input = toChannelInput(values, isEditing);
    setSubmitting(true);
    setSubmitError(null);
    try {
      if (channel === null) {
        await api.createNotificationChannel(slug, input);
      } else {
        await api.updateNotificationChannel(slug, channel.id, input);
      }
      await onSaved();
    } catch (caught) {
      setSubmitError(messageFor(caught));
    } finally {
      setSubmitting(false);
    }
  };

  const isTelegram = values.kind === "telegram";
  const keepSecretHint = isEditing && channel.has_secret ? "Leave blank to keep the current one." : undefined;

  return (
    <form onSubmit={submit} noValidate>
      <div className="space-y-4 py-4">
        {submitError !== null && (
          <p role="alert" className="text-destructive text-sm">
            {submitError}
          </p>
        )}

        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <Label htmlFor="channel-kind">Type</Label>
            <Select
              value={values.kind}
              disabled={isEditing}
              onValueChange={(value) => update({ kind: value as NotificationChannelKind, target: "", secret: "" })}
            >
              <SelectTrigger id="channel-kind" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {(Object.keys(KIND_LABELS) as NotificationChannelKind[]).map((kind) => (
                  <SelectItem key={kind} value={kind}>
                    {KIND_LABELS[kind]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <Field id="channel-name" label="Name" error={errors.name}>
            <Input
              id="channel-name"
              value={values.name}
              onChange={(event) => update({ name: event.target.value })}
              placeholder={isTelegram ? "Backend on-call" : "Incident bot"}
              aria-invalid={errors.name !== null}
              aria-describedby={errors.name === null ? undefined : "channel-name-error"}
            />
          </Field>
        </div>

        <Field
          id="channel-target"
          label={isTelegram ? "Chat ID" : "URL"}
          error={errors.target}
          hint={
            isTelegram
              ? "A numeric id such as -1001234567890, or @username for a public channel. Add the bot to the chat first."
              : undefined
          }
        >
          <Input
            id="channel-target"
            value={values.target}
            onChange={(event) => update({ target: event.target.value })}
            placeholder={isTelegram ? "-1001234567890" : "https://hooks.example.com/faultline"}
            className="font-mono"
            aria-invalid={errors.target !== null}
            aria-describedby={describedBy("channel-target", errors.target, isTelegram)}
          />
        </Field>

        <Field
          id="channel-secret"
          label={isTelegram ? "Bot token" : "Signing secret (optional)"}
          error={errors.secret}
          hint={
            keepSecretHint ??
            (isTelegram
              ? "Issued by @BotFather. Stored on the server and never shown again."
              : "Signs each request: X-Faultline-Signature is sha256= followed by the hex HMAC-SHA256 of the X-Faultline-Timestamp value, a dot, and the body.")
          }
        >
          <Input
            id="channel-secret"
            type="password"
            autoComplete="off"
            value={values.secret}
            onChange={(event) => update({ secret: event.target.value })}
            placeholder={keepSecretHint === undefined ? undefined : "••••••••"}
            className="font-mono"
            aria-invalid={errors.secret !== null}
            aria-describedby={describedBy("channel-secret", errors.secret, true)}
          />
        </Field>

        <fieldset className="space-y-2">
          <legend className="text-sm font-medium">Notify about</legend>
          <CheckboxField
            id="channel-notify-new"
            label="New issues"
            checked={values.notifyNew}
            onChange={(checked) => update({ notifyNew: checked })}
          />
          <CheckboxField
            id="channel-notify-regression"
            label="Regressions — a resolved issue seen again"
            checked={values.notifyRegression}
            onChange={(checked) => update({ notifyRegression: checked })}
          />
          {errors.triggers !== null && (
            <p role="alert" className="text-destructive text-xs">
              {errors.triggers}
            </p>
          )}
        </fieldset>

        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <Label htmlFor="channel-min-level">Minimum level</Label>
            <Select value={values.minLevel} onValueChange={(value) => update({ minLevel: value as NotificationLevel })}>
              <SelectTrigger id="channel-min-level" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {NOTIFICATION_LEVELS.map((level) => (
                  <SelectItem key={level} value={level}>
                    {level}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <Field id="channel-environments" label="Environments" error={null} hint="Comma-separated. Blank means all.">
            <Input
              id="channel-environments"
              value={values.environments}
              onChange={(event) => update({ environments: event.target.value })}
              placeholder="production"
              className="font-mono"
              aria-describedby="channel-environments-hint"
            />
          </Field>
        </div>

        <CheckboxField
          id="channel-enabled"
          label="Enabled"
          checked={values.enabled}
          onChange={(checked) => update({ enabled: checked })}
        />
      </div>

      <DialogFooter>
        <Button type="button" variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" disabled={submitting}>
          {submitting ? "Saving…" : isEditing ? "Save channel" : "Add channel"}
        </Button>
      </DialogFooter>
    </form>
  );
}

function describedBy(id: string, error: string | null, hasHint: boolean): string | undefined {
  const ids = [error === null ? null : `${id}-error`, hasHint ? `${id}-hint` : null].filter((value) => value !== null);
  return ids.length === 0 ? undefined : ids.join(" ");
}

interface FieldProps {
  id: string;
  label: string;
  error: string | null;
  hint?: string;
  children: ReactNode;
}

function Field({ id, label, error, hint, children }: FieldProps) {
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {hint !== undefined && (
        <p id={`${id}-hint`} className="text-muted-foreground text-xs">
          {hint}
        </p>
      )}
      {error !== null && (
        <p id={`${id}-error`} role="alert" className="text-destructive text-xs">
          {error}
        </p>
      )}
    </div>
  );
}

interface CheckboxFieldProps {
  id: string;
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}

function CheckboxField({ id, label, checked, onChange }: CheckboxFieldProps) {
  return (
    <div className="flex items-center gap-2">
      {/* Radix renders a button, which takes its accessible name from its
          content rather than from an associated <label>. */}
      <Checkbox id={id} aria-labelledby={`${id}-label`} checked={checked} onCheckedChange={(next) => onChange(next === true)} />
      <Label id={`${id}-label`} htmlFor={id} className="font-normal">
        {label}
      </Label>
    </div>
  );
}
