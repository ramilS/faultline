import { toast } from "sonner";

import { messageFor } from "../errors.ts";

/**
 * The Clipboard API is absent outside a secure context and can be denied by
 * permission policy, so failure is an expected branch rather than an
 * exception: silently doing nothing would leave the user believing a
 * shown-once key had been captured.
 */
export async function copyToClipboard(value: string, label: string): Promise<void> {
  if (navigator.clipboard === undefined) {
    toast.error(`${label} could not be copied automatically — select the text and copy it manually.`);
    return;
  }
  try {
    await navigator.clipboard.writeText(value);
    toast.success(`${label} copied to clipboard`);
  } catch (caught) {
    toast.error(`${label} could not be copied: ${messageFor(caught)}`);
  }
}

/**
 * Safari lets a page write to the clipboard only while the click that asked
 * for it is still being handled, which an awaited fetch outlives. Handing the
 * pending text to a ClipboardItem keeps the write inside that click.
 */
export async function copyLoadedText(load: () => Promise<string>, label: string): Promise<void> {
  if (navigator.clipboard === undefined) {
    toast.error(`${label} could not be copied automatically — select the text and copy it manually.`);
    return;
  }
  const text = load();
  try {
    if (typeof ClipboardItem === "function" && typeof navigator.clipboard.write === "function") {
      const blob = text.then((value) => new Blob([value], { type: "text/plain" }));
      await navigator.clipboard.write([new ClipboardItem({ "text/plain": blob })]);
    } else {
      await navigator.clipboard.writeText(await text);
    }
    toast.success(`${label} copied to clipboard`);
  } catch (caught) {
    const loadFailure = await text.then(
      () => null,
      (failure: unknown) => failure
    );
    toast.error(`${label} could not be copied: ${messageFor(loadFailure ?? caught)}`);
  }
}
