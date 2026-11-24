import type { SxProps, Theme } from '@mui/material/styles'

/**
 * The styling props every public component forwards to its root element.
 * `sx` merges after the component's own styles, so a host rule wins an
 * overlap; `className` lands on the same element.
 */
export type RootStyleProps = {
  /** Extra styling applied to the component's root element. */
  readonly sx?: SxProps<Theme>
  /** Extra class applied to the component's root element. */
  readonly className?: string
}

/**
 * Layers a host's `sx` after the component's own rules. MUI resolves
 * exactly one array level, so a host array is flattened into the result
 * rather than nested.
 */
export function layerSx(
  base: SxProps<Theme>,
  host: SxProps<Theme> | undefined,
): SxProps<Theme> {
  if (host === undefined) {
    return base
  }
  const baseParts = Array.isArray(base) ? base : [base]
  return [...baseParts, ...(Array.isArray(host) ? host : [host])]
}
