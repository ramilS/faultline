// Mirrors the server's group name rule (internal/group/service.go's
// validateGroupName): trimmed, non-blank, at most 64 characters. Checking it
// client-side means most invalid names are caught before the round trip,
// while the server's own 400 is still shown verbatim for whatever slips
// through.
const MAX_GROUP_NAME_LENGTH = 64;

export function validateGroupName(name: string): string | null {
  const trimmed = name.trim();
  if (trimmed === "") {
    return "Name is required";
  }
  if (trimmed.length > MAX_GROUP_NAME_LENGTH) {
    return `Name must be ${MAX_GROUP_NAME_LENGTH} characters or fewer`;
  }
  return null;
}
