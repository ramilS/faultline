import { DetailSection } from "./DetailSection";
import { isRequestEmpty } from "./eventSections";
import type {
  EventContext,
  EventExtraEntry,
  EventModule,
  EventPayloadFields,
  EventSdk,
  EventTag,
  EventUser,
  HttpRequest
} from "./payloadReaders";

interface EventContextPanelsProps {
  /** The event's payload, already read once — see `parsedEvent.ts`. */
  fields: EventPayloadFields;
}

/**
 * Who and what: the user attached to this event and the HTTP request it
 * carried, when the SDK captured either. Many events carry neither — most
 * SDKs don't set a user, and background jobs have no request — so each panel
 * renders nothing rather than an empty card.
 */
export function EventContextPanels({ fields }: EventContextPanelsProps) {
  const { user, request, contexts, sdk, tags, extra, modules } = fields;

  return (
    <>
      {user !== null && !user.isEmpty && <UserPanel user={user} />}
      {request !== null && !isRequestEmpty(request) && <RequestPanel request={request} />}
      {tags.length > 0 && <TagsPanel tags={tags} />}
      {contexts.length > 0 && <ContextsPanel contexts={contexts} />}
      {sdk !== null && <SdkPanel sdk={sdk} />}
      {extra.length > 0 && <ExtraPanel extra={extra} />}
      {modules.length > 0 && <ModulesPanel modules={modules} />}
    </>
  );
}

function UserPanel({ user }: { user: EventUser }) {
  return (
    <DetailSection id="section-user" title="User">
      <dl className="divide-y text-sm">
        <UserField label="ID" value={user.id} />
        <UserField label="Email" value={user.email} />
        <UserField label="Username" value={user.username} />
        <UserField label="IP address" value={user.ipAddress} />
      </dl>
      <p className="text-muted-foreground mt-3 text-xs">
        The user on this event — not a count of users affected by this issue.
      </p>
    </DetailSection>
  );
}

function UserField({ label, value }: { label: string; value: string }) {
  if (value === "") {
    return null;
  }

  return (
    <div className="grid grid-cols-[auto_1fr] items-baseline gap-x-3 gap-y-0 py-1.5 first:pt-0">
      <dt className="text-muted-foreground shrink-0">{label}</dt>
      {/* Not a mailto: link — the email is attacker-supplied, and linking it
          invites contacting an address the payload chose. */}
      <dd className="min-w-0 font-mono text-xs break-all">{value}</dd>
    </div>
  );
}

function RequestPanel({ request }: { request: HttpRequest }) {
  return (
    <DetailSection id="section-request" title="Request">
      <div className="flex flex-wrap items-baseline gap-2 text-sm">
        {request.method !== "" && (
          <span className="shrink-0 rounded-md border px-1.5 py-0.5 font-mono text-xs font-medium">
            {request.method}
          </span>
        )}
        {/* Text, never a link — following a URL out of an error report is not
            a request the dashboard should make on the user's behalf. */}
        <span className="min-w-0 flex-1 font-mono text-xs break-all">{request.url}</span>
      </div>

      {request.query !== "" && (
        <section className="mt-3">
          <h3 className="text-muted-foreground text-xs font-medium tracking-wide uppercase">Query string</h3>
          <p className="mt-1 font-mono text-xs break-all">{request.query}</p>
        </section>
      )}

      {request.headers.length > 0 && (
        <section className="mt-3">
          <h3 className="text-muted-foreground text-xs font-medium tracking-wide uppercase">Headers</h3>
          <ul className="mt-1.5 space-y-1.5">
            {request.headers.map((header) => (
              <li key={header.name} className="grid grid-cols-[minmax(0,1fr)_minmax(0,2fr)] gap-x-3 text-xs">
                <span className="text-muted-foreground font-mono break-all">{header.name}</span>
                <span className="font-mono break-all">{header.value}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </DetailSection>
  );
}

function TagsPanel({ tags }: { tags: readonly EventTag[] }) {
  return (
    <DetailSection id="section-tags" title="Tags" description="Only this event's tags — the issue-wide breakdown is in the sidebar.">
      <dl className="divide-y text-sm">
        {tags.map((tag, index) => (
          // Key by position because the array can contain repeated keys (e.g., array-of-pairs
          // wire shape allows [["release","1.0"],["release","2.0"]]). The list is immutable
          // and never reordered, so index-based keying is stable and safe here.
          <div key={`${tag.key}:${index}`} className="grid grid-cols-[auto_1fr] items-baseline gap-x-3 gap-y-0 py-1.5 first:pt-0">
            <dt className="text-muted-foreground shrink-0 font-mono text-xs">{tag.key}</dt>
            <dd className="min-w-0 font-mono text-xs break-all">{tag.value}</dd>
          </div>
        ))}
      </dl>
    </DetailSection>
  );
}

/**
 * One card for every context. OS, runtime and device typically carry two or
 * three fields each, so a card apiece made the page several screens taller for
 * the same information.
 */
function ContextsPanel({ contexts }: { contexts: readonly EventContext[] }) {
  return (
    <DetailSection id="section-contexts" title="Contexts" contentClassName="space-y-4">
      {contexts.map((context) => (
        <section key={context.name}>
          <h3 className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
            {contextTitle(context.name)}
          </h3>
          <dl className="mt-1 divide-y text-sm">
            {context.fields.map((field) => (
              <div key={field.label} className="grid grid-cols-[auto_1fr] items-baseline gap-x-3 gap-y-0 py-1.5">
                <dt className="text-muted-foreground shrink-0">{field.label}</dt>
                <dd className="min-w-0 font-mono text-xs break-all">{field.value}</dd>
              </div>
            ))}
          </dl>
        </section>
      ))}
    </DetailSection>
  );
}

/** Capitalizes a context name for display: "os" -> "OS", "runtime" -> "Runtime". */
function contextTitle(name: string): string {
  return name.toLowerCase() === "os" ? "OS" : name.charAt(0).toUpperCase() + name.slice(1);
}

function SdkPanel({ sdk }: { sdk: EventSdk }) {
  return (
    <DetailSection id="section-sdk" title="SDK">
      <p className="font-mono text-xs">
        {sdk.name} <span className="text-muted-foreground">{sdk.version}</span>
      </p>
    </DetailSection>
  );
}

function ExtraPanel({ extra }: { extra: readonly EventExtraEntry[] }) {
  return (
    <DetailSection id="section-extra" title="Additional data">
      <dl className="divide-y text-sm">
        {extra.map((entry) => (
          <div key={entry.key} className="grid grid-cols-[auto_1fr] items-baseline gap-x-3 gap-y-0 py-1.5 first:pt-0">
            <dt className="text-muted-foreground shrink-0 font-mono text-xs">{entry.key}</dt>
            <dd className="min-w-0 font-mono text-xs break-all">{entry.value}</dd>
          </div>
        ))}
      </dl>
    </DetailSection>
  );
}

function ModulesPanel({ modules }: { modules: readonly EventModule[] }) {
  return (
    <DetailSection id="section-modules" title="Dependencies">
      <dl className="divide-y text-sm">
        {modules.map((module) => (
          <div key={module.name} className="grid grid-cols-[auto_1fr] items-baseline gap-x-3 gap-y-0 py-1.5 first:pt-0">
            <dt className="text-muted-foreground shrink-0 font-mono text-xs break-all">{module.name}</dt>
            <dd className="min-w-0 font-mono text-xs break-all">{module.version}</dd>
          </div>
        ))}
      </dl>
    </DetailSection>
  );
}
