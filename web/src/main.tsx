import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'
import { bacaTema, terapkanTema } from './tema'
import './styles.css'

// Applied before React renders, so the page never paints light and then flips
// to dark a frame later.
terapkanTema(bacaTema())

const root = document.getElementById('root')
if (!root) throw new Error('elemen #root tidak ditemukan')

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
