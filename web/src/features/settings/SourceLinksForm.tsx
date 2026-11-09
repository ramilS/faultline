import { ExternalLink } from "lucide-react";
import { useState, type FormEvent } from "react";

import type { Project, SourceLinkSetting, SymbolicatedFrame } from "@/api";
import { api } from "@/apiClient";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { sourceLink } from "@/features/issue-detail/frames";
import { useSaveForm } from "@/hooks/useSaveForm";

const SAMPLE_RELEASE = "1.0.0";
const SAMPLE_LINE = 42;

function sampleFrame(stackRoot: string): SymbolicatedFrame {
  const root = stackRoot.replace(/\/+$/, "");
  const filename = root === "" ? "shop/cart.py" : `${root}/shop/cart.py`;
  return {
    generated: { function: "load", filename, line: SAMPLE_LINE, column: 0, in_app: true },
    original: null,
    context: [],
    vars: null
  };
}

interface SourceLinksFormProps {
  project: Project;
  editable: boolean;
  onSaved: () => Promise<void>;
}

export function SourceLinksForm({ project, editable, onSaved }: SourceLinksFormProps) {
  const [setting, setSetting] = useState<SourceLinkSetting>({
    source_link_template: project.source_link_template,
    source_stack_root: project.source_stack_root,
    source_root: project.source_root
  });
  const { submitting, error, saved, touch, submit: save } = useSaveForm();
  const sample = sampleFrame(setting.source_stack_root);
  const preview = sourceLink(sample, SAMPLE_RELEASE, setting);

  const edit = (field: keyof SourceLinkSetting) => (value: string) => {
    setSetting((current) => ({ ...current, [field]: value }));
    touch();
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    await save(async () => {
      await api.updateSourceLinks(project.slug, setting);
      await onSaved();
    });
  };

  return (
    <form onSubmit={(event) => void submit(event)} className="max-w-2xl space-y-6 border-t pt-6">
      <div className="space-y-1">
        <h2 className="text-base font-semibold">Source code links</h2>
        <p className="text-muted-foreground text-sm">
          Puts an “Open in repository” link on every in-app frame that has a file and line.
        </p>
      </div>

      <div className="space-y-2">
        <Label htmlFor="source-link-template">Link template</Label>
        <Input
          id="source-link-template"
          value={setting.source_link_template}
          disabled={!editable}
          placeholder="https://github.com/acme/shop/blob/main/{path}#L{line}"
          aria-describedby="source-link-template-hint"
          className="font-mono"
          onChange={(event) => edit("source_link_template")(event.target.value)}
        />
        <p id="source-link-template-hint" className="text-muted-foreground text-sm">
          An https:// or http:// URL containing <code>{"{path}"}</code>, and optionally <code>{"{line}"}</code> and{" "}
          <code>{"{release}"}</code>. The release is put in exactly as the SDK reported it, so a name like{" "}
          <code>app@1.2.3</code> only works if your repository has a branch or tag by that name. Leave empty for no
          links.
        </p>
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-2">
          <Label htmlFor="source-stack-root">Stack root</Label>
          <Input
            id="source-stack-root"
            value={setting.source_stack_root}
            disabled={!editable}
            placeholder="/app/"
            className="font-mono"
            onChange={(event) => edit("source_stack_root")(event.target.value)}
          />
          <p className="text-muted-foreground text-sm">Removed from the start of each frame's path.</p>
        </div>
        <div className="space-y-2">
          <Label htmlFor="source-root">Source root</Label>
          <Input
            id="source-root"
            value={setting.source_root}
            disabled={!editable}
            placeholder="src/"
            className="font-mono"
            onChange={(event) => edit("source_root")(event.target.value)}
          />
          <p className="text-muted-foreground text-sm">Put in front of the path inside the repository.</p>
        </div>
      </div>

      {setting.source_link_template !== "" && (
        <div className="bg-muted/30 space-y-1 rounded-md border p-3 text-sm">
          <p className="text-muted-foreground">
            Preview for <code>{sample.generated.filename}:{SAMPLE_LINE}</code> at release <code>{SAMPLE_RELEASE}</code>
          </p>
          {preview === null ? (
            <p>The template must be an absolute https:// or http:// URL containing {"{path}"}.</p>
          ) : (
            <a
              href={preview}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 font-mono text-xs break-all underline"
            >
              <ExternalLink className="size-3 shrink-0" aria-hidden="true" />
              {preview}
            </a>
          )}
        </div>
      )}

      {error !== null && (
        <p role="alert" className="text-destructive text-sm">
          {error}
        </p>
      )}
      {saved && (
        <p role="status" className="text-sm">
          Saved.
        </p>
      )}
      {editable && (
        <Button type="submit" disabled={submitting}>
          {submitting ? "Saving…" : "Save source links"}
        </Button>
      )}
    </form>
  );
}
