/**
 * The account page and the social-binding callback beside it.
 *
 * @speed/account-ui ships the account family as sections, not a routed
 * screen: the page, its heading and the surrounding chrome are the host's.
 * Each pane is a page of its own (src/routes.tsx, src/paths.ts) and composes
 * a ui-kit PageLayout naming it; the pane's blocks follow as one card each,
 * separated by the frame's stack, with no gate to open first.
 *
 * The contact-verification landings reach the profile page with a flag:
 * `verify=1` and `change=email` open the verification surface there, which
 * is why the banner's links and the post-registration redirect keep
 * working unchanged.
 *
 * - profile: the account's username and display name (edited in place and
 *   saved when the field loses focus), the email addresses and the phone
 *   number, one card each.
 * - security: the password, the sign-in ways, two-step verification when
 *   `authn.mfa` is served, and the social bindings, one card each.
 * - sessions: SessionsSection, already a device list with revoke actions.
 * - preferences: the theme preset and color-mode selects, and the language
 *   and time zone selects.
 *
 * The page reads the caller's profile once through the generated
 * `useAuthnProfileGet` for the profile pane's facts and the verification
 * landing. The sections keep their own reads and async states (loading,
 * empty, error) from the account-ui namespace, so nothing here repeats a
 * spinner or an error block.
 *
 * The account page is also where the user's appearance is chosen: the
 * appearance and color-mode sections sit in the preferences pane, wired to the
 * theme provider's preset and preference and to the device's stored choices,
 * so a pick applies immediately and survives a reload (src/chrome.tsx holds
 * the storage helpers). The preset names are this bundle's copy; the package
 * renders the options the host hands it. The color-mode section writes the
 * account first, and only a confirmed write is applied to the provider and
 * mirrored to the device.
 *
 * The phone write (account-ui's PhoneSection) is mounted only when the
 * deployment serves the `authn.phone` feature: its two routes are registered
 * only when the deployment delivers an SMS code provider (an assembly that
 * names authn.sms), and this skeleton's baseline carries no SMS family -- the
 * family needs a carrier's credentials, so it is a product's to add (see
 * AGENTS.md and the server's baseline criterion). A section whose submit would
 * be answered 404 is a write the page cannot keep, so a deployment without the
 * feature offers none; a product that adds the SMS family gets the block here
 * beside the other writes.
 *
 * The two-step block is offered only when `authn.mfa` is served, and the
 * identifier section learns from `authn.identifiers.email` whether its add
 * face may offer an email (a deployment without it records usernames only).
 *
 * The social-bindings section takes the assembled session (it starts the
 * `link` flow the callback completes) and the callback route the flow
 * lands back on. The skeleton configures no OAuth provider, so the
 * deployment's discovery document carries no redirect method and the
 * add area stays empty; a product that configures one gets its channels
 * from that same document. The callback route is mounted beside the page
 * (src/routes.tsx) for that product.
 *
 * The callback page renders account-ui's BindingCallbackHandler: the flow
 * model completes the provider hand-off server-side and the handler reads
 * the `link` flow back here, then returns the viewer to the account page.
 * The client carries only the access token from step-up-gated writes; the
 * site's own signal handling stays inside account-ui. Its absent slot is the
 * app's 404: a deployment that configures no redirect method has no binding
 * round trip at all.
 */

import type { MouseEventHandler, ReactElement, ReactNode, Ref } from 'react'
import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from 'react'
import type { Theme } from '@mui/material/styles'
import Box from '@mui/material/Box'
import Card from '@mui/material/Card'
import Chip from '@mui/material/Chip'
import Button from '@mui/material/Button'
import Portal from '@mui/material/Portal'
import Stack from '@mui/material/Stack'
import Table from '@mui/material/Table'
import TableBody from '@mui/material/TableBody'
import TableCell from '@mui/material/TableCell'
import TableRow from '@mui/material/TableRow'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  ACCOUNT_UI_NAMESPACE,
  AppearanceSection,
  BindingCallbackHandler,
  bindingReturnTarget,
  ColorModeSection,
  ContactVerificationSection,
  IdentifiersSection,
  MfaSection,
  PasswordSection,
  PhoneSection,
  PreferencesSection,
  ProfileSection,
  SessionsSection,
  StepUpDialog,
  SocialBindingsSection,
  useSocialMethodsAvailable,
} from '@speed/account-ui'
import { useAuthState, useFeature, useStepUpGate } from '@speed/auth-core'
import { useTranslation } from '@speed/i18n'
import {
  useLocationPath,
  useNavigate,
} from '@speed/route-kit'
import { themePresets } from '@speed/theme-presets'
import type { AppThemeModePreference } from '@speed/ui-kit'
import {
  ActionButton,
  ConfirmDialog,
  FORM_MAX_WIDTH,
  PageHeader,
  PageLayout,
  SectionSurfaceProvider,
  useAppThemeMode,
  useAppThemePreset,
  useWriteFeedback,
  WriteFeedbackRegion,
} from '@speed/ui-kit'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnProfileGetQueryKey,
  useAuthnLoginRemoveIdentifier,
  useAuthnProfileGet,
} from '../app-api/index.js'
import { useAppServices } from '../app-services.js'
import { CallbackLandingNotice } from '../callback-error-notice.js'
import {
  persistColorModePreference,
  persistThemePreset,
} from '../chrome.js'
import {
  forgetInvitationLanding,
  pendingInvitationLanding,
} from '../invitation-return.js'
import type { AccountSection } from '../paths.js'
import {
  ACCOUNT_CALLBACK_PATH,
  ACCOUNT_SECTION_PATHS,
} from '../paths.js'
import { navigateToAuthorizationUrl } from '../sign-in-navigation.js'
import { useCallbackLanding } from '../use-callback-landing.js'
import { NotFoundPage } from './not-found-page.js'
import { DEMO_NAMESPACE } from '../resources.js'



