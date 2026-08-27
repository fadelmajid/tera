/**
 * Light / dark / system, chosen by the person using it.
 *
 * The shop floor is under fluorescent light all day and the owner reads margin
 * figures on a phone in the evening. Those want different things, and the
 * operating system's preference is only a guess at either — so the choice is
 * explicit, remembered, and defaults to following the system when nobody has
 * made one.
 *
 * Stored in localStorage rather than on the server: it is a property of the
 * device someone is looking at, not of their account. The cashier's till and
 * the owner's phone should not have to agree.
 */

export type Tema = 'terang' | 'gelap' | 'sistem'

const KUNCI = 'tera:tema'

/** What each option is called on screen. */
export const labelTema: Record<Tema, string> = {
  terang: 'Terang',
  gelap: 'Gelap',
  sistem: 'Sistem',
}

export function bacaTema(): Tema {
  try {
    const v = localStorage.getItem(KUNCI)
    if (v === 'terang' || v === 'gelap' || v === 'sistem') return v
  } catch {
    // Private browsing, or storage disabled. Following the system is a fine
    // answer and not worth an error message.
  }
  return 'sistem'
}

/**
 * Applies the theme by stamping `data-theme` on the root element.
 *
 * "sistem" removes the attribute rather than resolving it to light or dark,
 * so the CSS media query takes over and the page follows the OS live — a
 * laptop switching at sunset changes without a reload.
 */
export function terapkanTema(tema: Tema) {
  const root = document.documentElement
  if (tema === 'sistem') {
    root.removeAttribute('data-theme')
  } else {
    root.setAttribute('data-theme', tema === 'gelap' ? 'dark' : 'light')
  }
  try {
    localStorage.setItem(KUNCI, tema)
  } catch {
    // Not being able to remember the choice is not a reason to refuse it.
  }
}
