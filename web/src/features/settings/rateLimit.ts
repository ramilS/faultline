import type { CreateKeyInput } from "@/api";

// Mirrors the server bounds in internal/project/model.go. Checking them here
// turns an opaque 400 ("invalid key rate limit rps") into a message beside the
// field that caused it.
export const MAX_RATE_LIMIT_RPS = 10_000;
export const MAX_RATE_LIMIT_BURST = 100_000;

export const DEFAULT_RATE_LIMIT_RPS = 200;
export const DEFAULT_RATE_LIMIT_BURST = 1_000;

export interface RateLimitErrors {
  readonly rps?: string;
  readonly burst?: string;
}

export type RateLimitResult =
  | { readonly ok: true; readonly value: CreateKeyInput }
  | { readonly ok: false; readonly errors: RateLimitErrors };

export function parseRateLimit(rps: string, burst: string): RateLimitResult {
  const rpsError = boundsError(rps, MAX_RATE_LIMIT_RPS);
  const burstError = boundsError(burst, MAX_RATE_LIMIT_BURST);

  if (rpsError !== null || burstError !== null) {
    return {
      ok: false,
      errors: {
        ...(rpsError === null ? {} : { rps: rpsError }),
        ...(burstError === null ? {} : { burst: burstError })
      }
    };
  }

  return {
    ok: true,
    value: { rate_limit_rps: Number(rps.trim()), rate_limit_burst: Number(burst.trim()) }
  };
}

function boundsError(raw: string, max: number): string | null {
  const trimmed = raw.trim();
  if (!/^\d+$/.test(trimmed)) {
    return "Enter a whole number.";
  }
  const parsed = Number(trimmed);
  if (parsed < 1 || parsed > max) {
    return `Enter a number between 1 and ${max.toLocaleString("en-US")}.`;
  }
  return null;
}