/**
 * The left inset a block's content takes to start on its title's rail: the
 * fixed icon column plus the grid gap the title sits after. The column stays
 * 2rem; the gap and therefore the inset follow the theme's spacing base, so a
 * preset with a different spacing unit keeps the heading and the items on one
 * vertical line.
 */
function titleRailInset(theme: Theme): string {
  return `calc(2rem + ${theme.spacing(2)})`
}

interface SecurityRowProps {
  readonly icon: ReactNode
  readonly title: string
  /** The state, shown beside the title. */
  readonly badge?: ReactNode
  readonly description: string
  readonly actionLabel: string
  /** A link action: the row leads to the pane that manages it. */
  readonly actionHref?: string
  readonly actionOnClick?: MouseEventHandler<HTMLElement>
  readonly expanded?: boolean
  readonly onToggle?: () => void
  readonly children?: ReactNode
  /**
   * Where the body's left edge sits: the row's title column, or its icon
   * column when the body is itself a list of iconed rows.
   */
  readonly alignBody?: 'title' | 'icon'
  readonly testId: string
}

/** One row of the security list: icon, title and state, one action. */
function SecurityRow({
  icon,
  title,
  badge,
  description,
  actionLabel,
  actionHref,
  actionOnClick,
  expanded = false,
  onToggle,
  children,
  alignBody = 'title',
  testId,
}: SecurityRowProps): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const titleId = useId()
  const bodyId = `${testId}-body`
  const expandable = onToggle !== undefined
  const open = expandable && expanded
  return (
    <Card
      component="section"
      variant="outlined"
      aria-labelledby={titleId}
      sx={{ p: 3 }}
    >
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: '2rem minmax(0, 1fr) auto',
          columnGap: 2,
          alignItems: 'center',
          px: 0,
          py: 0,
        }}
      >
        <Box sx={{ display: 'flex', color: 'text.secondary' }}>{icon}</Box>
        <Box sx={{ minWidth: 0 }}>
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: 1,
              flexWrap: 'wrap',
            }}
          >
            <Typography id={titleId} variant="h5" component="h2">
              {title}
            </Typography>
            {badge}
          </Box>
          <Typography variant="body2" color="text.secondary" sx={{ mt: 0.5 }}>
            {description}
          </Typography>
        </Box>
        {actionHref !== undefined ? (
          <Button
            component="a"
            href={actionHref}
            onClick={actionOnClick}
            size="small"
            variant="outlined"
            data-testid={testId}
          >
            {actionLabel}
          </Button>
        ) : expandable ? (
          <Button
            type="button"
            size="small"
            variant="outlined"
            aria-expanded={expanded}
            aria-controls={bodyId}
            data-testid={testId}
            onClick={onToggle}
          >
            {open ? t('account.rows.action.collapse') : actionLabel}
          </Button>
        ) : null}
      </Box>
      {open ? (
        <Box
          id={bodyId}
          role="group"
          aria-label={title}
          data-testid={`${testId}-body`}
          sx={{ mt: 2, pl: alignBody === 'icon' ? 0 : titleRailInset }}
        >
          {children}
        </Box>
      ) : null}
    </Card>
  )
}

function MailIcon(): ReactElement {
  return (
    <Box component="svg" {...ICON_PROPS}>
      <rect x="3" y="5" width="18" height="14" rx="2" />
      <path d="M3 7l9 6 9-6" />
    </Box>
  )
}

function PhoneIcon(): ReactElement {
  return (
    <Box component="svg" {...ICON_PROPS}>
      <rect x="7" y="3" width="10" height="18" rx="2" />
      <path d="M11 18h2" />
    </Box>
  )
}

function DeviceIcon(): ReactElement {
  return (
    <Box component="svg" {...ICON_PROPS}>
      <rect x="3" y="5" width="18" height="11" rx="2" />
      <path d="M8 20h8M12 16v4" />
    </Box>
  )
}

function PaletteIcon(): ReactElement {
  return (
    <Box component="svg" {...ICON_PROPS}>
      <path d="M12 3a9 9 0 1 0 0 18c1.4 0 2-1 2-2 0-.8-.6-1.2-.6-2 0-.8.7-1.5 1.6-1.5H17a4 4 0 0 0 4-4c0-4.7-4-8.5-9-8.5z" />
      <circle cx="7.5" cy="12" r="1" fill="currentColor" />
      <circle cx="9.5" cy="8" r="1" fill="currentColor" />
      <circle cx="14" cy="7" r="1" fill="currentColor" />
    </Box>
  )
}

