/**
 * Toggles one id in a selection set, used by both the members and projects
 * dialogs — each `PUT` sends the complete set, so this is the one place that
 * decides what "complete" means after a single click.
 *
 * Returns a new array; the caller's selection is never mutated in place.
 */
export function toggleSelection(selected: readonly number[], id: number): number[] {
  if (selected.includes(id)) {
    return selected.filter((current) => current !== id);
  }
  return [...selected, id];
}
