import { useState } from "react";

import { messageFor } from "@/errors";

export interface SaveForm {
  submitting: boolean;
  error: string | null;
  saved: boolean;
  setError: (message: string) => void;
  /** Call on every edit: a stale "Saved." or error no longer describes the form. */
  touch: () => void;
  submit: (action: () => Promise<void>) => Promise<void>;
}

export function useSaveForm(): SaveForm {
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const touch = () => {
    setSaved(false);
    setError(null);
  };

  const submit = async (action: () => Promise<void>) => {
    setSubmitting(true);
    setError(null);
    try {
      await action();
      setSaved(true);
    } catch (failure) {
      setError(messageFor(failure));
    } finally {
      setSubmitting(false);
    }
  };

  return { submitting, error, saved, setError, touch, submit };
}