const ICON_PROPS = {
  viewBox: '0 0 24 24',
  width: 20,
  height: 20,
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 1.7,
  strokeLinecap: 'round',
  strokeLinejoin: 'round',
  'aria-hidden': true,
  focusable: false,
} as const


function KeyIcon(): ReactElement {
  return (
    <Box component="svg" {...ICON_PROPS}>
      <circle cx="8" cy="15" r="4" />
      <path d="M11 12l9-9M17 6l3 3M14 9l2 2" />
    </Box>
  )
}

function ShieldIcon(): ReactElement {
  return (
    <Box component="svg" {...ICON_PROPS}>
      <path d="M12 3l7 3v5c0 4.5-3 8-7 10-4-2-7-5.5-7-10V6l7-3z" />
      <path d="M9 12l2 2 4-4" />
    </Box>
  )
}

function UserIcon(): ReactElement {
  return (
    <Box component="svg" {...ICON_PROPS}>
      <circle cx="12" cy="8" r="3.5" />
      <path d="M4.5 20c1.5-3.3 4.2-5 7.5-5s6 1.7 7.5 5" />
    </Box>
  )
}

function LinkIcon(): ReactElement {
  return (
    <Box component="svg" {...ICON_PROPS}>
      <path d="M10 14a5 5 0 0 0 7 0l3-3a5 5 0 0 0-7-7l-1 1" />
      <path d="M14 10a5 5 0 0 0-7 0l-3 3a5 5 0 0 0 7 7l1-1" />
    </Box>
  )
}

interface AccountSectionProps {
  /** The block's label, already translated. */
  readonly title: string
  /** The block's leading icon, like the security blocks'. */
  readonly icon?: ReactNode
  /** One sentence under the title, like the security blocks' descriptions. */
  readonly description?: string
  /** The action at the header's right, beside the title block. */
  readonly action?: ReactNode
  /** The section element, for a focus landing when its content goes away. */
  readonly sectionRef?: Ref<HTMLElement>
  /** Makes the section a programmatic focus target; -1 keeps it out of Tab order. */
  readonly sectionTabIndex?: number
  /** The section's own surface. */
  readonly children: ReactNode
}

/**
 * One block of a pane: a card carrying its own title (with the block's
 * icon) and, beneath it, the block's fields. The card is the block's
 * surface -- a pane is a stack of these, not one sheet subdivided by
 * headings.
 */
function AccountSection({
  title,
  icon,
  description,
  action,
  sectionRef,
  sectionTabIndex,
  children,
}: AccountSectionProps): ReactElement {
  const titleId = useId()
  return (
    <Card
      component="section"
      ref={sectionRef}
      tabIndex={sectionTabIndex}
      variant="outlined"
      aria-labelledby={titleId}
      sx={{ p: 3 }}
    >
      {/* The icon column is fixed at 2rem with the same gap the security
          blocks use, so the block title lands on the same rail as its
          items. */}
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns:
            action === undefined ? '2rem minmax(0, 1fr)' : '2rem minmax(0, 1fr) auto',
          columnGap: 2,
          alignItems: 'center',
        }}
      >
        {icon === undefined ? null : (
          <Box
            sx={{
              display: 'flex',
              color: 'text.secondary',
              flexShrink: 0,
            }}
          >
            {icon}
          </Box>
        )}
        <Box sx={{ minWidth: 0 }}>
          <Typography id={titleId} variant="h5" component="h2">
            {title}
          </Typography>
          {description === undefined ? null : (
            <Typography
              variant="body2"
              color="text.secondary"
              sx={{ mt: 0.5 }}
            >
              {description}
            </Typography>
          )}
        </Box>
        {action === undefined ? null : <Box>{action}</Box>}
      </Box>
      {/* The block's content shares the title's rail: the icon column and the
          gap before the title are skipped, so every item starts where the
          block's own name starts. */}
      <Box sx={{ mt: 2, pl: icon === undefined ? 0 : titleRailInset }}>
        {children}
      </Box>
    </Card>
  )
}

interface AccountItemProps {
  readonly title: string
  /** The item's detail or control, under the title. */
  readonly children?: ReactNode
  /** The action at the title's right. */
  readonly action?: ReactNode
}

/** One item of a block: a title, its action, and the detail beneath it. */
function AccountItem({
  title,
  children,
  action,
}: AccountItemProps): ReactElement {
  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}>
        <Typography variant="body1" sx={{ fontWeight: 600, flexGrow: 1, minWidth: 0 }}>
          {title}
        </Typography>
        {action === undefined ? null : (
          <Box sx={{ flexShrink: 0 }}>{action}</Box>
        )}
      </Box>
      {children === undefined ? null : (
        <Box sx={{ mt: 0.5, minWidth: 0 }}>{children}</Box>
      )}
    </Box>
  )
}

