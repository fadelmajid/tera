import { useCallback, useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

/**
 * Every dialog in the application.
 *
 * Three behaviours that were missing when each screen rolled its own:
 *
 *   Escape closes.       There was no key handler anywhere in the app.
 *   Focus is trapped.    Tab used to walk straight out of the dialog into the
 *                        page behind it, which for a keyboard user means the
 *                        modal is decoration.
 *   The backdrop does    The payment dialog used to close on any backdrop
 *   not close.           click, discarding a half-typed amount, date, method
 *                        and note with no confirmation. A dialog holding typed
 *                        input must not lose it to a stray click.
 *
 * Rendered through a portal so a dialog opened from inside a <td> is not
 * subject to the table's stacking context.
 */

const BISA_FOKUS = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

export function Modal({
  judul,
  tutup,
  children,
  aksi,
  gawat = false,
  labelTutup = 'Tutup',
}: {
  judul: string
  /** Called by Escape and by the close button. Backdrop clicks never call it. */
  tutup: () => void
  children: ReactNode
  /** The buttons along the bottom. Rendered inside the dialog's action row. */
  aksi?: ReactNode
  /** Marks the dialog as announcing something irreversible. */
  gawat?: boolean
  labelTutup?: string
}) {
  const kotak = useRef<HTMLDivElement>(null)
  const judulId = useId()

  useEffect(() => {
    const sebelumnya = document.activeElement as HTMLElement | null
    const semula = document.body.style.overflow
    document.body.style.overflow = 'hidden'

    // Focus the dialog itself rather than its first field: a destructive
    // confirmation should be read before anything is typed into it.
    kotak.current?.focus()

    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation()
        tutup()
        return
      }
      if (e.key !== 'Tab' || !kotak.current) return

      const fokusable = Array.from(
        kotak.current.querySelectorAll<HTMLElement>(BISA_FOKUS),
      ).filter((el) => el.offsetParent !== null || el === document.activeElement)
      if (fokusable.length === 0) {
        e.preventDefault()
        return
      }

      const pertama = fokusable[0]
      const terakhir = fokusable[fokusable.length - 1]
      if (!pertama || !terakhir) return
      const aktif = document.activeElement

      if (!e.shiftKey && (aktif === terakhir || aktif === kotak.current)) {
        e.preventDefault()
        pertama.focus()
      } else if (e.shiftKey && (aktif === pertama || aktif === kotak.current)) {
        e.preventDefault()
        terakhir.focus()
      }
    }

    document.addEventListener('keydown', onKey, true)
    return () => {
      document.removeEventListener('keydown', onKey, true)
      document.body.style.overflow = semula
      sebelumnya?.focus?.()
    }
  }, [tutup])

  return createPortal(
    <div className="modal-latar">
      <div
        className={gawat ? 'modal gawat' : 'modal'}
        role="dialog"
        aria-modal="true"
        aria-labelledby={judulId}
        tabIndex={-1}
        ref={kotak}
      >
        <div className="card-kepala">
          <h3 id={judulId}>{judul}</h3>
          <button
            type="button"
            className="sekunder ikon-saja"
            onClick={tutup}
            aria-label={labelTutup}
          >
            ×
          </button>
        </div>

        {children}

        {aksi && <div className="modal-aksi">{aksi}</div>}
      </div>
    </div>,
    document.body,
  )
}

/**
 * A confirmation that records why.
 *
 * This replaces `window.prompt`, which the tax-rule and omzet screens used to
 * collect audit-log reasons and closure dates. Those are the changes a
 * konsultan pajak is shown; capturing them through a browser dialog that
 * cannot be styled, cannot be validated, cannot show the figure being changed,
 * and looks exactly like a phishing popup was the wrong control for them.
 *
 * The reason is required when `wajibAlasan` is set, because a log entry saying
 * only *what* changed is half a record.
 */
export function Konfirmasi({
  judul,
  tutup,
  jalankan,
  labelJalankan,
  gawat = false,
  wajibAlasan = true,
  labelAlasan = 'Alasan (dicatat di log audit)',
  petunjukAlasan,
  sibuk = false,
  children,
  tambahan,
}: {
  judul: string
  tutup: () => void
  jalankan: (alasan: string) => void
  labelJalankan: string
  gawat?: boolean
  wajibAlasan?: boolean
  labelAlasan?: string
  petunjukAlasan?: string
  sibuk?: boolean
  /** What is about to happen, in the user's terms. */
  children: ReactNode
  /** Extra fields above the reason — a date, a choice. */
  tambahan?: ReactNode
}) {
  const [alasan, setAlasan] = useState('')
  const bisa = !sibuk && (!wajibAlasan || alasan.trim() !== '')

  const kirim = useCallback(() => {
    if (!bisa) return
    jalankan(alasan.trim())
  }, [bisa, jalankan, alasan])

  return (
    <Modal
      judul={judul}
      tutup={tutup}
      gawat={gawat}
      labelTutup="Batal"
      aksi={
        <>
          <button
            type="button"
            className={gawat ? 'bahaya' : undefined}
            disabled={!bisa}
            onClick={kirim}
          >
            {sibuk ? 'Menyimpan…' : labelJalankan}
          </button>
          <button type="button" className="sekunder" disabled={sibuk} onClick={tutup}>
            Batal
          </button>
        </>
      }
    >
      {children}
      {tambahan}
      <label>
        <span>
          {labelAlasan}
          {wajibAlasan ? ' *' : ''}
        </span>
        <input
          value={alasan}
          onChange={(e) => setAlasan(e.target.value)}
          required={wajibAlasan}
          autoFocus
        />
        {petunjukAlasan && <small className="petunjuk">{petunjukAlasan}</small>}
      </label>
    </Modal>
  )
}
