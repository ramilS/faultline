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
