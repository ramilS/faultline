import {
  Bug,
  FileKey,
  KeyRound,
  LayoutGrid,
  ListFilter,
  Settings2,
  ShieldCheck,
  UserCircle,
  Users,
  UsersRound,
  type LucideIcon
} from "lucide-react";

import type { UserRole } from "@/api";

export interface NavItem {
  readonly to: string;
  readonly label: string;
  readonly icon: LucideIcon;
  /** Exact matching, so /projects does not stay highlighted inside a project. */
  readonly end?: boolean;
}

/**
 * Navigation outside any project. Filtered by role rather than left to a
 * failed request: a member must never see a link to a screen the server will
 * 403. The account screen carries no such restriction — every
 * signed-in user may change their own password — so it is never filtered.
 */
export function globalNavItems(role: UserRole): readonly NavItem[] {
  const items: NavItem[] = [
    { to: "/issues", label: "All issues", icon: Bug, end: true },
    { to: "/projects", label: "Projects", icon: LayoutGrid, end: true },
    { to: "/settings/account", label: "Account", icon: UserCircle }
  ];
  if (role === "owner") {
    items.push({ to: "/settings/users", label: "Users", icon: Users });
    items.push({ to: "/settings/groups", label: "Groups", icon: UsersRound });
    items.push({ to: "/settings/data-scrubbing", label: "Data scrubbing", icon: ShieldCheck });
  }
  return items;
}

/**
 * Navigation scoped to a project. Shared by the desktop sidebar and the
 * mobile menu so the two cannot drift apart.
 */
export function projectNavItems(slug: string): readonly NavItem[] {
  return [
    { to: `/${slug}/issues`, label: "Issues", icon: Bug },
    { to: `/${slug}/settings/keys`, label: "Ingest keys", icon: KeyRound },
    { to: `/${slug}/settings/artifact-tokens`, label: "Artifact tokens", icon: FileKey },
    { to: `/${slug}/settings/grouping`, label: "Grouping rules", icon: ListFilter },
    { to: `/${slug}/settings/data-scrubbing`, label: "Data scrubbing", icon: ShieldCheck },
    { to: `/${slug}/settings/general`, label: "General", icon: Settings2 }
  ];
}
