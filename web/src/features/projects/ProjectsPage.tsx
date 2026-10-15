import { FolderPlus, Plus } from "lucide-react";
import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";

import { api } from "@/apiClient";
import { EmptyState } from "@/components/EmptyState";
import { ErrorState } from "@/components/ErrorState";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger
} from "@/components/ui/dialog";
import { messageFor } from "@/errors";
import { useSession } from "@/features/auth/SessionProvider";
import { useProjects } from "@/features/projects/ProjectsProvider";
import { PlatformSelect } from "@/features/projects/PlatformSelect";
import { PLATFORMS, platformLabel, type Platform } from "@/features/projects/platforms";
import { projectsEmptyState } from "@/features/projects/projectsEmptyState";
import { PageHeader } from "@/components/PageHeader";
import { MAX_SLUG_LENGTH, slugFromName } from "@/lib/projectSlug";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

export function ProjectsPage() {
  const { session } = useSession();
  const isOwner = session.state === "authenticated" && session.user.role === "owner";
  const { state, reload } = useProjects();
  const emptyState = projectsEmptyState(isOwner ? "owner" : "member");
  useDocumentTitle("Projects");

  return (
    <>
      <PageHeader
        title="Projects"
        description={
          isOwner
            ? "Each project has its own DSN, grouping rules, and issues."
            : "Each project has its own DSN, grouping rules, and issues. Projects are created by an owner."
        }
      >
        {isOwner && <CreateProjectDialog onCreated={reload} />}
      </PageHeader>

      <div className="p-4 sm:p-6">
        {state.status === "loading" && (
          <div className="space-y-2">
            {[0, 1, 2].map((row) => (
              <Skeleton key={row} className="h-16 w-full" />
            ))}
          </div>
        )}

        {state.status === "failed" && <ErrorState message={state.message} onRetry={() => void reload()} />}

        {state.status === "ready" && state.projects.length === 0 && (
          <EmptyState
            icon={FolderPlus}
            title={emptyState.title}
            description={emptyState.description}
            action={emptyState.canCreate ? <CreateProjectDialog onCreated={reload} /> : undefined}
          />
        )}

        {state.status === "ready" && state.projects.length > 0 && (
          <ul className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
            {state.projects.map((project) => (
              <li key={project.id}>
                <Link
                  to={`/${project.slug}/issues`}
                  className="hover:border-foreground/20 hover:bg-accent/40 block rounded-lg border p-4 transition-colors"
                >
                  <span className="block truncate font-medium">{project.name}</span>
                  <span className="text-muted-foreground mt-0.5 block truncate font-mono text-xs">
                    {project.slug}
                  </span>
                  <span className="text-muted-foreground mt-3 block text-xs">{platformLabel(project.platform)}</span>
                </Link>
              </li>
            ))}
          </ul>
        )}
      </div>
    </>
  );
}

function CreateProjectDialog({ onCreated }: { onCreated: () => Promise<void> }) {
  const [open, setOpen] = useState(false);
  const navigate = useNavigate();
  const [slug, setSlug] = useState("");
  // Until the user types in the slug field, it is a suggestion that follows the
  // name; after that it is theirs, and a later name edit must not overwrite it.
  const [slugEdited, setSlugEdited] = useState(false);
  const [name, setName] = useState("");
  const [platform, setPlatform] = useState<Platform>(PLATFORMS[0].value);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      const created = await api.createProject({ slug, name, platform });
      await onCreated();
      setSlug("");
      setSlugEdited(false);
      setName("");
      setPlatform(PLATFORMS[0].value);
      setOpen(false);
      // A project cannot receive events until it has a key, so that is the next
      // thing anyone creating one needs.
      void navigate(`/${created.slug}/settings/keys`);
    } catch (caught) {
      setError(messageFor(caught));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus className="size-4" aria-hidden="true" />
          New project
        </Button>
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>New project</DialogTitle>
            <DialogDescription>The slug appears in URLs and cannot be changed later.</DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-4">
            {error !== null && (
              <p role="alert" className="text-level-error text-sm">
                {error}
              </p>
            )}

            <div className="space-y-2">
              <Label htmlFor="project-name">Name</Label>
              <Input
                id="project-name"
                value={name}
                onChange={(event) => {
                  setName(event.target.value);
                  if (!slugEdited) {
                    setSlug(slugFromName(event.target.value));
                  }
                }}
                required
                autoFocus
              />
            </div>

            <div className="space-y-2">
              <Label htmlFor="project-slug">Slug</Label>
              <Input
                id="project-slug"
                value={slug}
                onChange={(event) => {
                  setSlug(event.target.value);
                  setSlugEdited(event.target.value !== "");
                }}
                required
                maxLength={MAX_SLUG_LENGTH}
                pattern="[a-z0-9]+(-[a-z0-9]+)*"
                title="Lowercase letters, digits and single hyphens"
                aria-describedby="project-slug-hint"
                className="font-mono"
              />
              <p id="project-slug-hint" className="text-muted-foreground text-xs">
                Lowercase letters, digits and hyphens.
              </p>
            </div>

            <div className="space-y-2">
              <Label htmlFor="project-platform">Platform</Label>
              <PlatformSelect id="project-platform" value={platform} onChange={setPlatform} />
            </div>
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={submitting}>
              {submitting ? "Creating…" : "Create project"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
