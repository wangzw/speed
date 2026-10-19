/**
 * The impersonation notice's shared surface: the provider that ties the
 * banner, the header indicator and the account-menu end item to one
 * dismissal state and one end-impersonation write attempt.
 *
 * The host mounts ImpersonationNoticeProvider around its shell and feeds
 * it what it already knows: the current impersonation (null when none),
 * the effective identity (`identityKey`), the one attempt's callbacks and
 * state (`onEnd`, `pending`, `failureCode`, `successMessage`). The three
 * consumers then render from that one source:
 *
 *  - ImpersonationBanner (the banner slot) renders the facts and the end
 *    control, and its close control hides the visible notice.
 *  - ImpersonationIndicator (the user-menu slot, before the account
 *    menu) is the compact "impersonating" chip. It is a disclosure
 *    control: `aria-expanded` reports whether the notice is visible,
 *    `aria-controls` names the banner's region, and activating it
 *    toggles the notice without moving focus off the chip. It renders
 *    for the whole impersonation, dismissed or not -- the persistent
 *    visible indication that an impersonation is in force.
 *  - ImpersonationEndMenuItem (inside the account menu) starts the same
 *    attempt, disabled and labelled busy while it is in flight.
 *
 * A dismissal is memory state on this provider, never storage: a reload
 * clears it. It resets when the impersonation notice keyed here changes
 * -- a new grant (`grantId`), a new effective identity (`identityKey`),
 * the impersonation ending -- and when the shared attempt reports a
 * failure, because the failure is rendered inside the banner and the
 * notice must come back to show it. Status re-reads that keep the same
 * grant, route changes and focus re-reads do not reset it.
 *
 * When the close control hides the notice, focus moves to the indicator
 * chip rather than being lost to the document body; the chip is already
 * mounted, so the focus transfer is immediate.
 *
 * Two consumers read the provider themselves so a host does not have to
 * wire the controlled props by hand: ImpersonationNoticeBanner renders the
 * banner from the notice's state (with the provider's region id, and the
 * close control unless `dismissible` is false), and
 * ImpersonationNoticeIndicator renders the header chip bound to the same
 * disclosure state and focus target. Both render nothing outside the
 * provider.
 */

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

export interface ImpersonationNoticeProviderProps {
  /** The current impersonation, or null when none is active. */
  readonly impersonation: ImpersonationBannerState | null
  /**
   * The effective identity's stable value (the acting account id, plus
   * whatever the host counts as an identity change). A change resets a
   * dismissal, so a different identity's notice is shown again.
   */
  readonly identityKey?: string
  /** Starts the host's one end-impersonation write attempt. */
  readonly onEnd: () => void
  /** Whether that attempt is in flight. */
  readonly pending?: boolean
  /** The attempt's failure code, rendered inside the banner. */
  readonly failureCode?: string | null
  /** The attempt's success message, announced in the banner's live region. */
  readonly successMessage?: string | null
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

  // The notice the dismissal belongs to. Only a change between two KNOWN
  // grant ids is a new impersonation: a grant id arriving after the
  // dismissal (the dismissal was made while the status read was still
  // pending) must not pop the notice back open. A grant id that is
  // unknown on either side is treated as the same notice.
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

  // A failed end attempt must be visible: the failure renders inside the
  // banner, so the notice comes back. Tracked as the null -> code edge,
  // because the attempt clears its outcome when it starts and a second
  // failure of the same code is still a new failure.
  const previousFailure = useRef<string | null>(null)
  useEffect(() => {
    if (failureCode !== null && previousFailure.current === null) {
      setDismissal(null)
    }
    previousFailure.current = failureCode
  }, [failureCode])

  // Any identity change resets the dismissal. The key the host passes
  // covers the acting account and its identity generation.
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

  // An end resets it, so the next impersonation starts visible.
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
}

/**
 * The compact "impersonating" chip. It renders for the whole
 * impersonation and is the focus target the banner's close control moves
 * to, so a dismissal never drops focus into the document. Its name is
 * its visible text; `aria-expanded` makes the notice's state explicit and
 * `aria-controls` names the visible region while it exists.
 */
export function ImpersonationIndicator({
  impersonation,
  expanded,
  controls,
  onToggle,
  ref,
}: ImpersonationIndicatorProps): ReactElement | null {
  const { t } = useAuthUiTranslation()
  if (impersonation === null) {
    return null
  }
  // describeChild: the tooltip becomes the chip's description, not its
  // accessible name. The name is the visible "Impersonating" text, as
  // WCAG 2.5.3 requires; MUI would otherwise put the hint into aria-label
  // and replace it.
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
        label={t('impersonation.impersonating')}
        aria-expanded={expanded}
        aria-controls={expanded ? controls : undefined}
        onClick={onToggle}
        sx={{ fontWeight: 600 }}
      />
    </Tooltip>
  )
}

export interface ImpersonationEndMenuItemProps {
  /** Called after the item activates, typically to close the menu. */
  readonly onActivate?: () => void
  /** Whether a divider follows the item in its menu. */
  readonly divider?: boolean
}

/**
 * The account menu's way back to the impersonation: it starts the same
 * shared end attempt the banner's control does, so both are disabled and
 * marked busy together. Renders nothing when no impersonation is active
 * or when the menu is mounted outside the notice provider.
 */
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

export interface ImpersonationNoticeBannerProps {
  /** Whether the banner's own close control renders. Defaults to true.
   * False removes that control only: the provider's shared hidden state
   * is still toggled by `ImpersonationNoticeIndicator`'s click, so a host
   * that needs the notice to stay visible throughout renders neither the
   * indicator nor a dismissible banner. */
  readonly dismissible?: boolean
}

/**
 * The banner bound to the enclosing notice provider: it renders the
 * provider's impersonation, shared end attempt and dismissal state, with
 * the provider's own region id so the indicator's `aria-controls` names
 * it. Renders nothing outside the provider; inside one it renders the
 * banner's permanent live region even while no impersonation is active
 * (that is the controlled component's own contract).
 */
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

/**
 * The header chip bound to the enclosing notice provider: expanded while
 * the notice is visible, controlling the provider's region, toggling the
 * same dismissal state, and carrying the provider's indicator ref so a
 * dismissal moves focus here. Renders nothing outside the provider or
 * while no impersonation is active.
 */
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
