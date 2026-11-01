/**
 * TotpQrCode: a scannable symbol for an `otpauth://` enrolment URI.
 *
 * The enrolment panel must show the same code a person can type by hand
 * as a QR symbol an authenticator can scan. This component takes the URI
 * as-is and renders the module matrix itself, in SVG: the encoder's job
 * ends at "is this module dark", so the output is a viewBox-scaled image
 * with a quiet zone, crisp module edges and no dependence on a canvas,
 * a data URL or a network round trip.
 *
 * The accessible name is a prop, never a string this package decides:
 * the component reads no language bundle of its own, so the enrolling
 * surface keeps the wording. An empty URI, or one the encoder cannot
 * represent (a module count beyond its limit), renders nothing -- the
 * caller keeps the manual-entry path -- and no failure reaches the
 * render tree.
 */

import { useMemo } from 'react'
import type { ReactElement } from 'react'
import { useTheme } from '@mui/material/styles'
import qrcode from 'qrcode-generator'

/** Modules of white space around the symbol. The format requires four;
 * scanners rely on it to find the symbol's edges. */
const QUIET_ZONE_MODULES = 4

/** The rendered edge length in CSS pixels when the caller names none:
 * large enough for a phone camera at normal distance, small enough for
 * a desktop panel. */
const DEFAULT_SIZE = 192

/** One rendered matrix: the viewBox edge and the path for its dark
 * modules, both in module units. */
type QrMatrix = {
  size: number
  path: string
}

export type TotpQrCodeProps = {
  /** The `otpauth://` URI to encode. An empty or unusable URI renders null. */
  uri: string
  /** The image's accessible name, already translated by the caller. */
  label: string
  /** Rendered edge length in CSS pixels. Defaults to 192. */
  size?: number
}

/**
 * Renders `uri` as a QR symbol: role="img" with the caller's accessible
 * name, a white background, the dark modules drawn as one path so edges
 * stay sharp at any rendered size.
 */
export function TotpQrCode({
  uri,
  label,
  size = DEFAULT_SIZE,
}: TotpQrCodeProps): ReactElement | null {
  const theme = useTheme()
  const matrix = useMemo(
    () => (uri.trim() === '' ? null : buildMatrix(uri)),
    [uri],
  )

  if (matrix === null) {
    return null
  }

  const renderedSize = Number.isFinite(size) && size > 0 ? size : DEFAULT_SIZE

  return (
    <svg
      role="img"
      aria-label={label}
      viewBox={`0 0 ${matrix.size} ${matrix.size}`}
      width={renderedSize}
      height={renderedSize}
      xmlns="http://www.w3.org/2000/svg"
    >
      <rect
        width={matrix.size}
        height={matrix.size}
        fill={theme.palette.common.white}
      />
      <path
        d={matrix.path}
        fill={theme.palette.common.black}
        shapeRendering="crispEdges"
      />
    </svg>
  )
}

/**
 * Encodes `uri` and reads back only its module matrix, then joins each
 * row's runs of dark modules into one path segment. Answers null when
 * the encoder refuses the input -- a payload beyond the largest symbol
 * throws -- so the failure never reaches the render tree.
 */
function buildMatrix(uri: string): QrMatrix | null {
  try {
    const encoder = qrcode(0, 'M')
    encoder.addData(uri)
    encoder.make()

    const moduleCount = encoder.getModuleCount()
    const segments: string[] = []

    for (let row = 0; row < moduleCount; row += 1) {
      let column = 0
      while (column < moduleCount) {
        if (!encoder.isDark(row, column)) {
          column += 1
          continue
        }
        const start = column
        while (column < moduleCount && encoder.isDark(row, column)) {
          column += 1
        }
        const run = column - start
        segments.push(
          `M${start + QUIET_ZONE_MODULES} ${row + QUIET_ZONE_MODULES}h${run}v1h-${run}z`,
        )
      }
    }

    return {
      size: moduleCount + QUIET_ZONE_MODULES * 2,
      path: segments.join(''),
    }
  } catch {
    return null
  }
}
