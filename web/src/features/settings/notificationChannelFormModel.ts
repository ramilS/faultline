import type { NotificationChannel, NotificationChannelInput, NotificationChannelKind, NotificationLevel } from "../../api.ts";

export const NOTIFICATION_LEVELS: readonly NotificationLevel[] = ["debug", "info", "warning", "error", "fatal"];

export interface ChannelFormValues {
  name: string;
  kind: NotificationChannelKind;
  target: string;
  /** Blank on edit means "keep the stored secret". */
  secret: string;
  enabled: boolean;
  notifyNew: boolean;
  notifyRegression: boolean;
  /** Comma- or newline-separated; blank means every environment. */
  environments: string;
  minLevel: NotificationLevel;
}

export interface ChannelFormErrors {
  name: string | null;
  target: string | null;
  secret: string | null;
  triggers: string | null;
}

export const NO_CHANNEL_FORM_ERRORS: ChannelFormErrors = { name: null, target: null, secret: null, triggers: null };

export function initialChannelFormValues(channel: NotificationChannel | null): ChannelFormValues {
  if (channel === null) {
    return {
      name: "",
      kind: "telegram",
      target: "",
      secret: "",
      enabled: true,
      notifyNew: true,
      notifyRegression: true,
      environments: "",
      minLevel: "error"
    };
  }
  return {
    name: channel.name,
    kind: channel.kind,
    target: channel.target,
    secret: "",
    enabled: channel.enabled,
    notifyNew: channel.notify_new,
    notifyRegression: channel.notify_regression,
    environments: channel.environments.join(", "),
    minLevel: channel.min_level
  };
}

export function parseEnvironments(text: string): string[] {
  const environments: string[] = [];
  for (const part of text.split(/[,\n]/)) {
    const environment = part.trim();
    if (environment !== "" && !environments.includes(environment)) {
      environments.push(environment);
    }
  }
  return environments;
}

/**
 * Catches what the person can fix before a round trip. The server validates
 * everything again and its message is shown as-is, so these checks only need
 * to cover the common slips, not every rule.
 */
export function channelFormErrors(values: ChannelFormValues, isEditing: boolean): ChannelFormErrors {
  const errors: ChannelFormErrors = { ...NO_CHANNEL_FORM_ERRORS };
  if (values.name.trim() === "") {
    errors.name = "Give the channel a name.";
  }
  if (!values.notifyNew && !values.notifyRegression) {
    errors.triggers = "Choose at least one of new issues and regressions.";
  }
  const target = values.target.trim();
  if (values.kind === "telegram") {
    if (target === "") {
      errors.target = "Enter the chat id or @channel username.";
    }
    if (!isEditing && values.secret.trim() === "") {
      errors.secret = "Enter the bot token from @BotFather.";
    }
  } else if (!/^https?:\/\/[^/\s]+/i.test(target)) {
    errors.target = "Enter an http:// or https:// URL.";
  }
  return errors;
}

export function hasChannelFormError(errors: ChannelFormErrors): boolean {
  return Object.values(errors).some((error) => error !== null);
}

/** Leaves `secret` out on edit when blank, so the server keeps the stored one. */
export function toChannelInput(values: ChannelFormValues, isEditing: boolean): NotificationChannelInput {
  const input: NotificationChannelInput = {
    name: values.name.trim(),
    kind: values.kind,
    target: values.target.trim(),
    enabled: values.enabled,
    notify_new: values.notifyNew,
    notify_regression: values.notifyRegression,
    environments: parseEnvironments(values.environments),
    min_level: values.minLevel
  };
  const secret = values.secret.trim();
  if (secret !== "" || !isEditing) {
    input.secret = secret;
  }
  return input;
}

export type ChannelHealth =
  | { state: "idle" }
  | { state: "ok"; at: string }
  | { state: "failing"; at: string; message: string };

/**
 * The latest delivery outcome. An error newer than the last success means the
 * channel is currently failing; one older than it was recovered from.
 */
export function channelHealth(channel: NotificationChannel): ChannelHealth {
  const errorAt = channel.last_error_at;
  const successAt = channel.last_success_at;
  if (channel.last_error !== null && errorAt !== null && (successAt === null || Date.parse(errorAt) > Date.parse(successAt))) {
    return { state: "failing", at: errorAt, message: channel.last_error };
  }
  if (successAt !== null) {
    return { state: "ok", at: successAt };
  }
  return { state: "idle" };
}
