import {
  CircleCheckIcon,
  InfoIcon,
  Loader2Icon,
  OctagonXIcon,
  TriangleAlertIcon,
} from "lucide-react"
import { Toaster as Sonner, type ToasterProps } from "sonner"

import type { ResolvedTheme } from "@/lib/theme"

// `theme` is required rather than defaulted. Sonner's own "system" reads
// prefers-color-scheme, which stops matching the page as soon as a stored
// preference overrides the OS; requiring the prop makes that mismatch a type
// error instead of a light toast on a dark page.
type Props = Omit<ToasterProps, "theme"> & { theme: ResolvedTheme }

const Toaster = ({ ...props }: Props) => {
  return (
    <Sonner
      className="toaster group"
      icons={{
        success: <CircleCheckIcon className="size-4" />,
        info: <InfoIcon className="size-4" />,
        warning: <TriangleAlertIcon className="size-4" />,
        error: <OctagonXIcon className="size-4" />,
        loading: <Loader2Icon className="size-4 animate-spin" />,
      }}
      style={
        {
          "--normal-bg": "var(--popover)",
          "--normal-text": "var(--popover-foreground)",
          "--normal-border": "var(--border)",
          "--border-radius": "var(--radius)",
        } as React.CSSProperties
      }
      {...props}
    />
  )
}

export { Toaster }
