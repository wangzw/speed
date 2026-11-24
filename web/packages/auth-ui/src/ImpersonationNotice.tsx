/** The provider that ties the impersonation banner, the header indicator and
 * the account-menu end item to one dismissal state and one end attempt. */

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from 'react'
import type { ReactElement, ReactNode, Ref, RefObject } from 'react'
import Chip from '@mui/material/Chip'
import MenuItem from '@mui/material/MenuItem'
import Tooltip from '@mui/material/Tooltip'
import { useAuthUiTranslation } from './internal/translation.js'
import { ImpersonationBanner } from './ImpersonationBanner.js'
import type { ImpersonationBannerState } from './ImpersonationBanner.js'

/** The host supplies the impersonation status and the shared end attempt; the
 * provider owns the dismissal state and the region id. */
export interface ImpersonationNoticeProviderProps {
  /** The current impersonation, or null when none is active. */
  readonly impersonation: ImpersonationBannerState | null
  /** The effective identity's stable value. A change resets a dismissal, so a
   * different identity's notice is shown again. */
  readonly identityKey?: string
  /** Starts the host's one end-impersonation write attempt. */
  readonly onEnd: () => void
  /** Whether that attempt is in flight. */
  readonly pending?: boolean
  /** The attempt's failure code, rendered inside the banner. */
  readonly failureCode?: string | null
  /** The attempt's success message, announced in the banner's live region. */
  readonly successMessage?: string | null
  /** The subtree the provider wraps. */
  readonly children: ReactNode
}

/** What the consumers read from one provider. */
export interface ImpersonationNotice {
  /** The current impersonation, or null when none is active. */
  readonly impersonation: ImpersonationBannerState | null
  /** Whether the visible notice is hidden. */
  readonly dismissed: boolean
  /** The visible region's id, for the indicator's `aria-controls`. */
  readonly regionId: string
  /** Whether the shared end attempt is in flight. */
  readonly pending: boolean
  /** The attempt's failure code, or null. */
  readonly failureCode: string | null
  /** The attempt's success message, or null. */
  readonly successMessage: string | null
  /** Starts the shared end attempt. */
  readonly end: () => void
  /** Shows a dismissed notice (no-op while it is visible). */
  readonly show: () => void
  /** Hides the visible notice and moves focus to the indicator chip. */
  readonly dismiss: () => void
  /** Toggles the notice; used by the indicator chip. */
  readonly toggle: () => void
  /** The chip's element, the focus target a dismissal moves to. */
  readonly indicatorRef: RefObject<HTMLDivElement | null>
}

const ImpersonationNoticeContext = createContext<ImpersonationNotice | null>(
  null,
)

/**
 * Holds the dismissal state and exposes the shared end attempt to the banner,
 * the header indicator and the account-menu item; a failure of that attempt
 * resets a dismissal so the banner can show it.
 */
export function ImpersonationNoticeProvider({
  impersonation,
  identityKey = '',
  onEnd,
  pending = false,
  failureCode = null,
  successMessage = null,
  children,
}: ImpersonationNoticeProviderProps): ReactElement {
  const regionId = useId()
  const indicatorRef = useRef<HTMLDivElement | null>(null)

  // Only a change between two KNOWN grant ids is a new impersonation: a grant
  // id arriving after the dismissal (made while the status read was pending)
  // must not pop the notice back open.
  const currentGrantId = impersonation?.grantId ?? null
  const [dismissal, setDismissal] = useState<{
    readonly grantId: string | null
    readonly identityKey: string
  } | null>(null)
  const dismissed =
    dismissal !== null &&
    dismissal.identityKey === identityKey &&
    (dismissal.grantId === null ||
      currentGrantId === null ||
      dismissal.grantId === currentGrantId)

  // The failure renders inside the banner, so the notice comes back. Tracked
  // as the null -> code edge: a repeated failure of the same code is still new.
  const previousFailure = useRef<string | null>(null)
  useEffect(() => {
    if (failureCode !== null && previousFailure.current === null) {
      setDismissal(null)
    }
    previousFailure.current = failureCode
  }, [failureCode])

  const previousIdentity = useRef(identityKey)
  useEffect(() => {
    if (previousIdentity.current !== identityKey) {
      previousIdentity.current = identityKey
      setDismissal(null)
    }
  }, [identityKey])

  // A new grant resets it too, but only when both ids are known: the
  // absent-to-known edge is the status read filling in the same grant.
  const previousGrantId = useRef<string | null>(currentGrantId)
  useEffect(() => {
    if (
      previousGrantId.current !== null &&
      currentGrantId !== null &&
      previousGrantId.current !== currentGrantId
    ) {
      setDismissal(null)
    }
    previousGrantId.current = currentGrantId
  }, [currentGrantId])

  useEffect(() => {
    if (impersonation === null) {
      setDismissal(null)
    }
  }, [impersonation])

  const dismiss = useCallback(() => {
    setDismissal({ grantId: currentGrantId, identityKey })
    indicatorRef.current?.focus()
  }, [currentGrantId, identityKey])

  const show = useCallback(() => {
    setDismissal(null)
  }, [])

  const toggle = useCallback(() => {
    if (dismissed) {
      setDismissal(null)
    } else {
      dismiss()
    }
  }, [dismiss, dismissed])

  const value = useMemo<ImpersonationNotice>(
    () => ({
      impersonation,
      dismissed,
      regionId,
      pending,
      failureCode,
      successMessage,
      end: onEnd,
      show,
      dismiss,
      toggle,
      indicatorRef,
    }),
    [
      impersonation,
      dismissed,
      regionId,
      pending,
      failureCode,
      successMessage,
      onEnd,
      show,
      dismiss,
      toggle,
    ],
  )

  return (
    <ImpersonationNoticeContext.Provider value={value}>
      {children}
    </ImpersonationNoticeContext.Provider>
  )
}

