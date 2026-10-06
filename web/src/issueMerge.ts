// Merging is not fully reversible: the source issue's events and tag history
// move to the target, and the source becomes a tombstone. This message is the
// one place that consequence is spelled out, so the confirm step and its test
// stay in sync.
export function mergeConfirmationMessage(sourceTimesSeen: number, targetTitle: string): string {
  const eventWord = sourceTimesSeen === 1 ? "event" : "events";
  return (
    `This moves ${sourceTimesSeen} ${eventWord} and all tag history from this issue into "${targetTitle}". ` +
    `This issue becomes a redirect to "${targetTitle}" and cannot be split apart again.`
  );
}
