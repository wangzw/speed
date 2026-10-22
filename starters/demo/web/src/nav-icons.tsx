/**
 * Hand-authored icons for the tenant area's navigation entries.
 *
 * The same drawing convention as console-ui's navigation icons: 24px-grid
 * strokes in currentColor, no icon-font or MUI icons dependency, every
 * icon decorative (the entry's label is its accessible name). The tenant
 * navigation needs them because the collapsed sidebar rail shows an
 * entry's icon instead of its clipped label; without one, a collapsed
 * tenant navigation would show cut-off text ("Acco…") where the console
 * shows icons.
 *
 * Each icon is attached to its route declaration (src/routes.tsx), which is
 * where the frame looks a navigation entry's mark up from; nothing here
 * knows a path.
 */

interface IconProps {
  /** Box length in px; stroke geometry is sized for the 24 grid. */
  readonly size?: number
}

/** The home page: a house. */
export function HomeNavIcon({ size = 24 }: IconProps) {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M4 10.5 12 4l8 6.5" />
      <path d="M6 9.5v10h12v-10" />
      <path d="M10 19.5v-5h4v5" />
    </svg>
  )
}

/** Account settings: a person. */
export function AccountNavIcon({ size = 24 }: IconProps) {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <circle cx="12" cy="8.5" r="3.2" />
      <path d="M5.5 19.5c.8-3.2 3.4-5 6.5-5s5.7 1.8 6.5 5" />
    </svg>
  )
}

/** Object storage: a box. */
export function ObjectsNavIcon({ size = 24 }: IconProps) {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M12 3.5 19 7.5v9L12 20.5 5 16.5v-9z" />
      <path d="M5 7.5 12 11.5l7-4" />
      <path d="M12 11.5v9" />
    </svg>
  )
}

/** The notification centre: a bell. */
export function NotificationsNavIcon({ size = 24 }: IconProps) {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M6.5 16.5h11c-1.1-1.3-1.7-3-1.7-5.1V10a3.8 3.8 0 0 0-7.6 0v1.4c0 2.1-.6 3.8-1.7 5.1z" />
      <path d="M10.3 19a2 2 0 0 0 3.4 0" />
    </svg>
  )
}

/** The console entry: four panels, the administration surfaces' own mark. */
export function ConsoleNavIcon({ size = 24 }: IconProps) {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <rect x="3.8" y="3.8" width="7" height="7" rx="1.4" />
      <rect x="13.2" y="3.8" width="7" height="7" rx="1.4" />
      <rect x="3.8" y="13.2" width="7" height="7" rx="1.4" />
      <rect x="13.2" y="13.2" width="7" height="7" rx="1.4" />
    </svg>
  )
}
