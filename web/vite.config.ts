import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Port 3000 matches the CORS origin the Go API allows by default.
export default defineConfig({
  plugins: [react()],
  server: { port: 3000, strictPort: true },
})
