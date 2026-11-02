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

import type { MouseEventHandler, ReactElement, ReactNode } from 'react'
import { useEffect, useId, useState } from 'react'
import type { Theme } from '@mui/material/styles'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Card from '@mui/material/Card'
import Chip from '@mui/material/Chip'
import Button from '@mui/material/Button'
import Menu from '@mui/material/Menu'
import MenuItem from '@mui/material/MenuItem'
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
} from '@speed/account-ui'
import { useFeature, useStepUpGate } from '@speed/auth-core'
import { useTranslation } from '@speed/i18n'
import {
  useLocationPath,
  useNavigate,
} from '@speed/route-kit'
import { themePresets } from '@speed/theme-presets'
import type { AppThemeModePreference } from '@speed/ui-kit'
import {
  ConfirmDialog,
  errorCodeOf,
  FORM_MAX_WIDTH,
  PageHeader,
  PageLayout,
  SectionSurfaceProvider,
  useAppThemeMode,
  useAppThemePreset,
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
  children,
}: AccountSectionProps): ReactElement {
  const titleId = useId()
  return (
    <Card
      component="section"
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

/** The account-management page: one pane per route. */
export function AccountSettingsPage({
  section,
}: {
  readonly section: AccountSection
}): ReactElement {
  const { session } = useAppServices()
  const { t } = useTranslation(DEMO_NAMESPACE)
  const { t: tAccount } = useTranslation(ACCOUNT_UI_NAMESPACE)
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
  const [phoneMenuAnchor, setPhoneMenuAnchor] = useState<HTMLElement | null>(
    null,
  )
  const [phoneRemoveOpen, setPhoneRemoveOpen] = useState(false)
  const [phoneRemoveError, setPhoneRemoveError] = useState<string | null>(null)
  // The failure belongs to the profile pane's phone block. The pane's route
  // change reuses this page instance, so leaving the block (or the pane)
  // settles the banner instead of carrying it to the next visit.
  useEffect(() => {
    if (section !== 'profile') {
      setPhoneRemoveError(null)
    }
  }, [section])
  // The sessions block's title-row slot: the list's bulk action is rendered
  // here instead of on a row of its own under the block heading.
  const [sessionsHeaderAction, setSessionsHeaderAction] =
    useState<HTMLElement | null>(null)
  const queryClient = useQueryClient()
  const removeIdentifier = useAuthnLoginRemoveIdentifier()
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
  const separator = t('account.rows.separator')
  const boundPhone = identifiers.find(
    (identifier) => identifier.kind === 'phone',
  )
  // Deleting the number is a protected write: a stale session is answered
  // with the step-up, and the won challenge replays this one delete.
  const phoneGate = useStepUpGate<{ readonly id: string }>(({ id }) => {
    void removePhone(id)
  })
  async function removePhone(id: string): Promise<void> {
    // A new attempt owns the result position, so the previous attempt's
    // failure does not stand beside this one while it runs.
    setPhoneRemoveError(null)
    try {
      await removeIdentifier.mutateAsync({ id })
      setPhoneRemoveOpen(false)
      setPhoneRemoveError(null)
      await queryClient.invalidateQueries({
        queryKey: getAuthnProfileGetQueryKey(),
      })
    } catch (error) {
      if (phoneGate.intercept(error, { id })) {
        return
      }
      setPhoneRemoveOpen(false)
      setPhoneRemoveError(errorCodeOf(error) ?? 'client.unknown')
    }
  }
  return (
    <PageLayout title={t(`account.sections.${section}`)}>
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
                        <ProfileSection inline />
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
                    action={
                      // Add lives in the block header only while there is no
                      // number to act on; once a number is bound, its remove
                      // action rides the number's own row below, like the
                      // email card's per-row actions.
                      boundPhone?.id == null ? (
                        <Button
                          variant="outlined"
                          size="small"
                          onClick={() => setPhoneFormOpen(true)}
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
                        />
                      ) : (
                        <Typography variant="body2" color="text.secondary">
                          {t('account.rows.value.notBound')}
                        </Typography>
                      )
                    ) : phoneFormOpen ? (
                      <PhoneSection
                        session={session}
                        onCancel={() => setPhoneFormOpen(false)}
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
                                <>
                                  <Button
                                    variant="outlined"
                                    size="small"
                                    disabled={removeIdentifier.isPending}
                                    aria-label={tAccount(
                                      'identifiers.actionsAria',
                                      {
                                        value: boundPhone.value,
                                        interpolation: { escapeValue: false },
                                      },
                                    )}
                                    onClick={(event) =>
                                      setPhoneMenuAnchor(event.currentTarget)
                                    }
                                  >
                                    <Box
                                      component="svg"
                                      aria-hidden="true"
                                      focusable="false"
                                      viewBox="0 0 24 24"
                                      width={18}
                                      height={18}
                                      fill="currentColor"
                                    >
                                      <circle cx="5" cy="12" r="1.8" />
                                      <circle cx="12" cy="12" r="1.8" />
                                      <circle cx="19" cy="12" r="1.8" />
                                    </Box>
                                  </Button>
                                  <Menu
                                    anchorEl={phoneMenuAnchor}
                                    open={phoneMenuAnchor !== null}
                                    onClose={() => setPhoneMenuAnchor(null)}
                                  >
                                    <MenuItem
                                      sx={{ color: 'error.main' }}
                                      onClick={() => {
                                        setPhoneMenuAnchor(null)
                                        setPhoneRemoveOpen(true)
                                      }}
                                    >
                                      {t('account.action.delete')}
                                    </MenuItem>
                                  </Menu>
                                </>
                              )}
                            </TableCell>
                          </TableRow>
                        </TableBody>
                      </Table>
                    )}
                    {phoneRemoveError === null ? null : (
                      <Alert
                        severity="error"
                        sx={{ mt: 1 }}
                        onClose={() => setPhoneRemoveError(null)}
                        closeText={t('account.phoneRemove.dismiss')}
                      >
                        {t('account.phoneRemove.removeFailed')}
                      </Alert>
                    )}
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
        open={phoneRemoveOpen}
        title={t('account.phoneRemove.removeTitle')}
        message={t('account.phoneRemove.removeMessage')}
        variant="danger"
        confirmLabel={t('account.phoneRemove.removeConfirm')}
        confirmLoading={removeIdentifier.isPending}
        onCancel={() => setPhoneRemoveOpen(false)}
        onConfirm={() => {
          if (boundPhone?.id != null) {
            void removePhone(boundPhone.id)
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
