import type { IssueSummary } from "../api.ts";

type Labelled = Pick<IssueSummary, "title" | "culprit" | "display_title" | "display_culprit">;

export function issueTitle(issue: Labelled): string {
  return issue.display_title || issue.title;
}

export function issueCulprit(issue: Labelled): string {
  return issue.display_culprit || issue.culprit;
}