/** A phone-removal attempt's row and the confirmation or step-up dialog that
 * carried it, recorded when the attempt starts. */
interface PhoneRemoveAttempt {
  readonly id: string
  readonly row: HTMLElement | null
  readonly dialog: HTMLElement | null
}

/** The dialog the attempt starts in: the confirmation or step-up surface the
 * activated control sits in, read at the moment the attempt starts. */
function dialogHoldingFocus(): HTMLElement | null {
  const active = document.activeElement
  if (!(active instanceof HTMLElement)) {
    return null
  }
  const dialog = active.closest('[role="dialog"]')
  return dialog instanceof HTMLElement ? dialog : null
}

/**
 * Whether a removal took the operator's focus. A write that removes its own
 * row hands focus to a landing, but only when the removal is what took it:
 * focus on the document body, on a detached node, inside the removed row, or
 * inside the confirmation or step-up dialog this attempt recorded all end on
 * the body once that surface closes. A connected control elsewhere -- another
 * dialog included -- is a place the operator chose, and the removal leaves it
 * alone.
 */
function focusFellWithRemoval(
  removedRow: HTMLElement | null,
  dialog: HTMLElement | null,
): boolean {
  const active = document.activeElement
  if (active === null || active === document.body) {
    return true
  }
  if (!(active instanceof HTMLElement) || !active.isConnected) {
    return true
  }
  if (removedRow !== null && removedRow.contains(active)) {
    return true
  }
  return dialog !== null && dialog.contains(active)
}

/**
 * Hands the removal's focus loss to `section`. A confirmation or step-up
 * dialog that carried the write can still be closing when the row leaves; its
 * focus trap pulls a focus placed outside back inside, and the page ends on
 * the body once the dialog unmounts. The landing therefore waits for this
 * attempt's own dialog to leave the document, then hands the focus over only
 * if the removal -- or that dialog -- is what took it.
 */
function landInPhoneSection(
  section: HTMLElement | null,
  attempt: PhoneRemoveAttempt,
): void {
  const land = (): void => {
    if (!focusFellWithRemoval(attempt.row, attempt.dialog)) {
      return
    }
    section?.focus()
  }
  const dialog = attempt.dialog
  if (dialog === null || !dialog.isConnected) {
    land()
    return
  }
  const observer = new MutationObserver(() => {
    if (dialog.isConnected) {
      return
    }
    observer.disconnect()
    land()
  })
  observer.observe(document.body, { childList: true, subtree: true })
  if (!dialog.isConnected) {
    observer.disconnect()
    land()
  }
}

