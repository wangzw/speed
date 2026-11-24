/** The notice a host mounts in its app frame while the session is
 * impersonating: a presentational banner over host-supplied state. */

import { useCallback, useEffect, useRef, useState } from 'react'
import type { ReactElement } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import IconButton from '@mui/material/IconButton'
import Typography from '@mui/material/Typography'
import type { Theme } from '@mui/material/styles'
import {
  AnnouncementRegion,
  useActivationGuard,
  useFormatInstant,
  visuallyHiddenSx,
} from '@speed/ui-kit'
import { useAuthUiTranslation } from './internal/translation.js'
import { InlineError } from './internal/inline-error.js'

/** What the host knows about the current impersonation. The tenant and
 * expiry come from the host's status read; when it could not read them
 * the banner and the end control still render. */
export interface ImpersonationBannerState {
  /** The impersonated account's display name, already resolved to text. */
  readonly subject: string
  /** The tenant identifier the impersonation runs in, when known. */
  readonly tenant?: string
  /** When the impersonation ends, when known. */
  readonly expiresAt?: Date
  /** The grant's stable identity: a new grant is a new impersonation, which a
   * dismissal resets on, unlike the subject, whose text may change within a
   * grant. */
  readonly grantId?: string
}

/** The host owns the impersonation status, the shared end attempt and the
 * dismissal; the banner owns the settlement it announces and the focus it
 * hands over when the impersonation ends. */
export interface ImpersonationBannerProps {
  /** The current impersonation, or null when none is active. */
  readonly impersonation: ImpersonationBannerState | null
  /** Starts the host's one end-impersonation write attempt. */
  readonly onEnd: () => void
  /** Whether that shared attempt is in flight. The end control stays
   * focusable and carries `aria-disabled` and `aria-busy` while it is,
   * and the account menu's own end item is disabled beside it. */
  readonly pending?: boolean
  /** The attempt's failure code, rendered inside the banner. */
  readonly failureCode?: string | null
  /** The attempt's success message, announced in the permanent live
   * region (and shown while the banner is still mounted). */
  readonly successMessage?: string | null
  /** Whether the viewer has hidden the visible notice. The live region
   * stays mounted and keeps announcing; the visible region is unmounted. */
  readonly dismissed?: boolean
  /** Hides the visible notice. The close control renders only when it is
   * supplied. */
  readonly onDismiss?: () => void
  /** The visible region's id, named by the header indicator's
   * `aria-controls`. */
  readonly regionId?: string
}

const bannerSx = {
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'center',
  columnGap: 0.5,
  rowGap: 1,
  px: 1,
  py: 0.5,
  minHeight: 40,
  // The role's own fill and label pair: contrastText is chosen to read on
  // main in both modes. Painting main here is what makes that pair true.
  backgroundColor: 'warning.main',
  color: 'warning.contrastText',
} as const

const detailsSx = {
  flexGrow: 1,
  flexBasis: 0,
  minWidth: 0,
  overflow: 'hidden',
  margin: 0,
} as const

const subjectSx = {
  display: 'inline-block',
  maxWidth: 'min(16ch, 100%)',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
  verticalAlign: 'bottom',
} as const

const tenantSx = {
  display: 'inline-block',
  maxWidth: 'min(24ch, 100%)',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
} as const

const controlsSx = (theme: Theme) => ({
  display: 'flex',
  alignItems: 'center',
  gap: 0.25,
  flexShrink: 0,
  [theme.breakpoints.down('sm')]: {
    flexBasis: '100%',
    justifyContent: 'flex-end',
  },
})

/** Stands in for the subject while the acting-as template is split, so
 * the subject alone can carry the bounded style. A private-use character,
 * outside anything a bundle would contain. */
const SUBJECT_SLOT = '\uE000'

/** The acting-as sentence with its subject interpolated into its own
 * bounded element. A bundle that does not carry the placeholder exactly
 * once falls back to the plain interpolated sentence. */
function ActingAs({ subject }: { readonly subject: string }): ReactElement {
  const { t } = useAuthUiTranslation()
  const parts = t('impersonation.actingAs', {
    subject: SUBJECT_SLOT,
  }).split(SUBJECT_SLOT)
  if (parts.length !== 2) {
    return <>{t('impersonation.actingAs', { subject })}</>
  }
  const [before, after] = parts as [string, string]
  return (
    <>
      {before}
      <Box component="span" title={subject} sx={subjectSx}>
        {subject}
      </Box>
      {after}
    </>
  )
}

function FactSeparator(): ReactElement {
  return (
    <Box
      component="span"
      aria-hidden="true"
      sx={{
        mx: 0.25,
        opacity: 0.75,
        '&::before': { content: '"·"' },
      }}
    />
  )
}

/** The close glyph, decorative: the IconButton carries the accessible
 * name. */
function HideIcon(): ReactElement {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 24 24"
      width={20}
      height={20}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
    >
      <path d="M6 6l12 12M18 6L6 18" />
    </svg>
  )
}

/** The expiry line's text, or null when there is neither an instant nor a
 * formatted value; the formatter names the display zone. */
function expiryText(
  formatTime: ReturnType<typeof useFormatInstant>,
  expiresAt: Date | null | undefined,
): string | null {
  if (expiresAt === undefined || expiresAt === null) {
    return null
  }
  const text = formatTime(expiresAt, { style: 'long' })
  return text === '' ? null : text
}

