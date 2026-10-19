/**
 * ImpersonationBanner: the notice a host mounts in its app frame while the
 * session is impersonating.
 *
 * The component is purely presentational. It holds no session, issues no
 * request and reads no auth-core hook: the host tells it whether an
 * impersonation is current (`impersonation`), whether the one shared end
 * attempt is in flight (`pending`), that attempt's failure code
 * (`failureCode`) and success message (`successMessage`), and binds the
 * attempt itself (`onEnd`). The host mounts it for the whole shell, so the
 * banner can appear and disappear on any page without a remount of the
 * component itself.
 *
 * One polite live region is mounted for the component's lifetime, never
 * conditionally: empty while nothing is happening, filled with the
 * "acting as" announcement when an impersonation appears, and filled with
 * the end confirmation once the host's attempt settles (either its own
 * `successMessage`, or the "impersonation ended" text the component
 * announces when the impersonation it was displaying disappears -- the
 * latter covers an end made from another entry point, such as the account
 * menu). The announcement is filled on the tick after the region is in
 * the tree, so a first mount that already has an impersonation -- the
 * frame mounting after a start, or a whole-page reload while
 * impersonating -- is announced too. The confirmation therefore survives
 * the banner's own disappearance (the host clears `impersonation` after
 * re-reading the session) and stands until the next attempt or a page
 * reload replaces it.
 *
 * The visible notice is a named `region` landmark. Closing it
 * (`onDismiss`) unmounts that visible region while the live region above
 * stays mounted and keeps announcing; the host holds the dismissal and
 * moves focus to the header indicator. This component only renders the
 * result.
 *
 * The end control does not depend on `tenant` or `expiresAt`: those are
 * the host's status read, both are optional, and the control renders and
 * works even when neither arrived. The control starts the host's one
 * attempt: while `pending` it is disabled and busy, and a failure renders
 * the answer's code text inside the banner while leaving the control
 * retryable. A bare 404 answering "there is no impersonation to end" is
 * the host's concern: its attempt treats that as settled, and this
 * component never inspects an HTTP status -- a failure code that reaches
 * it is rendered like any other.
 *
 * The component never counts down and never acts at `expiresAt`; the
 * host re-reads the session when the impersonation expires.
 *
 * Layout: the four facts are one inline-flowing text block -- one
 * paragraph whose sentences run together and break by word (or
 * character) wherever the line ends, never one sentence per line. The end
 * and close controls share the row with that text, flush right; only
 * below `xs` do they take their own line. The subject and the tenant
 * identifier are capped and ellipsized -- the full value riding on the
 * element's title -- so neither an arbitrary display name nor a long
 * identifier can grow the bar past its single-line target or push the
 * controls out of it.
 */

import { useEffect, useRef, useState } from 'react'
import type { ReactElement } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import IconButton from '@mui/material/IconButton'
import Typography from '@mui/material/Typography'
import type { Theme } from '@mui/material/styles'
import { formatInstant } from '@speed/i18n'
import { InlineSuccess, useDisplayTimeZone, visuallyHiddenSx } from '@speed/ui-kit'
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
  /** The impersonation grant's stable identity. A new grant is a new
   * impersonation, which is what a dismissal resets on -- not the subject,
   * whose text may change without the grant changing. */
  readonly grantId?: string
}

