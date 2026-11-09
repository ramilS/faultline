import { useProjects } from "@/features/projects/ProjectsProvider";

export function useProjectName(slug: string): string {
  const { state } = useProjects();
  return state.status === "ready" ? (state.projects.find((project) => project.slug === slug)?.name ?? slug) : slug;
}
