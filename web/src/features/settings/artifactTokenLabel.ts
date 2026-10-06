/** Enough characters to tell two tokens of one project apart at a glance. */
const SHOWN_PREFIX_LENGTH = 12;

/**
 * The server's token prefix can run to dozens of characters, which drowns the
 * name it is there to disambiguate. The trailing ellipsis says it is a prefix,
 * not the whole token.
 */
export function tokenPrefixLabel(prefix: string): string {
  return `${prefix.slice(0, SHOWN_PREFIX_LENGTH)}…`;
}