export interface ImpersonationBannerProps {
  /** The current impersonation, or null when none is active. */
  readonly impersonation: ImpersonationBannerState | null
  /** Starts the host's one end-impersonation write attempt. */
  readonly onEnd: () => void
  /** Whether that shared attempt is in flight. The end control is
   * disabled and marked busy while it is, and the account menu's own end
   * item is disabled beside it. */
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

/** The notice's one wrapping row: the facts, then the controls. The 40px
 * floor is the design's single-line target (40 to 48px): one caption line
 * beside the small controls settles inside that range with the 4px
 * vertical padding, and each further text line grows the bar by a caption
 * line. The horizontal room is held as tight as the target allows: the
 * fixed sentences plus a normal display name fill the line at 1280, so
 * every pixel of padding and gap around them is width the text cannot
 * use. */
const bannerSx = {
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'center',
  columnGap: 0.5,
  rowGap: 1,
  px: 1,
  py: 0.5,
  minHeight: 40,
  backgroundColor: 'warning.dark',
  color: 'warning.contrastText',
} as const

/** The facts block: one paragraph, so the sentences share one text flow
 * and break at the line end instead of each occupying a line of its own.
 * Its flex base is zero, so line breaking never pushes the controls onto
 * a second row when the viewport is wide enough for them: the paragraph
 * takes whatever width the controls leave and wraps inside it. */
const detailsSx = {
  flexGrow: 1,
  flexBasis: 0,
  minWidth: 0,
  overflow: 'hidden',
  margin: 0,
} as const

/** The acting-as subject stays one line and ellipsizes, the full value on
 * the element's title. Its cap is the width the other three facts leave
 * at 1280, so a display name of any length keeps the paragraph inside the
 * design's single-line target; a normal name renders untruncated. Bottom
 * alignment keeps the inline block's text on the sentence's baseline. */
const subjectSx = {
  display: 'inline-block',
  maxWidth: 'min(16ch, 100%)',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
  verticalAlign: 'bottom',
} as const

/** A long tenant identifier stays one line and ellipsizes; the full value
 * rides on the element's title. It is an inline block inside the facts
 * paragraph, so it keeps the surrounding text's baseline. */
const tenantSx = {
  display: 'inline-block',
  maxWidth: 'min(24ch, 100%)',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
} as const

/** The controls sit at the end of the row and never shrink. Below `xs`
 * they take a line of their own, flush right, so the facts keep the full
 * width on a phone. */
const controlsSx = (theme: Theme) => ({
  display: 'flex',
  alignItems: 'center',
  gap: 0.25,
  marginLeft: 'auto',
  flexShrink: 0,
  [theme.breakpoints.down('sm')]: {
    flexBasis: '100%',
    marginLeft: 0,
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

/** The separator between facts, decorative. */
function FactSeparator(): ReactElement {
  return (
    <Box
      component="span"
      aria-hidden="true"
      // Painted through CSS, not a text node: it is punctuation between
      // the facts, not user-facing copy the host could translate.
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

/** Formats the expiry in the active language and the host's display time
 * zone (the device zone when none is provided); a malformed date, or a
 * zone the engine rejects, renders nothing rather than throwing out of
 * Intl. The host's zone is unvalidated by the provider, so a stale or
 * misspelled IANA name must degrade to "no expiry line", not crash the
 * render; the shared formatter owns that guard. */
function formatExpiry(
  expiresAt: Date,
  language: string,
  timeZone: string | undefined,
): string | null {
  const text = formatInstant(expiresAt, language, { timeZone })
  return text === '' ? null : text
}

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
  const { t, i18n } = useAuthUiTranslation()
  // The host's display time zone when one is provided; absent, the device
  // zone (the rendering before the provider existed).
  const timeZone = useDisplayTimeZone()

  // The announcement is committed one tick after the impersonation
  // appears. The live region is therefore already in the tree and empty
  // when its text arrives, including on the component's very first
  // mount with an impersonation already current -- a start that mounts
  // the frame, or a reload while impersonating -- which would otherwise
  // be silent. A new impersonation also replaces the previous one's
  // confirmation: a standing "impersonation ended" would describe a
  // context that is gone. The transition from a displayed impersonation
  // to none is a settlement by whatever entry point ended it, so the
  // ended text is announced for that edge too.
  const subject = impersonation?.subject ?? null
  const [announcedSubject, setAnnouncedSubject] = useState<string | null>(null)
  const [ended, setEnded] = useState(false)
  const previousSubject = useRef<string | null>(subject)
  useEffect(() => {
    const previous = previousSubject.current
    previousSubject.current = subject
    setAnnouncedSubject(subject)
    if (subject !== null) {
      setEnded(false)
    } else if (previous !== null) {
      setEnded(true)
    }
  }, [subject])

  const announcement =
    announcedSubject === null
      ? null
      : t('impersonation.actingAs', { subject: announcedSubject })
  const settlement = successMessage ?? (ended ? t('impersonation.ended') : null)
  const liveText = settlement ?? announcement

  const expiry =
    impersonation?.expiresAt == null
      ? null
      : formatExpiry(impersonation.expiresAt, i18n.language ?? 'en-US', timeZone)

  return (
    <>
      {/* The component's one live region, mounted for its whole life:
          empty while idle, the start announcement while impersonating,
          and the end confirmation afterwards. Its confirmation stays
          here after the visible banner below is hidden or unmounted.
          MUI's message slot carries `overflow: auto`; squeezed to the
          1px clipped width it would overflow into a scroll container,
          which Chromium treats as a keyboard focus stop. The hidden
          branch keeps the slot non-scrollable, so the first Tab in the
          banner reaches the end control. */}
      <Box
        sx={
          settlement !== null
            ? { width: '100%' }
            : {
                ...visuallyHiddenSx,
                '& .MuiAlert-message': { overflow: 'hidden' },
              }
        }
      >
        <InlineSuccess message={liveText} />
      </Box>
      {impersonation !== null && !dismissed && (
        <Box
          component="section"
          id={regionId}
          aria-label={t('impersonation.regionLabel')}
          sx={bannerSx}
        >
          {/* One paragraph: the four statements share one text flow, so
              the line breaks fall where the width ends, not between
              sentences. */}
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
              type="button"
              size="small"
              variant="outlined"
              color="inherit"
              loading={pending}
              onClick={onEnd}
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
            // The failure belongs to the banner's own width, below the
            // facts and the controls, not beside them.
            <Box sx={{ flexBasis: '100%' }}>
              <InlineError code={failureCode} />
            </Box>
          )}
        </Box>
      )}
    </>
  )
}