/** The notice's shared state, or null outside a provider (a shell that
 * never impersonates). */
export function useImpersonationNotice(): ImpersonationNotice | null {
  return useContext(ImpersonationNoticeContext)
}

/** The host supplies the impersonation and the disclosure state; the chip
 * renders them and holds no state. */
export interface ImpersonationIndicatorProps {
  /** The current impersonation; null renders nothing. */
  readonly impersonation: ImpersonationBannerState | null
  /** Whether the banner is currently shown. */
  readonly expanded: boolean
  /** The visible region's id. Used by `aria-controls` while expanded. */
  readonly controls?: string
  /** Shows or hides the banner. Focus stays on the indicator. */
  readonly onToggle: () => void
  /** The chip's element, so the host can move focus to it on dismissal. */
  readonly ref?: Ref<HTMLDivElement>
  /** Renders the chip as an icon-only control, keeping the text chip's
   * accessible name and its `aria-expanded` / `aria-controls`. */
  readonly compact?: boolean
}

/** The compact chip's glyph, drawn decorative; the chip carries the name. */
function ImpersonatingGlyph() {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 24 24"
      width={16}
      height={16}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M3.5 12s3.1-5.2 8.5-5.2S20.5 12 20.5 12 17.4 17.2 12 17.2 3.5 12 3.5 12z" />
      <circle cx="12" cy="12" r="2.4" />
    </svg>
  )
}

/**
 * The compact "impersonating" chip: a disclosure control that renders for the
 * whole impersonation and is the focus target the banner's close control moves
 * to. Its accessible name is its visible text.
 */
export function ImpersonationIndicator({
  impersonation,
  expanded,
  controls,
  onToggle,
  ref,
  compact = false,
}: ImpersonationIndicatorProps): ReactElement | null {
  const { t } = useAuthUiTranslation()
  if (impersonation === null) {
    return null
  }
  const label = t('impersonation.impersonating')
  // describeChild keeps the tooltip a description, not the accessible name;
  // the name is the visible text, as WCAG 2.5.3 requires. The compact form
  // has no visible text, so the label becomes the name.
  return (
    <Tooltip
      describeChild
      title={
        expanded
          ? t('impersonation.hideHint')
          : t('impersonation.showHint')
      }
    >
      <Chip
        ref={ref}
        color="warning"
        size="small"
        label={compact ? undefined : label}
        aria-label={compact ? label : undefined}
        icon={compact ? <ImpersonatingGlyph /> : undefined}
        aria-expanded={expanded}
        aria-controls={expanded ? controls : undefined}
        onClick={onToggle}
        sx={{ fontWeight: 600 }}
      />
    </Tooltip>
  )
}

/** The host supplies the activation and divider options; the item reads the
 * notice provider for the impersonation and the shared end attempt. */
export interface ImpersonationEndMenuItemProps {
  /** Called after the item activates, typically to close the menu. */
  readonly onActivate?: () => void
  /** Whether a divider follows the item in its menu. */
  readonly divider?: boolean
}

/** The account menu's way back to the impersonation: it starts the same shared
 * end attempt the banner's control does. Renders nothing without an active
 * impersonation or outside the notice provider. */
export function ImpersonationEndMenuItem({
  onActivate,
  divider = false,
}: ImpersonationEndMenuItemProps): ReactElement | null {
  const notice = useImpersonationNotice()
  const { t } = useAuthUiTranslation()
  if (notice === null || notice.impersonation === null) {
    return null
  }
  return (
    <MenuItem
      disabled={notice.pending}
      divider={divider}
      onClick={() => {
        notice.end()
        onActivate?.()
      }}
    >
      {t(notice.pending ? 'impersonation.ending' : 'impersonation.end')}
    </MenuItem>
  )
}

/** The host chooses whether the banner's own close control renders; the
 * banner reads the notice provider for everything else. */
export interface ImpersonationNoticeBannerProps {
  /** Whether the banner's own close control renders. Defaults to true; false
   * removes that control only, while the indicator can still toggle the shared
   * hidden state. */
  readonly dismissible?: boolean
}

/** The banner bound to the enclosing notice provider: renders its
 * impersonation, shared end attempt and dismissal state, and nothing outside
 * the provider. */
export function ImpersonationNoticeBanner({
  dismissible = true,
}: ImpersonationNoticeBannerProps): ReactElement | null {
  const notice = useImpersonationNotice()
  if (notice === null) {
    return null
  }
  return (
    <ImpersonationBanner
      impersonation={notice.impersonation}
      onEnd={notice.end}
      pending={notice.pending}
      failureCode={notice.failureCode}
      successMessage={notice.successMessage}
      dismissed={notice.dismissed}
      onDismiss={dismissible ? notice.dismiss : undefined}
      regionId={notice.regionId}
    />
  )
}

/** The header chip bound to the enclosing notice provider: expanded while the
 * notice is visible, controlling its region, and carrying the indicator ref.
 * Renders nothing outside the provider or while no impersonation is active. */
export function ImpersonationNoticeIndicator(): ReactElement | null {
  const notice = useImpersonationNotice()
  if (notice === null) {
    return null
  }
  return (
    <ImpersonationIndicator
      impersonation={notice.impersonation}
      expanded={!notice.dismissed}
      controls={notice.regionId}
      onToggle={notice.toggle}
      ref={notice.indicatorRef}
    />
  )
}