/**
 * Renders the banner and its permanent live region from host-supplied state,
 * and starts the host's one end attempt; it never reads the session or counts
 * down to `expiresAt`.
 */
export function ImpersonationBanner({
  impersonation,
  onEnd,
  pending = false,
  failureCode = null,
  successMessage = null,
  dismissed = false,
  onDismiss,
  regionId,
}: ImpersonationBannerProps) {
  const { t } = useAuthUiTranslation()
  const formatTime = useFormatInstant()
  // Refuse the click through the guard, not a native disabled state, so the
  // focused end control keeps focus while its attempt runs.
  const guardedEnd = useActivationGuard(onEnd, { busy: pending })

  // React calls a ref callback with null before the node leaves the document,
  // so the active element still names the end control there; remember whether
  // it held focus when the impersonation cleared.
  const settlementRef = useRef<HTMLDivElement | null>(null)
  const endControl = useRef<HTMLButtonElement | null>(null)
  const endHeldFocus = useRef(false)
  const setEndControl = useCallback((node: HTMLButtonElement | null) => {
    if (node !== null) {
      endControl.current = node
      endHeldFocus.current = false
      return
    }
    if (
      endControl.current !== null &&
      endControl.current === document.activeElement
    ) {
      endHeldFocus.current = true
    }
    endControl.current = null
  }, [])

  // Hand focus to the settlement section only when the end control left with
  // it; an end from the account menu or focus the operator moved stays put.
  useEffect(() => {
    if (impersonation !== null || !endHeldFocus.current) {
      return
    }
    endHeldFocus.current = false
    if (document.activeElement === document.body) {
      settlementRef.current?.focus()
    }
  }, [impersonation])

  // The region lives for the component's whole life and writes each message
  // empty first, so a first mount that already impersonates is announced; the
  // subject and ended flag are already settled in this commit.
  const subject = impersonation?.subject ?? null
  const [displayedSubject, setDisplayedSubject] = useState<string | null>(
    subject,
  )
  const [ended, setEnded] = useState(false)
  if (subject !== displayedSubject) {
    setEnded(subject === null && displayedSubject !== null)
    setDisplayedSubject(subject)
  }

  const announcement =
    displayedSubject === null
      ? null
      : t('impersonation.actingAs', { subject: displayedSubject })
  const settlement = successMessage ?? (ended ? t('impersonation.ended') : null)
  const liveText = settlement ?? announcement

  const expiry = expiryText(formatTime, impersonation?.expiresAt)

  return (
    <>
      {/* The one live region, mounted for the component's whole life. The
          hidden branch keeps MUI's `overflow: auto` message slot from becoming
          a scroll container, which Chromium treats as a keyboard focus stop. */}
      <Box
        ref={settlementRef}
        // The focus target when the end control leaves with focus: a
        // programmatic target, not a Tab stop.
        tabIndex={-1}
        sx={
          settlement !== null
            ? {
                width: '100%',
                // The written marker appears with the text, so the skin stays
                // clipped through the empty frames instead of painting a box.
                '&:not(:has([data-announcement-written]))': {
                  ...visuallyHiddenSx,
                  '& .MuiAlert-message': { overflow: 'hidden' },
                },
              }
            : {
                ...visuallyHiddenSx,
                '& .MuiAlert-message': { overflow: 'hidden' },
              }
        }
      >
        {/* The live role and the empty-first write belong to the shared region
            inside; the Alert carries no role, so the announcement is made
            once. */}
        <Alert severity="success" role="none" sx={{ width: '100%' }}>
          <AnnouncementRegion message={liveText} />
        </Alert>
      </Box>
      {impersonation !== null && !dismissed && (
        <Box
          component="section"
          id={regionId}
          aria-label={t('impersonation.regionLabel')}
          sx={bannerSx}
        >
          <Typography component="p" variant="caption" sx={detailsSx}>
            <Box component="span" sx={{ fontWeight: 600 }}>
              <ActingAs subject={impersonation.subject} />
            </Box>
            {impersonation.tenant !== undefined && (
              <>
                <FactSeparator />
                <Box
                  component="span"
                  title={impersonation.tenant}
                  sx={tenantSx}
                >
                  {t('impersonation.tenant', {
                    tenant: impersonation.tenant,
                  })}
                </Box>
              </>
            )}
            {expiry !== null && (
              <>
                <FactSeparator />
                <Box component="span">
                  {t('impersonation.expiresAt', { expiresAt: expiry })}
                </Box>
              </>
            )}
            <FactSeparator />
            <Box component="span">
              {t('impersonation.managementUnavailable')}
            </Box>
          </Typography>
          <Box sx={controlsSx}>
            <Button
              ref={setEndControl}
              type="button"
              size="small"
              variant="outlined"
              color="inherit"
              aria-disabled={pending || undefined}
              aria-busy={pending || undefined}
              onClick={guardedEnd}
              sx={{ px: 0.75 }}
            >
              {t(
                pending
                  ? 'impersonation.ending'
                  : 'impersonation.end',
              )}
            </Button>
            {onDismiss !== undefined && (
              <IconButton
                size="small"
                color="inherit"
                aria-label={t('impersonation.hide')}
                onClick={onDismiss}
              >
                <HideIcon />
              </IconButton>
            )}
          </Box>
          {failureCode !== null && (
            <Box sx={{ flexBasis: '100%' }}>
              <InlineError code={failureCode} />
            </Box>
          )}
        </Box>
      )}
    </>
  )
}
