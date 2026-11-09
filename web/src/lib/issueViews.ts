export interface IssueViewTracker {
  readonly record: (issueId: number) => void;
  readonly settled: () => Promise<void>;
}

export function createIssueViewTracker(post: (issueId: number) => Promise<void>): IssueViewTracker {
  const inFlight = new Set<Promise<void>>();
  return {
    record: (issueId) => {
      const request: Promise<void> = post(issueId)
        .catch(() => undefined)
        .then(() => {
          inFlight.delete(request);
        });
      inFlight.add(request);
    },
    settled: async () => {
      await Promise.all(inFlight);
    }
  };
}
