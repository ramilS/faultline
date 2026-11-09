import { Badge } from "@/components/ui/badge";

interface DefaultProtectionProps {
  fields: readonly string[];
  scope: "project" | "instance";
}

export function DefaultProtection({ fields, scope }: DefaultProtectionProps) {
  return (
    <section aria-labelledby="scrub-defaults-heading" className="space-y-3 rounded-md border p-4">
      <div className="space-y-1">
        <h2 id="scrub-defaults-heading" className="text-base font-medium">
          Protected by default
        </h2>
        <p className="text-muted-foreground text-sm">
          {scope === "project"
            ? "While scrubbing is on, these are replaced with [Filtered] before an event is stored. You only need a rule for something else."
            : "Every project with scrubbing on replaces these with [Filtered] before an event is stored. Rules on this page are added on top, for every project."}
        </p>
      </div>
      <div className="space-y-2">
        <p className="text-sm">Values under any key containing</p>
        <ul aria-label="Default sensitive field names" className="flex flex-wrap gap-1.5">
          {fields.map((field) => (
            <li key={field}>
              <Badge variant="secondary" className="font-mono">
                {field}
              </Badge>
            </li>
          ))}
        </ul>
      </div>
      <ul className="text-muted-foreground list-disc space-y-1 pl-5 text-sm">
        <li>Card numbers that pass the Luhn check, wherever they appear.</li>
        <li>Bearer tokens of 16 or more characters, and name=value pairs whose name contains one of the names above.</li>
        <li>
          The client IP (user.ip_address, REMOTE_ADDR, X-Forwarded-For, X-Real-IP), unless the project is set to store
          user IPs.
        </li>
      </ul>
    </section>
  );
}
