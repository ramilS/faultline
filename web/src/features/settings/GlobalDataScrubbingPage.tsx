import { useCallback, useState, type FormEvent } from "react";

import type { GlobalScrubbing } from "@/api";
import { api } from "@/apiClient";
import { ErrorState } from "@/components/ErrorState";
import { PageHeader } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { ScrubRulesEditor } from "@/features/settings/ScrubRulesEditor";
import { validateRules } from "@/features/settings/scrubRules";
import { useAsyncResource } from "@/hooks/useAsyncResource";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import { useSaveForm } from "@/hooks/useSaveForm";

export function GlobalDataScrubbingPage() {
  useDocumentTitle("Data scrubbing · all projects");
  const { state, reload, replace } = useAsyncResource<GlobalScrubbing>(
    useCallback(() => api.getGlobalScrubbing(), [])
  );

  return (
    <>
      <PageHeader
        title="Data scrubbing"
        description="Rules applied to every project, including projects that switch scrubbing off. Events received after a change are scrubbed within about 30 seconds."
      />
      <div className="space-y-4 p-4 sm:p-6">
        {state.status === "loading" && <Skeleton className="h-48 w-full" />}
        {state.status === "failed" && <ErrorState message={state.message} onRetry={() => void reload()} />}
        {state.status === "ready" && <GlobalScrubbingForm initial={state.value} onSaved={replace} />}
      </div>
    </>
  );
}

interface GlobalScrubbingFormProps {
  initial: GlobalScrubbing;
  onSaved: (saved: GlobalScrubbing) => void;
}

function GlobalScrubbingForm({ initial, onSaved }: GlobalScrubbingFormProps) {
  const [rules, setRules] = useState(initial.rules);
  const { submitting, error, saved, setError, touch, submit: save } = useSaveForm();

  const edit = (value: typeof rules) => {
    setRules(value);
    touch();
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const invalidRules = validateRules(rules);
    if (invalidRules !== null) {
      setError(invalidRules);
      return;
    }
    await save(async () => {
      onSaved(await api.updateGlobalScrubbing({ rules }));
    });
  };

  return (
    <form onSubmit={(event) => void submit(event)} className="max-w-2xl space-y-6">
      <ScrubRulesEditor rules={rules} editable disabled={false} onChange={edit} />

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
      <Button type="submit" disabled={submitting}>
        {submitting ? "Saving…" : "Save"}
      </Button>
    </form>
  );
}
