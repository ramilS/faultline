import { FileQuestion } from "lucide-react";
import { Link } from "react-router-dom";

import { EmptyState } from "@/components/EmptyState";
import { Button } from "@/components/ui/button";

interface NotAnIssueProps {
  /** The project the URL named, or undefined when it named none. */
  slug: string | undefined;
  /** The route segment that was supposed to be an issue id. */
  issueId: string | undefined;
}

/** What both issue screens show for a URL whose issue id is not one. */
export function NotAnIssue({ slug, issueId }: NotAnIssueProps) {
  return (
    <EmptyState
      icon={FileQuestion}
      title="Not an issue"
      description={`"${issueId ?? ""}" is not a valid issue id.`}
      action={
        slug === undefined ? undefined : (
          <Button asChild variant="outline" size="sm">
            <Link to={`/${slug}/issues`}>Back to issues</Link>
          </Button>
        )
      }
    />
  );
}
