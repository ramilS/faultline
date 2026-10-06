import { cn } from "@/lib/utils";

/** The narrowest a bar may be, in the 100-unit drawing, before it stops reading as a bar. */
const MIN_BAR_WIDTH = 1.5;

interface SparklineProps {
  counts: readonly number[];
  /** Describes the window, e.g. "24 hours", for the screen-reader summary. */
  window: string;
  className?: string;
}

/**
 * Event volume over a fixed window, drawn as SVG rather than DOM nodes so a
 * 24-bar row costs one element instead of twenty-four.
 *
 * Hand-rolled on purpose: two chart shapes across the whole dashboard does not
 * justify a charting dependency.
 */
export function Sparkline({ counts, window, className }: SparklineProps) {
  if (counts.length === 0) {
    return <span className={cn("text-muted-foreground text-xs", className)}>—</span>;
  }

  const peak = Math.max(1, ...counts);
  const total = counts.reduce((sum, count) => sum + count, 0);
  const barWidth = 100 / counts.length;

  return (
    <svg
      viewBox="0 0 100 100"
      preserveAspectRatio="none"
      className={cn("text-muted-foreground/70 h-6 w-24", className)}
      role="img"
      aria-label={`${total} events over the last ${window}, peaking at ${peak}`}
    >
      {/* A faint floor, so a single spike reads as one bar on a chart rather
          than a stray stroke, and empty buckets read as zero rather than gaps. */}
      <rect x={0} y={96} width={100} height={4} fill="currentColor" opacity={0.25} />
      {counts.map((count, index) => {
        // A non-zero bucket always paints at least a sliver; rounding it to
        // nothing would read as "no events" when there were some.
        const height = count === 0 ? 0 : Math.max(3, (count / peak) * 100);
        // A long window has hundreds of buckets, so an even share of the width
        // is a sliver; floor it, and pull the bar back inside the drawing.
        const width = Math.max(barWidth - 0.6, MIN_BAR_WIDTH);
        return (
          <rect
            key={index}
            x={Math.min(index * barWidth, 100 - width)}
            y={100 - height}
            width={width}
            height={height}
            fill="currentColor"
          />
        );
      })}
    </svg>
  );
}
