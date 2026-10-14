/// <reference types="vite/client" />

// Typed access to our VITE_* environment variables (see .env.example).
interface ImportMetaEnv {
  readonly VITE_API_URL?: string
  readonly VITE_CURRENCY?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
