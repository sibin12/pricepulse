// Small form controls shared by the admin pages.

import { formatInterval } from '../../format'

// Re-check interval presets, in minutes. A preset select is easier to use
// than a free-form number, and keeps values within the server's 5 min–7 day range.
const INTERVALS = [15, 30, 60, 180, 360, 720, 1440, 4320, 10080]

export function IntervalSelect(props: { id: string; value: number; onChange: (minutes: number) => void }) {
  // Keep an unusual stored value (e.g. set via the API) selectable.
  const options = INTERVALS.includes(props.value) ? INTERVALS : [...INTERVALS, props.value].sort((a, b) => a - b)
  return (
    <select id={props.id} className="input" value={props.value} onChange={(e) => props.onChange(Number(e.target.value))}>
      {options.map((m) => (
        <option key={m} value={m}>
          {capitalize(formatInterval(m))}
        </option>
      ))}
    </select>
  )
}

// Toggle is a switch-styled checkbox. The real <input> is visually hidden but
// still focusable, so keyboards and screen readers work as for any checkbox.
export function Toggle(props: { id: string; checked: boolean; onChange: (v: boolean) => void; label: string }) {
  return (
    <label className="toggle" htmlFor={props.id}>
      <input id={props.id} type="checkbox" checked={props.checked} onChange={(e) => props.onChange(e.target.checked)} />
      <span className="toggle__track" aria-hidden />
      <span>{props.label}</span>
    </label>
  )
}

const capitalize = (s: string) => s.charAt(0).toUpperCase() + s.slice(1)

export const onOff = (b: boolean) => (b ? 'On' : 'Off')
