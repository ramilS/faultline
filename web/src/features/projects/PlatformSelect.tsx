import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from "@/components/ui/select";
import { platformGroups, type Platform } from "@/features/projects/platforms";

interface PlatformSelectProps {
  id: string;
  value: string;
  onChange: (value: Platform) => void;
  disabled?: boolean;
}

export function PlatformSelect({ id, value, onChange, disabled = false }: PlatformSelectProps) {
  return (
    <Select value={value} onValueChange={(next) => onChange(next as Platform)} disabled={disabled}>
      <SelectTrigger id={id} className="w-full">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {platformGroups().map((entry) => (
          <SelectGroup key={entry.group}>
            <SelectLabel>{entry.group}</SelectLabel>
            {entry.platforms.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </SelectGroup>
        ))}
      </SelectContent>
    </Select>
  );
}
