import { FileQuestion } from "lucide-react";
import { Link } from "react-router-dom";

import { EmptyState } from "@/components/EmptyState";
import { Button } from "@/components/ui/button";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

/**
 * The dashboard's one not-found screen. It renders for two distinct causes
 * that must look identical to the caller: a URL that matches no route at all
 * (the router's catch-all, below), and a URL that matches a route but whose
 * project the caller cannot reach — the API answers that with a 404 too
 * (spec: "a deep link to an inaccessible project resolves to the existing
 * not-found route"), and a slug-keyed page renders this in place rather than
 * navigating, so the unreachable URL stays intact to paste or bookmark.
 */
export function NotFoundPage() {
  useDocumentTitle("Page not found");
  return (
    <EmptyState
      icon={FileQuestion}
      title="Page not found"
      description="That URL does not match any screen in the dashboard."
      action={
        <Button asChild variant="outline" size="sm">
          <Link to="/projects">Back to projects</Link>
        </Button>
      }
    />
  );
}
