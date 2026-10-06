import { ChevronRight } from "lucide-react";
import { useState } from "react";

import { cn } from "@/lib/utils";

interface JsonTreeProps {
  value: unknown;
  /** How many levels start open. The top level alone by default: the rest is a click away. */
  openDepth?: number;
}

/**
 * A payload as a tree that folds. The raw event and a breadcrumb's data run to
 * thousands of lines when printed flat, and the part wanted is usually one
 * branch. Folded branches build no rows, so opening a page costs what is
 * showing, not the size of the payload.
 */
export function JsonTree({ value, openDepth = 1 }: JsonTreeProps) {
  return (
    <div className="font-mono text-xs leading-5">
      <Node value={value} depth={0} openDepth={openDepth} />
    </div>
  );
}

interface NodeProps {
  /** The key or index this value hangs from, absent at the root. */
  label?: string;
  value: unknown;
  depth: number;
  openDepth: number;
}

function Node({ label, value, depth, openDepth }: NodeProps) {
  const [open, setOpen] = useState(depth < openDepth);

  if (!isContainer(value)) {
    return (
      <div className="flex gap-1.5 pl-4">
        {label !== undefined && <span className="text-muted-foreground shrink-0">{label}</span>}
        <Primitive value={value} />
      </div>
    );
  }

  const entries = Array.isArray(value) ? value.map((item, index) => [String(index), item] as const) : Object.entries(value);

  if (entries.length === 0) {
    return (
      <div className="flex gap-1.5 pl-4">
        {label !== undefined && <span className="text-muted-foreground shrink-0">{label}</span>}
        <span className="text-muted-foreground">{Array.isArray(value) ? "[]" : "{}"}</span>
      </div>
    );
  }

  const renderRows = () => (
    <ul className={cn(depth > 0 && "border-border ml-1.5 border-l pl-1.5")}>
      {entries.map(([key, child]) => (
        <li key={key}>
          <Node label={key} value={child} depth={depth + 1} openDepth={openDepth} />
        </li>
      ))}
    </ul>
  );

  // The root is always open and has nothing to say for itself, so it is just its rows.
  if (depth === 0) {
    return renderRows();
  }

  return (
    <div>
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((current) => !current)}
        className="hover:bg-accent/50 flex w-full items-center gap-1 rounded-sm text-left"
      >
        <ChevronRight className={cn("size-3 shrink-0 transition-transform", open && "rotate-90")} aria-hidden="true" />
        {label !== undefined && <span className="text-muted-foreground shrink-0">{label}</span>}
        <span className="text-muted-foreground/70">{sizeLabel(value, entries.length)}</span>
      </button>
      {open && renderRows()}
    </div>
  );
}

function Primitive({ value }: { value: unknown }) {
  const text = value === undefined ? "undefined" : JSON.stringify(value);
  const tone = typeof value === "string" ? "text-foreground" : value === null ? "text-muted-foreground" : "text-level-info";
  return <span className={cn("min-w-0 break-all", tone)}>{text}</span>;
}

function isContainer(value: unknown): value is object {
  return typeof value === "object" && value !== null;
}

function sizeLabel(value: object, count: number): string {
  if (Array.isArray(value)) {
    return `${count} ${count === 1 ? "item" : "items"}`;
  }
  return `${count} ${count === 1 ? "key" : "keys"}`;
}
