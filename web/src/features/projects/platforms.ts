export const PLATFORMS = [
  { value: "javascript", label: "JavaScript", group: "JavaScript" },
  { value: "nestjs", label: "NestJS", group: "JavaScript" },
  { value: "python", label: "Python", group: "Python" },
  { value: "python-django", label: "Python · Django", group: "Python" },
  { value: "python-fastapi", label: "Python · FastAPI", group: "Python" },
  { value: "python-flask", label: "Python · Flask", group: "Python" },
  { value: "go", label: "Go", group: "Go" },
  { value: "ruby", label: "Ruby", group: "Ruby" },
  { value: "ruby-rails", label: "Ruby · Rails", group: "Ruby" },
  { value: "php", label: "PHP", group: "PHP" },
  { value: "php-laravel", label: "PHP · Laravel", group: "PHP" },
  { value: "php-symfony", label: "PHP · Symfony", group: "PHP" },
  { value: "rust", label: "Rust", group: "Rust" },
  { value: "jvm-spring", label: "JVM · Spring", group: "JVM" },
  { value: "jvm-ktor", label: "JVM · Ktor", group: "JVM" },
  { value: "kotlin-multiplatform", label: "Kotlin Multiplatform", group: "JVM" },
  { value: "scala", label: "JVM · Scala", group: "JVM" },
  { value: "android", label: "Android", group: "Mobile" },
  { value: "ios", label: "iOS", group: "Mobile" },
  { value: "other", label: "Other", group: "Other" }
] as const;

export type Platform = (typeof PLATFORMS)[number]["value"];
export type PlatformOption = (typeof PLATFORMS)[number];

export interface PlatformGroup {
  readonly group: string;
  readonly platforms: readonly PlatformOption[];
}

/** The catalogue folded into the runs the picker renders as headed sections. */
export function platformGroups(): PlatformGroup[] {
  const groups: { group: string; platforms: PlatformOption[] }[] = [];
  for (const option of PLATFORMS) {
    const last = groups[groups.length - 1];
    if (last?.group === option.group) {
      last.platforms.push(option);
    } else {
      groups.push({ group: option.group, platforms: [option] });
    }
  }
  return groups;
}

export function platformLabel(value: string): string {
  return PLATFORMS.find((platform) => platform.value === value)?.label ?? value;
}
