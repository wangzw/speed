/**
 * Avatar: the small initials-or-image identity chip.
 *
 * Renders the supplied image when `src` is given (and loads), and derives
 * up to two uppercase initials from `name` otherwise. It is
 * presentational only -- the name and the image are host data, it holds
 * no state, and it makes no request.
 *
 * By default a named avatar exposes `name` as its accessible name (a
 * `role="img"` with an `aria-label`), a named avatar with no image reads
 * as its name rather than its bare initials. When the avatar sits inside
 * a control that already carries the identity -- a button wrapping it
 * next to the account name -- pass `decorative` so the glyph leaves the
 * accessibility tree and the control's own label is the single
 * announcement.
 *
 * A decorative glyph is still VISIBLE text. WCAG 2.5.3 (Label in Name)
 * requires a control's visible text to be part of its accessible name, so
 * a host wrapping a decorative avatar must include the initials in that
 * label. `avatarInitials` is the same derivation the glyph renders, exported
 * so the host's label and the glyph cannot drift apart.
 */

import MuiAvatar from '@mui/material/Avatar'
import type { SxProps, Theme } from '@mui/material/styles'

/** Font-size rem base; matches MUI's 16px html font size. */
const REM_BASE = 16

export interface AvatarProps {
  /** The display name; its initials render when there is no image. */
  readonly name?: string
  /** An image URL; the name's initials render when it is absent or fails to load. */
  readonly src?: string
  /** Side length in px. Defaults to 40. */
  readonly size?: number
  /** Leave the glyph out of the accessibility tree (a wrapping control carries the label). */
  readonly decorative?: boolean
  /** Extra styling merged onto the avatar. */
  readonly sx?: SxProps<Theme>
}

/**
 * Up to two uppercase initials from a display name (first and last word).
 * This is exactly the text the avatar renders when it has no image, exported
 * so a host wrapping a decorative avatar can put the visible glyph inside
 * the control's accessible name (WCAG 2.5.3).
 */
export function avatarInitials(name: string | undefined): string {
  if (name === undefined) {
    return ''
  }
  const parts = name.trim().split(/\s+/).filter((part) => part !== '')
  if (parts.length === 0) {
    return ''
  }
  const first = parts[0]?.[0] ?? ''
  const last = parts.length > 1 ? (parts[parts.length - 1]?.[0] ?? '') : ''
  return `${first}${last}`.toUpperCase()
}

/**
 * The identity chip. `name` supplies the initials and, unless
 * `decorative`, the accessible name; `src` supplies an image when the
 * host has one.
 */
export function Avatar({
  name,
  src,
  size = 40,
  decorative = false,
  sx,
}: AvatarProps) {
  const showImage = src !== undefined && src !== ''
  const labeled = !decorative && name !== undefined
  // When an image renders, its own `alt` carries the name and the wrapper
  // stays roleless; only the initials fallback needs an explicit image
  // role and label to read as the name rather than the bare initials.
  const labelInitials = labeled && !showImage
  return (
    <MuiAvatar
      src={src}
      alt={decorative ? '' : (name ?? '')}
      role={labelInitials ? 'img' : undefined}
      aria-label={labelInitials ? name : undefined}
      aria-hidden={decorative ? true : undefined}
      sx={[
        {
          width: size,
          height: size,
          fontSize: `${(size * 0.4) / REM_BASE}rem`,
        },
        ...(Array.isArray(sx) ? sx : sx ? [sx] : []),
      ]}
    >
      {avatarInitials(name)}
    </MuiAvatar>
  )
}
