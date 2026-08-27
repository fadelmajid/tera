import { useCallback, useEffect, useState } from 'react'
import { formatPercent, omzetPosition, stateLabel } from '../api/omzet'
import type { OmzetPosition, OmzetState } from '../api/omzet'
import { formatIDR } from '../money'
import { Stat } from './dasar'

/**
 * The omzet clock on the dashboard. TASKS 7.10, 7.12, SPEC 5.1-5.2.
 *
 * # The two figures are never shown as one
 *
 * The book-year cumulative is the legally binding number and is what the alarm
 * reads. The trailing twelve months is a pace estimate. They are laid out as two
 * separate blocks with the labels the server sent, because the mistake this
 * feature exists to prevent is somebody reading a rolling figure as the legal
 * one — which is what most guidance online invites.
 *
 * They differ most at exactly the moment it matters: in January, a business that
 * had a strong December reads high on the rolling figure and near zero on the
 * one that binds.
 *
 * # The two dates are never shown as one either
 *
 * On crossing the banner states both, side by side and labelled, because the gap
 * between them is the most misunderstood part of the rule. Registration is due
 * on one date; PPN has to be charged from a later one. A business told only the
 * first starts collecting tax it does not yet owe; told only the second, it
 * misses the registration.
 */
export function OmzetBanner({ entityId }: { entityId: string }) {
  const [data, setData] = useState<OmzetPosition | null>(null)

  const muat = useCallback(() => {
    omzetPosition(entityId)
      .then(setData)
      .catch(() => setData(null))
  }, [entityId])
  useEffect(muat, [muat])

  if (!data) return null

  const crossed = data.state === 'CROSSED'

  return (
    <div className="card">
      <div className="card-kepala">
        <h3>Omzet terhadap batas PKP</h3>
        <span className={`lencana ${lencanaState[data.state]}`}>{stateLabel[data.state]}</span>
      </div>

      {data.is_pkp && (
        <p className="catatan">
          Perusahaan ini sudah PKP. Angka di bawah dipantau untuk catatan, bukan sebagai peringatan
          pengukuhan.
        </p>
      )}

      <div className="statistik">
        <Stat
          besar
          label={data.cumulative.label}
          nilai={formatIDR(data.cumulative.amount_idr)}
          kaki={
            <>
              {formatPercent(data.cumulative.percent_bp)} dari{' '}
              {formatIDR(data.threshold.amount_idr)} · tahun buku {data.book_year} (
              <span className="tgl">{data.window.from}</span> s/d{' '}
              <span className="tgl">{data.window.to}</span>)
              {data.counted_to && (
                <>
                  {' '}
                  · dihitung s/d <span className="tgl">{data.counted_to}</span>
                </>
              )}
            </>
          }
        />
        <Stat
          label={data.trailing_12m.label}
          nilai={<span className="teks-redup">{formatIDR(data.trailing_12m.amount_idr)}</span>}
          kaki={
            <>
              <span className="tgl">{data.trailing_12m.window.from}</span> s/d{' '}
              <span className="tgl">{data.trailing_12m.window.to}</span> · perkiraan laju saja,
              bukan angka yang mengikat
            </>
          }
        />
      </div>

      {!crossed && data.cumulative.remaining_idr > 0 && (
        <p className="catatan">
          Sisa <strong className="angka-kiri">{formatIDR(data.cumulative.remaining_idr)}</strong>{' '}
          sebelum menyentuh batas pada tahun buku ini.
        </p>
      )}

      {crossed && data.crossed && (
        <div className="galat" role="status">
          <p>
            <strong>
              Batas {formatIDR(data.threshold.amount_idr)} terlewati pada{' '}
              <span className="tgl">{data.crossed.on}</span>.
            </strong>
          </p>

          <div className="statistik">
            <Stat label="Daftar PKP paling lambat" nilai={data.crossed.register_by} />
            <Stat label="Mulai wajib memungut PPN" nilai={data.crossed.vat_starts} />
          </div>

          <p>
            Dua tanggal yang berbeda: pendaftaran lebih dulu, kewajiban memungut PPN menyusul.
            Memungut PPN sebelum tanggal kedua sama salahnya dengan terlambat mendaftar.
          </p>

          {data.crossed.peak_idr > data.cumulative.amount_idr && (
            <p>
              Tertinggi tahun ini {formatIDR(data.crossed.peak_idr)} pada {data.crossed.peak_on}.
              Retur setelah itu menurunkan angka kumulatif, tetapi tidak membatalkan pelampauan yang
              sudah terjadi.
            </p>
          )}
        </div>
      )}

      <p className="disclaimer">
        {data.caveat} Dasar hukum batas: {data.threshold.legal_ref}.
      </p>
    </div>
  )
}

/** State is carried by the badge's colour as well as its word (SPEC 5.1). */
const lencanaState: Record<OmzetState, string> = {
  OK: 'lencana-aman',
  WATCH: '',
  WARN: 'lencana-hati',
  CROSSED: 'lencana-bahaya',
}
