/**
 * The Tera mark: a balance scale.
 *
 * "Tera" is the legal calibration stamp struck on a weighing instrument by the
 * Direktorat Metrologi — the mark that says this scale may be trusted to settle
 * a transaction. That is what this system is for, so it is what the logo says.
 *
 * Inline SVG, not an <img>: one less thing to load and one less thing that can
 * fail with no internet (INV-11). Sizing and spacing belong to `.merek-tanda`
 * in the stylesheet, so this takes no props — the shell decides how big the
 * mark is, the same way it did when this slot held a letter.
 *
 * Four shapes and nothing else. The same mark ships as the browser icon at
 * 16px (web/public/favicon.svg), where hangers and a pivot knob turn to mush;
 * the two files draw the same geometry and must be changed together.
 */
export function Merek() {
  return (
    <svg
      className="merek-tanda"
      viewBox="0 0 64 64"
      // Decorative: every place this appears already carries the word "Tera"
      // beside it, and a screen reader should not read the name twice.
      aria-hidden="true"
      focusable="false"
    >
      <rect width="64" height="64" rx="14" fill="var(--accent)" />
      <g fill="var(--accent-text)">
        <rect x="29.25" y="16" width="5.5" height="30" rx="2.75" />
        <rect x="15" y="20" width="34" height="5.5" rx="2.75" />
        <path d="M9 29h16l-8 9z" />
        <path d="M39 29h16l-8 9z" />
        <rect x="20" y="43.5" width="24" height="5.5" rx="2.75" />
      </g>
    </svg>
  )
}
