import { LogOut, Menu } from "lucide-react";
import { useNavigate } from "react-router-dom";

import { globalNavItems, projectNavItems } from "@/app/navItems";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger
} from "@/components/ui/dropdown-menu";
import { useSession } from "@/features/auth/SessionProvider";
import { useTheme } from "@/features/theme/ThemeProvider";
import { THEME_ICONS, THEME_LABELS, ThemeRadioGroup } from "@/features/theme/ThemeToggle";

/**
 * Navigation for viewports too narrow for the sidebar.
 *
 * Without this the shell simply hid itself below `lg`, leaving no way to
 * change project, reach settings, or sign out.
 */
export function MobileNav({ slug }: { slug: string | null }) {
  const { session, signOut } = useSession();
  const navigate = useNavigate();
  const { preference, setPreference } = useTheme();
  const ThemeIcon = THEME_ICONS[preference];
  const global = session.state === "authenticated" ? globalNavItems(session.user.role) : [];
  const scoped = slug === null || session.state !== "authenticated" ? [] : projectNavItems(slug, session.user.role);
  const items = [...global, ...scoped];

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="icon" aria-label="Open navigation menu">
          <Menu className="size-4" aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        {items.map((item) => (
          <DropdownMenuItem key={item.to} onSelect={() => void navigate(item.to)}>
            <item.icon className="size-4" aria-hidden="true" />
            {item.label}
          </DropdownMenuItem>
        ))}

        <DropdownMenuSeparator />

        <DropdownMenuSub>
          <DropdownMenuSubTrigger aria-label={`Colour scheme: ${THEME_LABELS[preference]}`}>
            <ThemeIcon className="size-4" aria-hidden="true" />
            Theme: {THEME_LABELS[preference]}
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent className="w-40">
            <ThemeRadioGroup preference={preference} onSelect={setPreference} />
          </DropdownMenuSubContent>
        </DropdownMenuSub>

        <DropdownMenuSeparator />

        {session.state === "authenticated" && (
          <DropdownMenuLabel className="text-muted-foreground truncate text-xs font-normal">
            {session.user.email}
          </DropdownMenuLabel>
        )}
        <DropdownMenuItem onSelect={() => void signOut()}>
          <LogOut className="size-4" aria-hidden="true" />
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
