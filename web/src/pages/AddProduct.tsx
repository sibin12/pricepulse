import { useState, type FormEvent } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api } from '../api'
import { useAuth } from '../auth'
import { Field } from '../components/Field'
import { parsePrice } from '../format'

// AddProduct is the "Add a wish" form. Validation here mirrors the API's
// rules so users get instant feedback; the API still validates everything.
export function AddProduct() {
  const navigate = useNavigate()
  const { user, refresh } = useAuth()
  // Bought items don't count, so product_count is only the active wishes.
  const atLimit = !!user && user.product_count >= user.limits.max_products

  // Form fields are kept as strings (what the inputs hold) and parsed on submit.
  const [url, setUrl] = useState('')
  const [title, setTitle] = useState('') // optional; the store's name is used if empty
  const [note, setNote] = useState('') // optional "why I want it"
  const [target, setTarget] = useState('') // "my price": alert at or below this
  const [current, setCurrent] = useState('') // optional; the scraper fetches it if empty
  const [errors, setErrors] = useState<Record<string, string>>({}) // per-field messages
  const [serverError, setServerError] = useState('') // e.g. "already tracking", "list full"
  const [saving, setSaving] = useState(false)

  // validate fills `errors` and returns true when the form can be sent.
  function validate() {
    const e: Record<string, string> = {}
    try {
      const u = new URL(url.trim())
      if (u.protocol !== 'http:' && u.protocol !== 'https:') throw new Error()
    } catch {
      e.url = 'Enter a full http(s) URL, e.g. https://store.com/item'
    }
    if (parsePrice(target) === null) e.target = 'Enter a price greater than 0'
    if (current.trim() && parsePrice(current) === null) e.current = 'Leave empty or enter a price greater than 0'
    setErrors(e)
    return Object.keys(e).length === 0
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setServerError('')
    if (!validate()) return
    setSaving(true)
    try {
      const product = await api.createProduct({
        url: url.trim(),
        title: title.trim() || undefined,
        note: note.trim() || undefined,
        target_price: parsePrice(target)!,
        current_price: parsePrice(current) ?? undefined,
      })
      await refresh() // update "x of N wishes used" in the header/list
      // Go straight to the new product's page; its first price check starts there.
      navigate(`/products/${product.id}`)
    } catch (err) {
      setServerError((err as Error).message)
      setSaving(false)
    }
  }

  return (
    <div className="narrow">
      <Link to="/" className="back">
        ← Back to my bucket list
      </Link>
      <h1>Add a wish</h1>
      <p className="muted">Something you want, and the price you'd happily pay. We'll watch it and tell you when it's time.</p>
      {user && (
        <p className={atLimit ? 'callout callout--warn' : 'muted'}>
          {atLimit
            ? `Your bucket list is full (${user.product_count} of ${user.limits.max_products}). Check off or remove a wish to add a new one.`
            : `${user.product_count} of ${user.limits.max_products} wishes used. Bought items don't count.`}
        </p>
      )}

      <form className="card form" onSubmit={onSubmit} noValidate>
        <Field label="Product URL" error={errors.url} htmlFor="url">
          <input
            id="url"
            className="input"
            type="url"
            placeholder="https://store.com/product/123"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            autoFocus
          />
        </Field>

        <Field label="Why do you want it?" hint="Optional, e.g. “For the Goa trip” or “Birthday gift for Mom”." htmlFor="note">
          <input
            id="note"
            className="input"
            placeholder="A reason makes the wish real"
            value={note}
            onChange={(e) => setNote(e.target.value)}
            maxLength={280}
          />
        </Field>

        <Field label="Name" hint="Optional. Filled in from the store page if left empty." htmlFor="title">
          <input
            id="title"
            className="input"
            placeholder="e.g. Sony WH-1000XM5"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            maxLength={200}
          />
        </Field>

        <div className="form__row">
          <Field label="My price" error={errors.target} hint="We'll alert you at or below this." htmlFor="target">
            <input
              id="target"
              className="input"
              type="number"
              inputMode="decimal"
              min="0.01"
              step="0.01"
              placeholder="0.00"
              value={target}
              onChange={(e) => setTarget(e.target.value)}
            />
          </Field>
          <Field label="Current price" error={errors.current} hint="Optional. Leave empty to fetch it automatically." htmlFor="current">
            <input
              id="current"
              className="input"
              type="number"
              inputMode="decimal"
              min="0.01"
              step="0.01"
              placeholder="0.00"
              value={current}
              onChange={(e) => setCurrent(e.target.value)}
            />
          </Field>
        </div>

        {serverError && <div className="callout callout--error">{serverError}</div>}

        <div className="form__actions">
          <Link to="/" className="btn">
            Cancel
          </Link>
          <button className="btn btn--primary" type="submit" disabled={saving || atLimit}>
            {saving ? 'Adding…' : 'Add to my bucket list'}
          </button>
        </div>
      </form>
    </div>
  )
}
