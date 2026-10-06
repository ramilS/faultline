import { useCallback, useEffect, useRef, useState } from "react";

import { isNotFound, messageFor } from "@/errors";

export type AsyncState<T> =
  | { readonly status: "loading" }
  | { readonly status: "ready"; readonly value: T }
  | {
      readonly status: "failed";
      readonly message: string;
      /** The failure is the access-boundary 404 — render the not-found route, not Retry. */
      readonly notFound: boolean;
    };

export interface ReloadOptions {
  /**
   * Refresh without dropping back to the loading state. Use after a mutation,
   * where the screen already shows valid data and a skeleton flash would read
   * as the content disappearing.
   */
  silent?: boolean;
}

export interface AsyncResource<T> {
  readonly state: AsyncState<T>;
  reload: (options?: ReloadOptions) => Promise<void>;
  /**
   * Adopts a value the caller already holds — a mutation's own response, say.
   * Refetching what the server just returned would be a wasted round trip.
   */
  replace: (value: T) => void;
}

/**
 * Loads one value into the three states every screen renders: loading, ready,
 * and failed-with-retry.
 *
 * `load` must be referentially stable (wrap it in `useCallback`); its identity
 * is what marks a request as new rather than a repeat.
 */
export function useAsyncResource<T>(load: () => Promise<T>): AsyncResource<T> {
  const [state, setState] = useState<AsyncState<T>>({ status: "loading" });

  // Monotonic request id rather than a per-effect boolean: `reload` is called
  // from outside the effect, and a superseded response must not land on top of
  // a newer one whichever path started it.
  const latestRequest = useRef(0);

  const run = useCallback(
    async (showLoading: boolean) => {
      const request = ++latestRequest.current;
      if (showLoading) {
        setState({ status: "loading" });
      }
      try {
        const value = await load();
        if (request === latestRequest.current) {
          setState({ status: "ready", value });
        }
      } catch (caught) {
        if (request === latestRequest.current) {
          setState({ status: "failed", message: messageFor(caught), notFound: isNotFound(caught) });
        }
      }
    },
    [load]
  );

  useEffect(() => {
    void run(true);
    // Invalidate anything in flight when the loader changes or the caller
    // unmounts, so a late response cannot resurrect a stale screen.
    return () => {
      latestRequest.current += 1;
    };
  }, [run]);

  const reload = useCallback((options?: ReloadOptions) => run(options?.silent !== true), [run]);
  const replace = useCallback((value: T) => setState({ status: "ready", value }), []);

  return { state, reload, replace };
}
