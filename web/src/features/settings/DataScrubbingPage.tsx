import { useCallback, useState, type FormEvent } from "react";
import { Navigate, useParams } from "react-router-dom";

import type { DataScrubbing } from "@/api";
import { NotFoundPage } from "@/app/NotFoundPage";
import { api } from "@/apiClient";
import { ErrorState } from "@/components/ErrorState";
import { PageHeader } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { useSession } from "@/features/auth/SessionProvider";
import { ScrubRulesEditor } from "@/features/settings/ScrubRulesEditor";
import { formatFieldList, parseFieldList } from "@/features/settings/scrubFields";
import { validateRules } from "@/features/settings/scrubRules";
import { useAsyncResource } from "@/hooks/useAsyncResource";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import { useSaveForm } from "@/hooks/useSaveForm";

export function DataScrubbingPage() {
  const { slug } = useParams<{ slug: string }>();
  return slug === undefined ? <Navigate to="/projects" replace /> : <DataScrubbingScreen slug={slug} />;
}

function DataScrubbingScreen({ slug }: { slug: string }) {
  const { session } = useSession();
  useDocumentTitle(`Data scrubbing · ${slug}`);
  const isOwner = session.state === "authenticated" && session.user.role === "owner";
  const { state, reload, replace } = useAsyncResource<DataScrubbing>(
    useCallback(() => api.getDataScrubbing(slug), [slug])
  );

  if (state.status === "failed" && state.notFound) {
    return <NotFoundPage />;
  }

  return (
    <>
      <PageHeader
        title="Data scrubbing"
        description={
          isOwner
            ? "Redact passwords, tokens and card numbers from events before they are stored."
            : "Redact passwords, tokens and card numbers from events before they are stored. Scrubbing is configured by an owner."
        }
      />
      <div className="space-y-4 p-4 sm:p-6">
        {state.status === "loading" && <Skeleton className="h-48 w-full" />}
        {state.status === "failed" && <ErrorState message={state.message} onRetry={() => void reload()} />}
        {state.status === "ready" && (
          <ScrubbingForm slug={slug} initial={state.value} editable={isOwner} onSaved={replace} />
        )}
      </div>
    </>
  );
}

interface ScrubbingFormProps {
  slug: string;
  initial: DataScrubbing;
  editable: boolean;
  onSaved: (saved: DataScrubbing) => void;
}

function ScrubbingForm({ slug, initial, editable, onSaved }: ScrubbingFormProps) {
  const [enabled, setEnabled] = useState(initial.enabled);
  const [extra, setExtra] = useState(formatFieldList(initial.extra_fields));
  const [safe, setSafe] = useState(formatFieldList(initial.safe_fields));
  const [rules, setRules] = useState(initial.rules);
  const { submitting, error, saved, setError, touch, submit: save } = useSaveForm();

  const edit = <T,>(setter: (value: T) => void) => (value: T) => {
    setter(value);
    touch();
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const extraFields = parseFieldList(extra);
    const safeFields = parseFieldList(safe);
    if (!extraFields.ok || !safeFields.ok) {
      setError(!extraFields.ok ? `Additional sensitive fields: ${extraFields.error}` : `Safe fields: ${safeFields.ok ? "" : safeFields.error}`);
      return;
    }
    const invalidRules = validateRules(rules);
    if (invalidRules !== null) {
      setError(invalidRules);
      return;
    }
    await save(async () => {
      const stored = await api.updateDataScrubbing(slug, {
        enabled,
        extra_fields: extraFields.fields,
        safe_fields: safeFields.fields,
        rules
      });
      onSaved(stored);
    });
  };

  return (
    <form onSubmit={(event) => void submit(event)} className="max-w-2xl space-y-6">
      <div className="flex items-start gap-3">
        <Checkbox
          id="scrub-enabled"
          checked={enabled}
          disabled={!editable}
          onCheckedChange={(checked) => edit(setEnabled)(checked === true)}
        />
        <div className="space-y-1">
          <Label htmlFor="scrub-enabled">Scrub sensitive data</Label>
          <p className="text-muted-foreground text-sm">
            Values whose key contains password, secret, passwd, api_key, apikey, auth, credentials, mysql_pwd,
            privatekey, private_key, token, bearer, cookie or api-key are replaced with [Filtered], as are card
            numbers and the event&apos;s user.ip_address unless the project stores user IPs. Applies to events received
            after the change, within about 30 seconds. Events already stored are not rewritten.
          </p>
        </div>
      </div>

      <FieldListInput
        id="scrub-extra"
        label="Additional sensitive fields"
        hint="One name per line. A key containing any of these is scrubbed too."
        value={extra}
        disabled={!editable || !enabled}
        onChange={edit(setExtra)}
      />
      <FieldListInput
        id="scrub-safe"
        label="Safe fields"
        hint="One name per line. A key matching one of these exactly is never scrubbed."
        value={safe}
        disabled={!editable || !enabled}
        onChange={edit(setSafe)}
      />

      <ScrubRulesEditor rules={rules} editable={editable} disabled={!enabled} onChange={edit(setRules)} />
      <p className="text-muted-foreground text-sm">
        Instance-wide rules set by an owner under Settings → Data scrubbing also apply to this project, even when
        scrubbing is switched off here.
      </p>

      {error !== null && (
        <p role="alert" className="text-destructive text-sm">
          {error}
        </p>
      )}
      {saved && (
        <p role="status" className="text-sm">
          Saved.
        </p>
      )}
      {editable && (
        <Button type="submit" disabled={submitting}>
          {submitting ? "Saving…" : "Save"}
        </Button>
      )}
    </form>
  );
}

interface FieldListInputProps {
  id: string;
  label: string;
  hint: string;
  value: string;
  disabled: boolean;
  onChange: (value: string) => void;
}

function FieldListInput({ id, label, hint, value, disabled, onChange }: FieldListInputProps) {
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{label}</Label>
      <textarea
        id={id}
        value={value}
        disabled={disabled}
        rows={4}
        onChange={(event) => onChange(event.target.value)}
        className="border-input bg-background focus-visible:ring-ring/50 w-full rounded-md border px-3 py-2 font-mono text-sm shadow-xs outline-none focus-visible:ring-[3px] disabled:cursor-not-allowed disabled:opacity-50"
      />
      <p className="text-muted-foreground text-sm">{hint}</p>
    </div>
  );
}