/** The account-management page: one pane per route. */
export function AccountSettingsPage({
  section,
}: {
  readonly section: AccountSection
}): ReactElement {
  const { session } = useAppServices()
  const { t } = useTranslation(DEMO_NAMESPACE)
  const { t: tAccount, i18n: i18nAccount } = useTranslation(ACCOUNT_UI_NAMESPACE)
  // The account-ui bundle owns the identifiers' own vocabulary: the kind
  // names the sign-in summary lists and the badge a proved address carries.
  const { path: pathname, search } = useLocationPath()
  const landing = new URLSearchParams(search)
  // The banner and the post-registration landing reach this page with an
  // intent: `verify=1` asks for the one automatic code, `change=email` opens
  // the change-address form instead. An ordinary visit carries neither, so the
  // section waits for the visitor to ask.
  const changeOpen = landing.get('change') === 'email'
  const verifyLanding = landing.get('verify') === '1' && !changeOpen
  const navigate = useNavigate()
  // A password registration made from an invitation link lands here to prove
  // the address, and the landing it started from has to survive that detour.
  // Once the profile reads verified, the visitor goes back to the link to
  // accept the invitation. The remembered landing is cleared first, so a later
  // verification does not repeat the move.
  const profile = useAuthnProfileGet()
  // The social card is a statement about the assembly, so the card itself
  // appears only when the deployment serves a social sign-in method -- or
  // when the account holds bound identities, which stay visible even if the
  // discovery read fails.
  const socialMethods = useSocialMethodsAvailable(session)
  const showSocialCard =
    (profile.data?.links?.length ?? 0) > 0 ||
    (!socialMethods.pending && socialMethods.available)
  const returnTo = verifyLanding ? pendingInvitationLanding() : null
  useEffect(() => {
    if (returnTo === null || profile.data?.verification?.state !== 'verified') {
      return
    }
    forgetInvitationLanding()
    navigate(returnTo)
  }, [navigate, profile.data, returnTo])
  // Only the host reads the feature document; each section is mounted (or
  // told what it may offer) from the names this deployment serves.
  const phoneServed = useFeature('authn.phone')
  const mfaServed = useFeature('authn.mfa')
  const emailIdentifierServed = useFeature('authn.identifiers.email')
  // The theme preset the provider is applying, and its setter. The choices
  // are the shipped shelf with this bundle's names; a pick applies at once
  // (the provider re-renders the theme) and is written to the device so the
  // next boot starts on it (src/chrome.tsx's initialThemePreset).
  const { preset, setPreset } = useAppThemePreset()
  const presetChoices = themePresets.map((option) => ({
    id: option.id,
    label: t(`themePreset.${option.id}`),
  }))
  const choosePreset = (id: string): void => {
    setPreset(id)
    persistThemePreset(id)
  }
  // The color-mode preference the provider is applying, and its setter. The
  // account section writes the server first; its `onApplied` then lands the
  // confirmed preference here and on the device, so the next boot reads it
  // back (src/chrome.tsx's initialColorModePreference).
  const { preference, setPreference } = useAppThemeMode()
  const applyColorMode = (value: AppThemeModePreference): void => {
    setPreference(value)
    persistColorModePreference(value)
  }
  // The security pane reports each row's state from the same answers the
  // sections read: the profile for the password.
  const [openSecurityRow, setOpenSecurityRow] = useState<string | null>(null)
  const [addEmailSignal, setAddEmailSignal] = useState(0)
  const [phoneFormOpen, setPhoneFormOpen] = useState(false)
  // The phone block's one result slot: an add confirmation and a removal
  // refusal both settle here, and the block renders whichever settled last
  // through a WriteFeedbackRegion. The page-level provider sees this scope,
  // so a later success elsewhere clears it.
  const phoneFeedback = useWriteFeedback()
  // A failure code resolves through the account-ui error vocabulary, the
  // same mapping the section family uses: the code's own leaf when the
  // bundle lists it, the bundle's unknown line otherwise -- never a raw key.
  // The resolver is stable across renders so the region is not re-settled
  // by an identity change alone.
  const resolvePhoneFailure = useCallback(
    (code: string): string => {
      const key = `errors.${code}`
      return i18nAccount.exists(key, { ns: ACCOUNT_UI_NAMESPACE })
        ? String(tAccount(key))
        : String(tAccount('errors.unknown'))
    },
    [i18nAccount, tAccount],
  )
  const [phoneRemoveOpen, setPhoneRemoveOpen] = useState(false)
  // The result belongs to the profile pane's phone block. The pane's route
  // change reuses this page instance, so leaving the block (or the pane)
  // settles the slot instead of carrying its result to the next visit.
  useEffect(() => {
    if (section !== 'profile') {
      phoneFeedback.reset()
    }
  }, [section, phoneFeedback.reset])
  // The sessions block's title-row slot: the list's bulk action is rendered
  // here instead of on a row of its own under the block heading.
  const [sessionsHeaderAction, setSessionsHeaderAction] =
    useState<HTMLElement | null>(null)
  const queryClient = useQueryClient()
  const removeIdentifier = useAuthnLoginRemoveIdentifier()
  // The phone block that holds the number's row, the control whose write can
  // remove that row, and the confirmation dialog that carries it: a
  // successful removal unmounts the control, so focus lands in the block. The
  // row and the dialog captured when the attempt starts are what tell a focus
  // the removal took from one the operator moved away.
  const phoneSection = useRef<HTMLElement | null>(null)
  const phoneRemoveButton = useRef<HTMLButtonElement | null>(null)
  const phoneConfirmDialog = useRef<HTMLDivElement | null>(null)
  const phoneRemoveFocus = useRef<PhoneRemoveAttempt | null>(null)
  const toggleSecurityRow = (key: string): void => {
    setOpenSecurityRow((previous) => (previous === key ? null : key))
  }
  const passwordSet = profile.data?.password_set === true
  const passwordSummary =
    profile.data === undefined
      ? '—'
      : passwordSet
        ? t('account.rows.value.passwordSet')
        : t('account.rows.value.passwordUnset')
  const passwordSummaryAction = passwordSet
    ? t('account.rows.action.change')
    : t('account.rows.action.set')
  const identifiers = profile.data?.identifiers ?? []
  const usernames = identifiers.filter(
    (identifier) => identifier.kind === 'username',
  )
  // The pane's breadcrumb starts at the account's display name -- the same
  // name the shell's account entry shows -- with the username as the
  // fallback for an account that never set one. These are the user's own
  // pages, so the first step says whose; it is not a link, because there is
  // no account home page above the panes.
  const snapshot = useAuthState()
  const displayName =
    snapshot.state === 'authenticated'
      ? (snapshot.principal?.display_name.trim() ?? '')
      : ''
  const accountName =
    displayName === '' ? usernames[0]?.value : displayName
  const breadcrumbs =
    accountName === undefined
      ? undefined
      : [
          { label: accountName },
          { label: t(`account.sections.${section}`) },
        ]
  const separator = t('account.rows.separator')
  const boundPhone = identifiers.find(
    (identifier) => identifier.kind === 'phone',
  )
  // Deleting the number is a protected write: a stale session is answered
  // with the step-up, and the won challenge replays this one delete.
  const phoneGate = useStepUpGate<{ readonly id: string }>(({ id }) => {
    // The won challenge's replay starts inside the step-up dialog, before it
    // closes; that dialog is this attempt's own surface, not a place the
    // operator moved to.
    void removePhone(id, dialogHoldingFocus())
  })
  async function removePhone(
    id: string,
    dialog: HTMLElement | null,
  ): Promise<void> {
    // A new attempt owns the result position, so the previous attempt's
    // failure or the previous add's confirmation does not stand beside this
    // one while it runs.
    phoneFeedback.reset()
    phoneRemoveFocus.current = {
      id,
      row: phoneRemoveButton.current?.closest('tr') ?? null,
      dialog,
    }
    try {
      await removeIdentifier.mutateAsync({ id })
      setPhoneRemoveOpen(false)
      await queryClient.invalidateQueries({
        queryKey: getAuthnProfileGetQueryKey(),
      })
    } catch (error) {
      // The row survives a refusal, so there is no landing to hand over; the
      // step-up replay starts a new attempt and records its own.
      phoneRemoveFocus.current = null
      if (phoneGate.intercept(error, { id })) {
        return
      }
      setPhoneRemoveOpen(false)
      // The refusal settles in the block's one result region; the scope
      // classifies the error into its code and the region resolves it.
      phoneFeedback.fail(error)
    }
  }
  // The section hands the bound number in E.164 once the verification lands
  // and its profile refresh has settled, so the number reaches this page
  // before the confirmation does. The form's visibility belongs to this page,
  // so the add closes here and the confirmation settles the block's feedback
  // scope instead of the section's own region, which unmounts with the form.
  const handlePhoneAdded = (phone: string): void => {
    phoneFeedback.succeed(
      tAccount('phone.added', {
        phone,
        interpolation: { escapeValue: false },
      }),
    )
    setPhoneFormOpen(false)
  }
  // A successful removal leaves the read without its number, so the row's
  // delete control unmounts: focus goes to the block that held the table, in
  // the commit that removes the row rather than leaving it on the document
  // body. Focus is handed over only when the removal is what took it.
  useLayoutEffect(() => {
    const target = phoneRemoveFocus.current
    if (target === null) {
      return
    }
    const identifiers = profile.data?.identifiers
    if (identifiers === undefined) {
      return
    }
    if (identifiers.some((identifier) => identifier.id === target.id)) {
      return
    }
    phoneRemoveFocus.current = null
    landInPhoneSection(phoneSection.current, target)
  }, [profile.data])
  return (
    <PageLayout
      title={t(`account.sections.${section}`)}
      breadcrumbs={breadcrumbs}
    >
      {/* A step-up or enroll round trip started here lands back here with a
          refusal's problem code in the fragment: this page is the surface
          that reads it (the redirect's `return_to`) and words it. */}
      <CallbackLandingNotice namespace={ACCOUNT_UI_NAMESPACE} />
      {/* Each block is its own card; a plain surface keeps the account-ui
          sections mounted inside them from stacking a second card and a
          second heading under the block's own. */}
      <SectionSurfaceProvider value="plain">
        <Stack spacing={3} sx={{ '& .MuiOutlinedInput-root': { borderRadius: (theme) => theme.spacing(1) } }}>
            {section === 'profile' ? (
              <>
                <AccountSection
                  title={t('account.blocks.identity')}
                  icon={<UserIcon />}
                  description={t('account.blockDescriptions.identity')}
                >
                {/* The identity rows are form fields, so they take the same
                    reading width as the forms in the pane's other cards. */}
                <Stack spacing={2}>
                    <AccountItem title={t('account.rows.label.username')}>
                      <TextField
                        size="small"
                        value={
                          usernames.length === 0
                            ? ''
                            : usernames
                                .map((identifier) => identifier.value)
                                .join(separator)
                        }
                        placeholder={t('account.rows.value.notSet')}
                        slotProps={{ input: { readOnly: true } }}
                        sx={{ maxWidth: FORM_MAX_WIDTH }}
                        fullWidth
                      />
                    </AccountItem>
                    <AccountItem title={t('account.rows.label.displayName')}>
                      <Box sx={{ maxWidth: FORM_MAX_WIDTH }}>
                        {/* The shell's account entry reads the display name
                            from the session snapshot, not from the profile
                            query, so a saved name reaches it only when the
                            host mirrors it here. The section reports the name
                            the server stored; the seam updates the snapshot
                            locally and notifies every principal consumer
                            (the account entry, the header menu, the
                            impersonation banner). */}
                        <ProfileSection
                          inline
                          onDisplayNameSaved={(savedName) =>
                            session.setDisplayName(savedName)
                          }
                        />
                      </Box>
                    </AccountItem>
                </Stack>
                </AccountSection>
                <AccountSection
                  title={t('account.rows.label.email')}
                  icon={<MailIcon />}
                  description={t('account.blockDescriptions.email')}
                  action={
                    // The add face only exists when the deployment serves
                    // the email identifier and the account is not waiting on
                    // a proof; the same two facts the section uses to decide
                    // its add kinds, read here so the button is absent when
                    // pressing it could never open anything.
                    emailIdentifierServed &&
                    profile.data?.verification?.state !== 'pending' ? (
                      <Button
                        variant="outlined"
                        size="small"
                        onClick={() => setAddEmailSignal((count) => count + 1)}
                      >
                        {t('account.action.add')}
                      </Button>
                    ) : undefined
                  }
                >
                  <Stack spacing={2}>
                    <IdentifiersSection
                      session={session}
                      emailAvailable={emailIdentifierServed}
                      offerUsername={false}
                      axis="contact"
                      showAddButton={false}
                      openAddSignal={addEmailSignal}
                      returnPath={pathname}
                    />
                    {profile.data?.verification?.state === 'pending' ||
                    verifyLanding ||
                    changeOpen ? (
                      <ContactVerificationSection
                        key={
                          changeOpen
                            ? 'change-email'
                            : verifyLanding
                              ? 'verify'
                              : 'profile'
                        }
                        session={session}
                        initialChangeOpen={changeOpen}
                        autoSend={verifyLanding}
                      />
                    ) : null}
                  </Stack>
                </AccountSection>
                {phoneServed ? (
                  <AccountSection
                    title={t('account.rows.label.phone')}
                    icon={<PhoneIcon />}
                    description={t('account.blockDescriptions.phone')}
                    sectionRef={phoneSection}
                    sectionTabIndex={-1}
                    action={
                      // Add lives in the block header only while there is no
                      // number to act on; once a number is bound, its remove
                      // action rides the number's own row below, like the
                      // email card's per-row actions.
                      boundPhone?.id == null ? (
                        <Button
                          variant="outlined"
                          size="small"
                          onClick={() => {
                            // A new attempt must not stand beside the previous
                            // result.
                            phoneFeedback.reset()
                            setPhoneFormOpen(true)
                          }}
                        >
                          {t('account.action.add')}
                        </Button>
                      ) : undefined
                    }
                  >
                    {boundPhone === undefined ? (
                      phoneFormOpen ? (
                        <PhoneSection
                          session={session}
                          onCancel={() => setPhoneFormOpen(false)}
                          onAdded={handlePhoneAdded}
                        />
                      ) : phoneFeedback.outcome?.kind === 'success' ? null : (
                        <Typography variant="body2" color="text.secondary">
                          {t('account.rows.value.notBound')}
                        </Typography>
                      )
                    ) : phoneFormOpen ? (
                      <PhoneSection
                        session={session}
                        onCancel={() => setPhoneFormOpen(false)}
                        onAdded={handlePhoneAdded}
                      />
                    ) : (
                      <Table
                        size="small"
                        sx={{
                          '& td': { border: 0, padding: '8px 0' },
                          '& td + td': { paddingLeft: 16 },
                        }}
                      >
                        <TableBody>
                          <TableRow>
                            {/* The same address-column width the contact
                                table in the email card uses, so the two
                                tables' badge columns land on one rail. */}
                            <TableCell sx={{ width: '11rem' }}>
                              {boundPhone.value}
                            </TableCell>
                            <TableCell>
                              <Chip
                                size="small"
                                variant="outlined"
                                color={
                                  boundPhone.proven ? 'success' : 'default'
                                }
                                label={
                                  boundPhone.proven
                                    ? tAccount('identifiers.verified')
                                    : tAccount('identifiers.unverified')
                                }
                              />
                            </TableCell>
                            <TableCell align="right">
                              {boundPhone.id == null ? null : (
                                <ActionButton
                                  ref={phoneRemoveButton}
                                  size="small"
                                  tone="danger"
                                  busy={removeIdentifier.isPending}
                                  onClick={() => setPhoneRemoveOpen(true)}
                                >
                                  {t('account.action.delete')}
                                </ActionButton>
                              )}
                            </TableCell>
                          </TableRow>
                        </TableBody>
                      </Table>
                    )}
                    <WriteFeedbackRegion
                      outcome={phoneFeedback.outcome}
                      resolve={resolvePhoneFailure}
                      sx={{ alignSelf: 'stretch', width: '100%' }}
                    />
                  </AccountSection>
                ) : null}
              </>
            ) : null}
            {section === 'security' ? (
              <>
                  <SecurityRow
                    icon={<KeyIcon />}
                    title={t('account.rows.label.password')}
                    badge={
                      <Chip
                        size="small"
                        variant="outlined"
                        label={passwordSummary}
                      />
                    }
                    description={t('account.security.password')}
                    actionLabel={passwordSummaryAction}
                    expanded={openSecurityRow === 'password'}
                    onToggle={() => toggleSecurityRow('password')}
                    testId="security-row-password"
                  >
                    <PasswordSection session={session} returnPath={pathname} />
                  </SecurityRow>
                  <AccountSection
                    title={t('account.rows.label.loginWays')}
                    icon={<UserIcon />}
                    description={t('account.security.identifiers')}
                  >
                    <IdentifiersSection
                      session={session}
                      emailAvailable={emailIdentifierServed}
                      axis="signIn"
                      returnPath={pathname}
                    />
                  </AccountSection>
                  {mfaServed ? (
                    <AccountSection
                      title={t('account.rows.label.mfa')}
                      icon={<ShieldIcon />}
                      description={t('account.security.mfaDescription')}
                    >
                      <MfaSection session={session} returnPath={pathname} />
                    </AccountSection>
                  ) : null}
                  {showSocialCard ? (
                    <AccountSection
                      title={t('account.rows.label.social')}
                      icon={<LinkIcon />}
                      description={t('account.security.social')}
                    >
                      <SocialBindingsSection
                        session={session}
                        returnTo={ACCOUNT_CALLBACK_PATH}
                        onAuthorizeUrl={navigateToAuthorizationUrl}
                        returnPath={pathname}
                      />
                    </AccountSection>
                  ) : null}
              </>
            ) : null}
            {section === 'sessions' ? (
              <>
                <AccountSection
                  title={t('account.blocks.devices')}
                  icon={<DeviceIcon />}
                  description={t('account.blockDescriptions.devices')}
                  action={
                    // The empty box is the block title row's action slot.
                    // The section portals its bulk action into it, so the
                    // control sits beside the block title instead of on a
                    // row of its own under the heading.
                    <Box
                      ref={setSessionsHeaderAction}
                      data-testid="sessions-block-action"
                    />
                  }
                >
                  <SessionsSection
                    renderHeaderAction={(action) =>
                      sessionsHeaderAction === null ? null : (
                        <Portal container={sessionsHeaderAction}>
                          {action}
                        </Portal>
                      )
                    }
                  />
                </AccountSection>
              </>
            ) : null}
            {section === 'preferences' ? (
              <>
                <AccountSection
                  title={t('account.blocks.appearance')}
                  icon={<PaletteIcon />}
                  description={t('account.blockDescriptions.appearance')}
                >
                <Stack spacing={2}>
                    <AccountItem title={t('account.rows.label.theme')}>
                      <AppearanceSection
                        presets={presetChoices}
                        value={preset}
                        onChange={choosePreset}
                        hideLabel
                      />
                    </AccountItem>
                    <AccountItem title={t('account.rows.label.colorMode')}>
                      <ColorModeSection
                        effective={preference}
                        onApplied={applyColorMode}
                        hideLabel
                      />
                    </AccountItem>
                    <PreferencesSection />
                </Stack>
                </AccountSection>
              </>
            ) : null}
        </Stack>
      </SectionSurfaceProvider>
      <ConfirmDialog
        ref={phoneConfirmDialog}
        open={phoneRemoveOpen}
        title={t('account.phoneRemove.removeTitle')}
        message={t('account.phoneRemove.removeMessage')}
        variant="danger"
        confirmLabel={t('account.phoneRemove.removeConfirm')}
        confirmLoading={removeIdentifier.isPending}
        onCancel={() => setPhoneRemoveOpen(false)}
        onConfirm={() => {
          if (boundPhone?.id != null) {
            // The confirmation is this attempt's own surface: a focus still
            // inside it while the row leaves fell with the removal.
            void removePhone(boundPhone.id, phoneConfirmDialog.current)
          }
        }}
      />
      <StepUpDialog
        open={phoneGate.open}
        reason={phoneGate.reason}
        session={session}
        onSuccess={phoneGate.succeed}
        onCancel={phoneGate.cancel}
        returnPath={pathname}
      />
    </PageLayout>
  )
}

