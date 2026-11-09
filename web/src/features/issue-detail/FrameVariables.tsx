import { ChevronRight } from "lucide-react";
import { useState } from "react";

import { cn } from "@/lib/utils";

import { MAX_VAR_DEPTH, describeVar, varEntries, type VarEntry } from "./frameVars";

export function FrameVariables({ vars }: { vars: Record<string, unknown> }) {
  return (
    <div className="border-t px-3 py-2">
      <VariableTable label="Local variables" value={vars} depth={0} />
    </div>
  );
}

function VariableTable({ label, value, depth }: { label?: string; value: unknown; depth: number }) {
  const { entries, omitted } = varEntries(value);
  return (
    <table aria-label={label} className="w-full font-mono text-xs leading-5">
      <tbody>
        {entries.map((entry) => (
          <VariableRow key={entry.key} entry={entry} depth={depth} />
        ))}
        {omitted > 0 && (
          <tr>
            <td colSpan={2} className="text-muted-foreground italic">
              … {omitted} more truncated
            </td>
          </tr>
        )}
      </tbody>
    </table>
  );
}

function VariableRow({ entry, depth }: { entry: VarEntry; depth: number }) {
  const [open, setOpen] = useState(false);
  const description = describeVar(entry.value);

  return (
    <tr className="align-top">
      <th scope="row" className="text-muted-foreground w-1/4 pr-3 text-left font-normal break-all">
        {entry.key}
      </th>
      <td className="break-all">
        {description.kind === "scalar" ? (
          <span className="whitespace-pre-wrap">
            {description.text}
            {description.truncated > 0 && (
              <span className="text-muted-foreground italic"> … truncated, {description.truncated} more characters</span>
            )}
          </span>
        ) : depth + 1 >= MAX_VAR_DEPTH ? (
          <span className="text-muted-foreground">
            {description.summary} <span className="italic">… truncated, nested too deep</span>
          </span>
        ) : (
          <>
            <button
              type="button"
              aria-expanded={open}
              onClick={() => setOpen((current) => !current)}
              className="text-muted-foreground hover:text-foreground inline-flex items-center gap-1"
            >
              <ChevronRight className={cn("size-3 transition-transform", open && "rotate-90")} aria-hidden="true" />
              {description.summary}
            </button>
            {open && <VariableTable value={entry.value} depth={depth + 1} />}
          </>
        )}
      </td>
    </tr>
  );
}
