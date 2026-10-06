import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";

import type { Project } from "@/api";
import { api } from "@/apiClient";
import { messageFor } from "@/errors";

type ProjectsState =
  | { readonly status: "loading" }
  | { readonly status: "ready"; readonly projects: readonly Project[] }
  | { readonly status: "failed"; readonly message: string };

interface ProjectsContextValue {
  readonly state: ProjectsState;
  reload: () => Promise<void>;
}

const ProjectsContext = createContext<ProjectsContextValue | null>(null);

/**
 * The project list drives both the switcher in the shell and the projects
 * page. Loading it once here keeps them from issuing duplicate requests and
 * from disagreeing after a create.
 */
export function ProjectsProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<ProjectsState>({ status: "loading" });

  const reload = useCallback(async () => {
    try {
      const projects = await api.listProjects();
      setState({ status: "ready", projects });
    } catch (caught) {
      setState({ status: "failed", message: messageFor(caught) });
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  const value = useMemo<ProjectsContextValue>(() => ({ state, reload }), [state, reload]);

  return <ProjectsContext value={value}>{children}</ProjectsContext>;
}

export function useProjects(): ProjectsContextValue {
  const value = useContext(ProjectsContext);
  if (value === null) {
    throw new Error("useProjects must be used within a ProjectsProvider");
  }
  return value;
}
