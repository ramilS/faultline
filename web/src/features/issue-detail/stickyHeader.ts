/**
 * The measured height of the issue page's sticky header, published as a CSS
 * custom property on the document element.
 *
 * Jump-to-section anchors have to clear that header or the card they land on
 * sits partly underneath it. The header carries a breadcrumb row, a title,
 * an optional message line, a culprit, a meta line and an action bar, so its
 * height depends on the issue and on the viewport width — no fixed
 * `scroll-mt-*` value is correct for every render. Measuring it and letting
 * the sections read the measurement keeps the two in step without either
 * side guessing.
 */
export const STICKY_HEADER_HEIGHT_PROPERTY = "--issue-header-height";

/** Fallback when nothing has published a measurement — a plausible header, never zero. */
export const STICKY_HEADER_HEIGHT_FALLBACK = "5rem";

/** Breathing room between the header's lower edge and the card scrolled to. */
const ANCHOR_GAP = "0.5rem";

/** The `scroll-margin-top` a jump target needs to clear the header. */
export const SECTION_SCROLL_MARGIN = `calc(var(${STICKY_HEADER_HEIGHT_PROPERTY}, ${STICKY_HEADER_HEIGHT_FALLBACK}) + ${ANCHOR_GAP})`;

/**
 * The value to publish for a measured height, or null when the measurement
 * is not usable and the fallback must survive.
 *
 * A zero is not a short header: it is an element that has not been laid out
 * (first paint, a display:none ancestor, a non-layout test environment).
 * Publishing it would silently reinstate the bug this property exists to fix.
 */
export function stickyHeaderHeightValue(height: number): string | null {
  return Number.isFinite(height) && height > 0 ? `${height}px` : null;
}
