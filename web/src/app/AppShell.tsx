import { LogOut } from "lucide-react";
import { NavLink, Outlet, useLocation } from "react-router-dom";

import { MobileNav } from "@/app/MobileNav";
import { globalNavItems, projectNavItems, type NavItem } from "@/app/navItems";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { Toaster } from "@/components/ui/sonner";
import { useSession } from "@/features/auth/SessionProvider";
import { ProjectSwitcher } from "@/features/projects/ProjectSwitcher";
import { useProjects } from "@/features/projects/ProjectsProvider";
import { useTheme } from "@/features/theme/ThemeProvider";
import { ThemeToggle } from "@/features/theme/ThemeToggle";
import { projectSlugFromPath } from "@/lib/projectSlug";
import { cn } from "@/lib/utils";

function navClasses({ isActive }: { isActive: boolean }): string {
  return cn(
    "flex items-center gap-2.5 rounded-md px-2.5 py-1.5 text-sm transition-colors",
    isActive
      ? "bg-accent text-accent-foreground font-medium"
      : "text-muted-foreground hover:bg-accent/60 hover:text-foreground"
  );
}

function NavLinks({ items }: { items: readonly NavItem[] }) {
  return (
    <>
      {items.map((item) => (
        <NavLink key={item.to} to={item.to} end={item.end} className={navClasses}>
          <item.icon className="size-4 shrink-0" aria-hidden="true" />
          {item.label}
        </NavLink>
      ))}
    </>
  );
}

export function AppShell() {
  const candidateSlug = projectSlugFromPath(useLocation().pathname);
  const { session, signOut } = useSession();
  const { state: projects } = useProjects();
  const { resolved } = useTheme();

  // A path segment is not proof a project exists: /nonexistent-route parses as
  // a slug and would otherwise render Issues and settings links for nothing.
  // Scoped navigation waits until the project list confirms the slug.
  const slug =
    candidateSlug !== null &&
    projects.status === "ready" &&
    projects.projects.some((project) => project.slug === candidateSlug)
      ? candidateSlug
      : null;

  const scoped = slug === null ? [] : projectNavItems(slug);
  const global = session.state === "authenticated" ? globalNavItems(session.user.role) : [];

  return (
    <div className="flex min-h-dvh">
      {/* Pinned to the viewport rather than stretched to the page: on a long page
          a full-height sidebar puts its footer — theme, sign out — at the very
          bottom, out of reach. Only the navigation scrolls, if it ever must. */}
      <aside className="bg-card/40 sticky top-0 hidden h-dvh w-60 shrink-0 flex-col self-start border-r lg:flex">
        <div className="px-3 py-4">
          <span className="px-2.5 text-sm font-semibold tracking-tight">Faultline</span>
        </div>

        <div className="px-3 pb-3">
          <ProjectSwitcher />
        </div>

        <Separator />

        <nav className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto p-3" aria-label="Main">
          <NavLinks items={global} />
          {scoped.length > 0 && (
            <>
              <p className="text-muted-foreground mt-4 mb-1 truncate px-2.5 text-xs font-medium tracking-wide uppercase">
                {slug}
              </p>
              <NavLinks items={scoped} />
            </>
          )}
        </nav>

        <Separator />

        <div className="p-3">
          {session.state === "authenticated" && (
            <p className="text-muted-foreground truncate px-2.5 pb-2 text-xs" title={session.user.email}>
              {session.user.email}
            </p>
          )}
          <ThemeToggle variant="labeled" />
          {/* Separated from navigation: signing out ends the session rather
              than moving within it. */}
          <Button
            variant="ghost"
            size="sm"
            className="text-muted-foreground hover:text-foreground w-full justify-start"
            onClick={() => void signOut()}
          >
            <LogOut className="size-4" aria-hidden="true" />
            Sign out
          </Button>
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <div className="bg-background/80 sticky top-0 z-20 flex items-center gap-2 border-b px-4 py-2.5 backdrop-blur lg:hidden">
          <span className="text-sm font-semibold tracking-tight">Faultline</span>
          <div className="min-w-0 flex-1">
            <ProjectSwitcher />
          </div>
          <MobileNav slug={slug} />
        </div>

        <Outlet />
      </div>

      <Toaster theme={resolved} />
    </div>
  );
}
