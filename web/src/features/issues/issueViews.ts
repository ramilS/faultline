import { api } from "@/apiClient";
import { createIssueViewTracker } from "@/lib/issueViews";

export const issueViews = createIssueViewTracker((issueId) => api.recordIssueView(issueId));
