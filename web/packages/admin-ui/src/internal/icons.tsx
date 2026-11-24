/**
 * Hand-authored 24px stroke icons for the console's navigation entries. Each
 * is decorative: it renders aria-hidden and the entry's label is the
 * accessible name.
 */

interface IconProps {
  /** Box length in px; stroke geometry is sized for the 24 grid. */
  readonly size?: number
}

export function DashboardIcon({ size = 24 }: IconProps) {
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
      <rect x="4" y="4" width="7" height="7" rx="1.5" />
      <rect x="13" y="4" width="7" height="7" rx="1.5" />
      <rect x="4" y="13" width="7" height="7" rx="1.5" />
      <rect x="13" y="13" width="7" height="7" rx="1.5" />
    </svg>
  )
}

export function TenantsIcon({ size = 24 }: IconProps) {
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
      <rect x="5" y="3.5" width="14" height="17" rx="1.5" />
      <path d="M9 7.5h2M13 7.5h2M9 11h2M13 11h2" />
      <path d="M10 20.5v-4h4v4" />
    </svg>
  )
}

export function AccountsIcon({ size = 24 }: IconProps) {
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

export function SessionsIcon({ size = 24 }: IconProps) {
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
      <rect x="3.5" y="5" width="17" height="11.5" rx="1.8" />
      <path d="M9 20h6M12 16.5V20" />
    </svg>
  )
}

export function ImpersonationsIcon({ size = 24 }: IconProps) {
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
      <circle cx="8.5" cy="8" r="2.6" />
      <path d="M3.5 18.5c.6-2.6 2.5-4.3 5-4.3s4.4 1.7 5 4.3" />
      <circle cx="16.5" cy="9" r="2.1" />
      <path d="M13.8 14.6c.7-.9 1.6-1.4 2.7-1.4 2.1 0 3.7 1.4 4.2 3.6" />
    </svg>
  )
}

export function PermissionsIcon({ size = 24 }: IconProps) {
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
      <path d="M12 3.5l7 2.8v5.2c0 4.3-2.8 7.4-7 8.9-4.2-1.5-7-4.6-7-8.9V6.3z" />
      <path d="M9 12l2.2 2.2L15.5 10" />
    </svg>
  )
}

export function InvitationsIcon({ size = 24 }: IconProps) {
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
      <rect x="3.5" y="5.5" width="17" height="13" rx="2" />
      <path d="M4.5 7.5l7.5 5.5 7.5-5.5" />
    </svg>
  )
}

export function SsoIcon({ size = 24 }: IconProps) {
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
      <circle cx="8" cy="12" r="3.5" />
      <path d="M11.5 12H21" />
      <path d="M17.5 12v3M20 12v2.5" />
    </svg>
  )
}

export function SettingsIcon({ size = 24 }: IconProps) {
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
      <circle cx="12" cy="12" r="3" />
      <path d="M12 3.5v3M12 17.5v3M3.5 12h3M17.5 12h3" />
      <path d="M6 6l2.1 2.1M15.9 15.9L18 18M18 6l-2.1 2.1M8.1 15.9L6 18" />
    </svg>
  )
}

export function SmsIcon({ size = 24 }: IconProps) {
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
      <path d="M6.5 5h11A2.5 2.5 0 0 1 20 7.5v6a2.5 2.5 0 0 1-2.5 2.5H11l-4 3.5V16H6.5A2.5 2.5 0 0 1 4 13.5v-6A2.5 2.5 0 0 1 6.5 5z" />
      <path d="M8.5 10.5h.01M12 10.5h.01M15.5 10.5h.01" />
    </svg>
  )
}

export function AuditIcon({ size = 24 }: IconProps) {
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
      <rect x="5.5" y="3.5" width="13" height="17" rx="2" />
      <path d="M9 8h6M9 12h6M9 16h4" />
    </svg>
  )
}

export function ProfileIcon({ size = 24 }: IconProps) {
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
      <rect x="3.5" y="4" width="17" height="16" rx="2" />
      <circle cx="9" cy="10" r="2.2" />
      <path d="M5.8 16.2c.5-1.9 1.8-2.9 3.2-2.9s2.7 1 3.2 2.9" />
      <path d="M14.5 9h3.5M14.5 12h3.5M14.5 15h2.5" />
    </svg>
  )
}
