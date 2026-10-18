import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '@fontsource-variable/inter'
import '@fontsource-variable/sora'
import '@fontsource-variable/jetbrains-mono'
import './daisy.css'
import './index.css'
import App from './App.tsx'
import { reloadForNewBuild } from './chunkReload'

// Vite fires this when a script the page needs cannot be fetched (typically: a deploy replaced it). Reload once for the new build instead of erroring.
window.addEventListener('vite:preloadError', (event) => {
  if (reloadForNewBuild()) event.preventDefault()
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
