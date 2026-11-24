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



function titleRailInset(theme: Theme): string {
  return `calc(2rem + ${theme.spacing(2)})`
}

interface SecurityRowProps {
  readonly icon: ReactNode
  readonly title: string
  readonly badge?: ReactNode
  readonly description: string
  readonly actionLabel: string
  readonly actionHref?: string
  readonly actionOnClick?: MouseEventHandler<HTMLElement>
  readonly expanded?: boolean
  readonly onToggle?: () => void
  readonly children?: ReactNode
  readonly alignBody?: 'title' | 'icon'
  readonly testId: string
}

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
  readonly title: string
  readonly icon?: ReactNode
  readonly description?: string
  readonly action?: ReactNode
  readonly sectionRef?: Ref<HTMLElement>
  readonly sectionTabIndex?: number
  readonly children: ReactNode
}

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
      <Box sx={{ mt: 2, pl: icon === undefined ? 0 : titleRailInset }}>
        {children}
      </Box>
    </Card>
  )
}

interface AccountItemProps {
  readonly title: string
  readonly children?: ReactNode
  readonly action?: ReactNode
}

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

interface PhoneRemoveAttempt {
  readonly id: string
  readonly row: HTMLElement | null
  readonly dialog: HTMLElement | null
}

function dialogHoldingFocus(): HTMLElement | null {
  const active = document.activeElement
  if (!(active instanceof HTMLElement)) {
    return null
  }
  const dialog = active.closest('[role="dialog"]')
  return dialog instanceof HTMLElement ? dialog : null
}

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

// A confirmation or step-up dialog can still be closing when the row leaves;
// its focus trap pulls a focus placed outside back in, so wait for this
// attempt's dialog to leave the document before handing focus to the section.
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

export function AccountSettingsPage({
  section,
}: {
  readonly section: AccountSection
}): ReactElement {
  const { session } = useAppServices()
  const { t } = useTranslation(DEMO_NAMESPACE)
  const { t: tAccount, i18n: i18nAccount } = useTranslation(ACCOUNT_UI_NAMESPACE)
  const { path: pathname, search } = useLocationPath()
  const landing = new URLSearchParams(search)
  const changeOpen = landing.get('change') === 'email'
  const verifyLanding = landing.get('verify') === '1' && !changeOpen
  const navigate = useNavigate()
  const profile = useAuthnProfileGet()
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
  const phoneServed = useFeature('authn.phone')
  const mfaServed = useFeature('authn.mfa')
  const emailIdentifierServed = useFeature('authn.identifiers.email')
  const { preset, setPreset } = useAppThemePreset()
  const presetChoices = themePresets.map((option) => ({
    id: option.id,
    label: t(`themePreset.${option.id}`),
  }))
  const choosePreset = (id: string): void => {
    setPreset(id)
    persistThemePreset(id)
  }
  const { preference, setPreference } = useAppThemeMode()
  const applyColorMode = (value: AppThemeModePreference): void => {
    setPreference(value)
    persistColorModePreference(value)
  }
  const [openSecurityRow, setOpenSecurityRow] = useState<string | null>(null)
  const [addEmailSignal, setAddEmailSignal] = useState(0)
  const [phoneFormOpen, setPhoneFormOpen] = useState(false)
  const phoneFeedback = useWriteFeedback()
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
  useEffect(() => {
    if (section !== 'profile') {
      phoneFeedback.reset()
    }
  }, [section, phoneFeedback.reset])
  const [sessionsHeaderAction, setSessionsHeaderAction] =
    useState<HTMLElement | null>(null)
  const queryClient = useQueryClient()
  const removeIdentifier = useAuthnLoginRemoveIdentifier()
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
  const phoneGate = useStepUpGate<{ readonly id: string }>(({ id }) => {
    void removePhone(id, dialogHoldingFocus())
  })
  async function removePhone(
    id: string,
    dialog: HTMLElement | null,
  ): Promise<void> {
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
      phoneRemoveFocus.current = null
      if (phoneGate.intercept(error, { id })) {
        return
      }
      setPhoneRemoveOpen(false)
      phoneFeedback.fail(error)
    }
  }
  const handlePhoneAdded = (phone: string): void => {
    phoneFeedback.succeed(
      tAccount('phone.added', {
        phone,
        interpolation: { escapeValue: false },
      }),
    )
    setPhoneFormOpen(false)
  }
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
      <CallbackLandingNotice namespace={ACCOUNT_UI_NAMESPACE} />
      <SectionSurfaceProvider value="plain">
        <Stack spacing={3} sx={{ '& .MuiOutlinedInput-root': { borderRadius: (theme) => theme.spacing(1) } }}>
            {section === 'profile' ? (
              <>
                <AccountSection
                  title={t('account.blocks.identity')}
                  icon={<UserIcon />}
                  description={t('account.blockDescriptions.identity')}
                >
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
                      boundPhone?.id == null ? (
                        <Button
                          variant="outlined"
                          size="small"
                          onClick={() => {
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
