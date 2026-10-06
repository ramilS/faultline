const MAX_LENGTH = 512;

/**
 * Validates a `?next=` redirect target.
 *
 * Returns the path when it is a same-origin absolute path, and `null`
 * otherwise. Callers must treat `null` as "navigate to the default landing
 * route" and must never fall back to the raw input.
 *
 * The rejections that matter are the ones that still *look* like a path:
 * `//evil.com` is a protocol-relative URL, and browsers normalise a backslash
 * to a forward slash, so `/\evil.com` is protocol-relative too. Checking only
 * the first character is the classic way this bug ships.
 */
export function safeNextPath(raw: string | null): string | null {
  if (raw === null || raw.length === 0 || raw.length > MAX_LENGTH) {
    return null;
  }

  // Control characters can smuggle a scheme past a naive prefix check once a
  // browser strips them during URL parsing.
  if (/[\u0000-\u001f\u007f]/.test(raw)) {
    return null;
  }

  const separatorsNormalised = raw.replaceAll("\\", "/");
  if (!separatorsNormalised.startsWith("/") || separatorsNormalised.startsWith("//")) {
    return null;
  }

  return raw;
}
