import type { PriceStatus } from '../format'

// StatusBadge renders the colored pill for a product's price status
// (green "Target hit", orange "12% above target", grey "Awaiting price").
export function StatusBadge({ status }: { status: PriceStatus }) {
  return <span className={`badge badge--${status.kind}`}>{status.label}</span>
}

// TargetProgress is a bar showing how close the current price is to the target.
export function TargetProgress({ status }: { status: PriceStatus }) {
  return (
    <div
      className={`progress progress--${status.kind}`}
      role="progressbar"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(status.progress)}
      aria-label="Progress toward target price"
    >
      <div className="progress__fill" style={{ width: `${status.progress}%` }} />
    </div>
  )
}
