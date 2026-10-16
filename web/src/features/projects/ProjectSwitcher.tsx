import { Check, ChevronsUpDown } from "lucide-react";
import { ProjectIdentity } from "@/features/projects/ProjectIdentity";
import { useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";

import { Button } from "@/components/ui/button";
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { useProjects } from "@/features/projects/ProjectsProvider";
import { projectSlugFromPath } from "@/lib/projectSlug";
import { cn } from "@/lib/utils";

export function ProjectSwitcher() {
  const { state } = useProjects();
  const slug = projectSlugFromPath(useLocation().pathname);
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);

  const projects = state.status === "ready" ? state.projects : [];
  const active = projects.find((project) => project.slug === slug);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          role="combobox"
          aria-expanded={open}
          aria-label="Switch project"
          className="w-full justify-between font-normal"
        >
          <span className="flex min-w-0 items-center gap-2">
            <ProjectIdentity project={active ?? {}} />
            <span className="truncate">{active?.name ?? "Select project"}</span>
          </span>
          <ChevronsUpDown className="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-(--radix-popover-trigger-width) p-0" align="start">
        <Command>
          <CommandInput placeholder="Find project…" />
          <CommandList>
            <CommandEmpty>No project matches.</CommandEmpty>
            <CommandGroup>
              {projects.map((project) => (
                <CommandItem
                  key={project.id}
                  value={`${project.name} ${project.slug}`}
                  onSelect={() => {
                    setOpen(false);
                    void navigate(`/${project.slug}/issues`);
                  }}
                >
                  <Check className={cn("size-4", project.slug === slug ? "opacity-100" : "opacity-0")} />
                  <ProjectIdentity project={project} /><span className="truncate">{project.name}</span>
                  <span className="text-muted-foreground ml-auto font-mono text-xs">{project.slug}</span>
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
