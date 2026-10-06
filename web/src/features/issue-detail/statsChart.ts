import type { Bucket } from "../../api.ts";

export const CHART_WIDTH = 100;
export const CHART_HEIGHT = 100;

/** Share of a bucket's slot left empty, so adjacent bars read as separate columns. */
const GAP_RATIO = 0.18;

/** A bucket with events never rounds to nothing: an invisible bar reads as zero. */
const MIN_BAR_HEIGHT = 2;

export interface StatsBar {
  readonly bucket: string;
  /** End of this bar's window — exclusive, so it can be linked to as an absolute range. */
  readonly bucketEnd: string;
  readonly count: number;
  readonly x: number;
  readonly y: number;
  readonly width: number;
  readonly height: number;
}

export interface StatsChartLayout {
  readonly bars: readonly StatsBar[];
  readonly peak: number;
  readonly total: number;
}

/** `start` plus `widthMs`, formatted like the bucket timestamps the server sends (no milliseconds). */
function addBucketWidth(start: string, widthMs: number): string {
  return new Date(Date.parse(start) + widthMs).toISOString().replace(/\.000Z$/, "Z");
}

/**
 * Positions each bucket in a unit viewBox, drawn with
 * `preserveAspectRatio="none"` so one layout serves any chart size.
 *
 * `fallbackBucketWidthMs` is the width of one bucket in the selected period
 * (e.g. one hour for 24h, one day for 14d/90d). It is only used to derive a
 * single bucket's end, since a lone bucket carries no spacing of its own to
 * infer it from; every other bucket's end comes from the next bucket's start.
 */
export function layoutStatsChart(buckets: readonly Bucket[], fallbackBucketWidthMs: number): StatsChartLayout {
  if (buckets.length === 0) {
    return { bars: [], peak: 0, total: 0 };
  }

  const peak = buckets.reduce((highest, bucket) => Math.max(highest, bucket.count), 0);
  const total = buckets.reduce((sum, bucket) => sum + bucket.count, 0);
  const slot = CHART_WIDTH / buckets.length;
  const width = slot * (1 - GAP_RATIO);

  // Buckets are evenly spaced by the server, so the gap between the first two
  // stands in for the last bucket's width too — the one bucket with no
  // successor to read its own end from.
  const bucketSpacingMs =
    buckets.length > 1 ? Date.parse(buckets[1].bucket) - Date.parse(buckets[0].bucket) : fallbackBucketWidthMs;

  const bars = buckets.map((bucket, index) => {
    const scaled = peak === 0 ? 0 : (bucket.count / peak) * CHART_HEIGHT;
    const height = bucket.count === 0 ? 0 : Math.max(MIN_BAR_HEIGHT, scaled);
    const nextBucket = buckets[index + 1];
    return {
      bucket: bucket.bucket,
      bucketEnd: nextBucket !== undefined ? nextBucket.bucket : addBucketWidth(bucket.bucket, bucketSpacingMs),
      count: bucket.count,
      x: index * slot,
      y: CHART_HEIGHT - height,
      width,
      height
    };
  });

  return { bars, peak, total };
}
