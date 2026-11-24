/**
 * A scannable QR symbol for an `otpauth://` enrolment URI, drawn as an SVG
 * path. The accessible name is the caller's; an unusable URI renders nothing.
 */

import { useMemo } from 'react'
import type { ReactElement } from 'react'
import { useTheme } from '@mui/material/styles'
import qrcode from 'qrcode-generator'

/** Modules of white space around the symbol; the format requires four. */
const QUIET_ZONE_MODULES = 4

/** The rendered edge length in CSS pixels when the caller names none. */
const DEFAULT_SIZE = 192

/** The viewBox edge and the path for the dark modules, both in module units. */
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

/** Renders the URI as `role="img"` with the caller's name and one path for the dark modules. */
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
 * Joins each row's runs of dark modules into one path segment. Answers null
 * when the encoder refuses the input, so no failure reaches the render tree.
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
