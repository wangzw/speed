interface IconProps {
  readonly size?: number
}

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

export function TransferRequestsNavIcon({ size = 24 }: IconProps) {
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
      <path d="M5 8.5h13" />
      <path d="m15 5.5 3 3-3 3" />
      <path d="M19 15.5H6" />
      <path d="m9 12.5-3 3 3 3" />
    </svg>
  )
}

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
      <circle cx="12" cy="8" r="3.5" />
      <path d="M5.5 19.5c.9-3 3.5-4.6 6.5-4.6s5.6 1.6 6.5 4.6" />
    </svg>
  )
}

export function AdminNavIcon({ size = 24 }: IconProps) {
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

export function ProfileNavIcon({ size = 24 }: IconProps) {
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
      <circle cx="9" cy="8" r="3.2" />
      <path d="M3.5 19c.8-2.6 3-4 5.5-4s4.7 1.4 5.5 4" />
      <path d="M16 9h5M16 13h5" />
    </svg>
  )
}

export function SecurityNavIcon({ size = 24 }: IconProps) {
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
      <path d="M12 3l7 3v5c0 4.5-3 8-7 10-4-2-7-5.5-7-10V6l7-3z" />
      <path d="M12 9v4M12 16h.01" />
    </svg>
  )
}

export function SessionsNavIcon({ size = 24 }: IconProps) {
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
      <rect x="3" y="5" width="18" height="11" rx="2" />
      <path d="M8 20h8M12 16v4" />
    </svg>
  )
}

export function PreferencesNavIcon({ size = 24 }: IconProps) {
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
      <path d="M12 3a9 9 0 1 0 0 18c1.4 0 2-1 2-2 0-.8-.6-1.2-.6-2 0-.8.7-1.5 1.6-1.5H17a4 4 0 0 0 4-4c0-4.7-4-8.5-9-8.5z" />
      <circle cx="7.5" cy="12" r="1" fill="currentColor" />
      <circle cx="9.5" cy="8" r="1" fill="currentColor" />
      <circle cx="14.5" cy="7" r="1" fill="currentColor" />
    </svg>
  )
}
