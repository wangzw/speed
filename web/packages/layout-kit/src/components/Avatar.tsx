/**
 * Avatar: the initials-or-image identity chip. A decorative avatar is visible
 * text, so a wrapping control must include its initials in its accessible name
 * (WCAG 2.5.3); `avatarInitials` is the same derivation the glyph renders.
 */

import MuiAvatar from '@mui/material/Avatar'
import type { SxProps, Theme } from '@mui/material/styles'

/** Font-size rem base; matches MUI's 16px html font size. */
const REM_BASE = 16

/** Props for Avatar. */
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
 * Up to two uppercase initials from a display name (first and last word) --
 * exactly the text the avatar renders when it has no image.
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

/** The identity chip: `name` supplies the initials and accessible name, `src` an image. */
export function Avatar({
  name,
  src,
  size = 40,
  decorative = false,
  sx,
}: AvatarProps) {
  const showImage = src !== undefined && src !== ''
  const labeled = !decorative && name !== undefined
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
