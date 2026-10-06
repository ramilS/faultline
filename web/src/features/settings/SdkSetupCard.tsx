import { Copy } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { copyToClipboard } from "@/lib/clipboard";

import { sdkSetupFor } from "./sdkSetup";

/** Shown in place of a DSN the viewer is not allowed to see. */
const DSN_PLACEHOLDER = "<your DSN>";

interface SdkSetupCardProps {
  platform: string;
  /** An active key's DSN, or null when the viewer cannot see one (members get masked keys). */
  dsn: string | null;
}

/**
 * How to point this project's SDK at Faultline. Renders nothing for a platform
 * with no checked snippet — the page header already says to set the DSN, and
 * an unverified snippet would be worse than saying so.
 */
export function SdkSetupCard({ platform, dsn }: SdkSetupCardProps) {
  const setup = sdkSetupFor(platform, dsn ?? DSN_PLACEHOLDER);
  if (setup === null) {
    return null;
  }

  return (
    <Card className="gap-3 py-4">
      <CardHeader className="px-4">
        <CardTitle className="text-sm">Set up your SDK</CardTitle>
        <CardDescription className="text-xs">
          {dsn === null
            ? `Add this at startup (${setup.language}), with the DSN an owner gives you.`
            : `Add this at startup (${setup.language}).`}
        </CardDescription>
        {dsn !== null && (
          <CardAction>
            <Button
              type="button"
              variant="outline"
              size="xs"
              onClick={() => void copyToClipboard(setup.code, "Snippet")}
            >
              <Copy aria-hidden="true" />
              Copy
            </Button>
          </CardAction>
        )}
      </CardHeader>
      <CardContent className="px-4">
        <pre className="bg-muted overflow-x-auto rounded-md p-3 font-mono text-xs leading-5">
          <code>{setup.code}</code>
        </pre>
      </CardContent>
    </Card>
  );
}
