import type { ReactNode } from 'react'

interface Props {
  label: string
  htmlFor: string
  hint?: string
  error?: string
  children: ReactNode
}

// Field wraps an input with its label and either a hint or an error message.
export function Field({ label, htmlFor, hint, error, children }: Props) {
  return (
    <div className={`field ${error ? 'field--error' : ''}`}>
      <label htmlFor={htmlFor}>{label}</label>
      {children}
      {error ? <span className="field__error">{error}</span> : hint && <span className="field__hint">{hint}</span>}
    </div>
  )
}