/**
 * The social-binding callback route: account-ui's handler reads the
 * `link` flow back and, on completion, returns the viewer to the page the
 * binding started from. With no provider configured no flow reaches here
 * today; the route exists so a product that configures one has the
 * completion point wired. The page carries its own PageHeader (the one h1
 * for the route), because account-ui's handler renders none and the route
 * still needs a page-level heading. A deployment with no redirect method
 * has no binding round trip: the handler renders the app's 404 instead of
 * a flow-not-found banner.
 *
 * The source page rides inside the flow's `return_to` as the callback
 * route's `return` query. account-ui's reader accepts only a site-internal
 * relative path; a missing or unusable value falls back to the security
 * pane, where the binding surface lives -- never the profile pane, which is
 * merely the group address's first landing.
 *
 * A refused round trip lands here with its problem code in the URL
 * fragment, the same shape a refused login lands with; there is no flow
 * left to read, so the page hands the code to the handler and gives it the
 * way back to the starting page.
 */
export function AccountBindingCallbackPage(): ReactElement {
  const { session } = useAppServices()
  const navigate = useNavigate()
  const { t } = useTranslation(DEMO_NAMESPACE)
  const { search } = useLocationPath()
  const { landing, dismiss } = useCallbackLanding()
  const target =
    bindingReturnTarget(search) ?? ACCOUNT_SECTION_PATHS.security
  return (
    <Box sx={{ maxWidth: 480 }}>
      <PageHeader title={t('account.bindingCallback.title')} sx={{ mb: 3 }} />
      <BindingCallbackHandler
        session={session}
        onBound={() => navigate(target)}
        absent={<NotFoundPage path={ACCOUNT_CALLBACK_PATH} />}
        initialError={landing?.code}
        onDismiss={() => {
          dismiss()
          navigate(target)
        }}
      />
    </Box>
  )
}
