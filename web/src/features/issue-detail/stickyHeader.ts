export const STICKY_HEADER_HEIGHT_PROPERTY = "--issue-header-height";
export const SECTION_NAV_HEIGHT_PROPERTY = "--issue-section-nav-height";

// Declared in globals.css and used as the height of AppShell's mobile top bar; 0 from `lg` up.
const APP_TOP_BAR_HEIGHT_PROPERTY = "--app-top-bar-height";

const SECTION_NAV_HEIGHT_FALLBACK = "2.75rem";
const ANCHOR_GAP = "0.5rem";

export const SECTION_NAV_TOP = `var(${STICKY_HEADER_HEIGHT_PROPERTY}, var(${APP_TOP_BAR_HEIGHT_PROPERTY}, 0px))`;

export const SECTION_SCROLL_MARGIN = `calc(${SECTION_NAV_TOP} + var(${SECTION_NAV_HEIGHT_PROPERTY}, ${SECTION_NAV_HEIGHT_FALLBACK}) + ${ANCHOR_GAP})`;

export function pinnedHeightValue(height: number): string | null {
  return Number.isFinite(height) && height > 0 ? `${height}px` : null;
}

export function publishPinnedHeight(property: string, height: number | null): void {
  const value = height === null ? null : pinnedHeightValue(height);
  if (value === null) {
    document.documentElement.style.removeProperty(property);
  } else {
    document.documentElement.style.setProperty(property, value);
  }
}
