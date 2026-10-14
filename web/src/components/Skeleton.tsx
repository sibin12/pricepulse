// Skeleton placeholders: grey blocks in the shape of content that is still
// loading. Showing these instead of "Loading…" text keeps the page layout
// stable, so nothing jumps around when the real data arrives.

export function SkeletonText({ width = '100%' }: { width?: string }) {
  return <div className="skeleton skeleton--text" style={{ width }} aria-hidden />
}

export function SkeletonBlock({ height }: { height?: number }) {
  return <div className="skeleton skeleton--block" style={height ? { height } : undefined} aria-hidden />
}

// SkeletonCards fills the bucket-list grid while products load.
export function SkeletonCards({ count = 4 }: { count?: number }) {
  return (
    <ul className="bucket-grid" aria-busy aria-label="Loading">
      {Array.from({ length: count }, (_, i) => (
        <li key={i} className="skeleton skeleton--card" />
      ))}
    </ul>
  )
}

// SkeletonDetail mimics a product page: title, stats and a chart area.
export function SkeletonDetail() {
  return (
    <div aria-busy aria-label="Loading">
      <div className="skeleton skeleton--title" />
      <SkeletonText width="40%" />
      <div className="stats stats--two" style={{ marginTop: '1rem' }}>
        <SkeletonBlock height={90} />
        <SkeletonBlock height={90} />
      </div>
      <div style={{ marginTop: '1.25rem' }}>
        <SkeletonBlock height={300} />
      </div>
    </div>
  )
}
