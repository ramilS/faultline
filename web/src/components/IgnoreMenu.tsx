import { BellOff, ChevronDown } from "lucide-react";
import { Fragment } from "react";

import type { IgnoreCondition } from "@/api";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger
} from "@/components/ui/dropdown-menu";
import { IGNORE_CHOICE_GROUPS } from "@/lib/ignoreUntil";

interface IgnoreMenuProps {
  disabled: boolean;
  onIgnore: (condition: IgnoreCondition | null) => void;
}

export function IgnoreMenu({ disabled, onIgnore }: IgnoreMenuProps) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" disabled={disabled}>
          <BellOff aria-hidden="true" />
          Ignore
          <ChevronDown className="text-muted-foreground" aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="min-w-56">
        <DropdownMenuItem onSelect={() => onIgnore(null)}>Forever</DropdownMenuItem>
        {IGNORE_CHOICE_GROUPS.map((choices) => (
          <Fragment key={choices[0].label}>
            <DropdownMenuSeparator />
            {choices.map((choice) => (
              <DropdownMenuItem key={choice.label} onSelect={() => onIgnore(choice.condition)}>
                {choice.label}
              </DropdownMenuItem>
            ))}
          </Fragment>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
