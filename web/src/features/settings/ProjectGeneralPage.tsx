import { ProjectIdentityEditor, validProjectEmoji } from "@/features/projects/ProjectIdentity";
import { useRef, useState, type FormEvent } from "react";
import { Navigate, useParams } from "react-router-dom";

import type { Project } from "@/api";
import { NotFoundPage } from "@/app/NotFoundPage";
import { api } from "@/apiClient";
import { ErrorState } from "@/components/ErrorState";
import { PageHeader } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { useSession } from "@/features/auth/SessionProvider";
import { PlatformSelect } from "@/features/projects/PlatformSelect";
import { useProjects } from "@/features/projects/ProjectsProvider";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import { useSaveForm } from "@/hooks/useSaveForm";

export function ProjectGeneralPage() {
  const { slug } = useParams<{ slug: string }>();
  return slug === undefined ? <Navigate to="/projects" replace /> : <ProjectGeneralScreen slug={slug} />;
}

function ProjectGeneralScreen({ slug }: { slug: string }) {
  const { session } = useSession();
  useDocumentTitle(`General · ${slug}`);
  const isOwner = session.state === "authenticated" && session.user.role === "owner";
  const { state, reload } = useProjects();
  const project = state.status === "ready" ? state.projects.find((candidate) => candidate.slug === slug) : undefined;

  if (state.status === "ready" && project === undefined) {
    return <NotFoundPage />;
  }

  return (
    <>
      <PageHeader
        title="General"
        description={isOwner ? "The project's name and platform." : "The project's name and platform. Changed by an owner."}
      />
      <div className="p-4 sm:p-6">
        {state.status === "loading" && <Skeleton className="h-48 w-full max-w-2xl" />}
        {state.status === "failed" && <ErrorState message={state.message} onRetry={() => void reload()} />}
        {project !== undefined && <ProjectForm key={project.id} project={project} editable={isOwner} onSaved={reload} />}
      </div>
    </>
  );
}

interface ProjectFormProps {
  project: Project;
  editable: boolean;
  onSaved: () => Promise<void>;
}

function ProjectForm({ project, editable, onSaved }: ProjectFormProps) {
  const [name, setName] = useState(project.name);
  const [emoji, setEmoji] = useState(project.icon_emoji ?? "");
  const [image, setImage] = useState(project.icon_image ?? "");
  const savedEmoji = useRef(project.icon_emoji ?? "");
  const savedImage = useRef(project.icon_image ?? "");
  const [imageBusy, setImageBusy] = useState(false);
  const [platform, setPlatform] = useState(project.platform);
  const { submitting, error, saved, touch, submit: save } = useSaveForm();

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    await save(async () => {
      await api.updateProject(project.slug, {
        name,
        platform,
        ...(emoji !== savedEmoji.current ? { icon_emoji: emoji } : {}),
        ...(image !== savedImage.current ? { icon_image: image } : {})
      });
      savedEmoji.current = emoji;
      savedImage.current = image;
      // The switcher and the projects list read the shared list, so they must
      // see the new name too.
      await onSaved();
    });
  };

  return (
    <form onSubmit={(event) => void submit(event)} className="max-w-2xl space-y-6">
      <div className="space-y-2">
        <Label htmlFor="project-name">Name</Label>
        <Input
          id="project-name"
          value={name}
          disabled={!editable}
          required
          onChange={(event) => {
            setName(event.target.value);
            touch();
          }}
        />
      </div>

      <ProjectIdentityEditor emoji={emoji} image={image} disabled={!editable || submitting} onBusyChange={setImageBusy} onChange={(nextEmoji, nextImage) => { setEmoji(nextEmoji); setImage(nextImage); touch(); }} />

      <div className="space-y-2">
        <Label htmlFor="project-slug">Slug</Label>
        <Input id="project-slug" value={project.slug} disabled readOnly aria-describedby="project-slug-hint" className="font-mono" />
        <p id="project-slug-hint" className="text-muted-foreground text-sm">
          The slug is part of every dashboard URL and sentry-cli upload path, so it cannot be changed.
        </p>
      </div>

      <div className="space-y-2">
        <Label htmlFor="project-platform">Platform</Label>
        <PlatformSelect
          id="project-platform"
          value={platform}
          disabled={!editable}
          onChange={(value) => {
            setPlatform(value);
            touch();
          }}
        />
        <p className="text-muted-foreground text-sm">
          Decides which SDK setup instructions the ingest keys page shows. Events already received are not affected.
        </p>
      </div>

      {error !== null && (
        <p role="alert" className="text-destructive text-sm">
          {error}
        </p>
      )}
      {saved && (
        <p role="status" className="text-sm">
          Saved.
        </p>
      )}
      {editable && (
        <Button type="submit" disabled={submitting || imageBusy || !validProjectEmoji(emoji)}>
          {submitting ? "Saving…" : "Save"}
        </Button>
      )}
    </form>
  );
}
