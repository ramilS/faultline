import type { UserRole } from "@/api";

export interface ProjectsEmptyState {
  readonly title: string;
  readonly description: string;
  readonly canCreate: boolean;
}

/**
 * Copy and affordance for an empty project list, by role.
 *
 * A member's empty list is not "no projects exist" — it is "no group grants you
 * one", which is a different situation with a different remedy. Offering them
 * the create action would point at an endpoint the server answers 403 to.
 */
export function projectsEmptyState(role: UserRole): ProjectsEmptyState {
  if (role === "owner") {
    return {
      title: "No projects yet",
      description: "Create a project to start receiving events.",
      canCreate: true
    };
  }
  return {
    title: "No project access",
    description: "Ask an owner to add you to a group that covers a project.",
    canCreate: false
  };
}
