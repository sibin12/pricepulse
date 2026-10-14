// Entry point: mounts the React app into <div id="root"> from index.html.
// StrictMode only affects development: it runs effects twice to surface bugs,
// which is why the API log shows some requests doubled while developing.
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import './styles.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
