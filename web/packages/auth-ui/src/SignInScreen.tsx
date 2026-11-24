/** The assembled sign-in surface: renders the deployment's discovery
 * document, grouped by kind, and switches between sign-in and register. */

import { Fragment, useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import Tab from '@mui/material/Tab'
import Tabs from '@mui/material/Tabs'
import Typography from '@mui/material/Typography'
import type {
  AuthSession,
  AuthnMethodDescriptor,
  AuthnMethodList,
} from '@speed/auth-core'
import { ActionButton } from '@speed/ui-kit'
import { PasswordSignInForm } from './PasswordSignInForm.js'
import { PasswordRegisterForm } from './PasswordRegisterForm.js'
import { SMSSignInForm } from './SMSSignInForm.js'
import { SocialSignInSection } from './SocialSignInSection.js'
import type {
  SocialProvider,
  SocialProviderConfig,
} from './SocialSignInSection.js'
import {
  DescriptorSignInForm,
  EnterpriseSignIn,
  PasskeySignIn,
} from './internal/sign-in-methods.js'
import { useSignInDiscovery } from './internal/use-signin-discovery.js'
import type { SignInDiscovery } from './internal/use-signin-discovery.js'
import { methodLabel } from './internal/labels.js'
import { firstControl } from './internal/step-focus.js'
import { InlineError } from './internal/inline-error.js'
import { RetryAction } from './internal/retry-action.js'
import { useAuthUiTranslation } from './internal/translation.js'
import { GroupHeadingProvider } from './internal/group-heading.js'
import type { HeadingLevel } from './internal/heading.js'
import type {
  SignInBeforeStart,
  SignInMode,
  SignInStartContext,
} from './internal/start-gate.js'

/** The form-channel names the deprecated override recognises. */
export type SignInChannel = 'password' | 'sms'

/** The synthetic form channel register mode adds when password registration
 * is enabled. It is not a discovery method. */
const PASSWORD_REGISTER_CHANNEL = 'password-register'

export type { SignInBeforeStart, SignInMode, SignInStartContext }

/** A channel class the screen assembles. */
export type SignInGroup = 'form' | 'redirect' | 'enterprise' | 'webauthn'

const DEFAULT_GROUP_ORDER: readonly SignInGroup[] = [
  'form',
  'redirect',
  'enterprise',
  'webauthn',
]

/** The two places a host commonly adds its own content. */
export interface SignInScreenSlots {
  /** Rendered above the channel classes, below the mode header. */
  readonly aboveChannels?: ReactNode
  /** Rendered below the form channels (and their account sentence). */
  readonly belowForms?: ReactNode
}

/** What a completion on this screen reports to the host. */
export interface SignInContext {
  /** The mode the screen was in when the login settled. */
  readonly mode: SignInMode
  /** The method that drove it: `password`, `sms`, a passkey method's name, or
   * a descriptor-driven form method's name. */
  readonly channel: string
}

/** The social / redirect block of the screen. */
export interface SocialSignInOptions {
  /**
   * The redirect methods to render, in the order the host wants.
   * @deprecated The redirect method set now comes from the discovery
   * document; this override is honored for one major.
   */
  readonly providers?: readonly SocialProviderConfig[]
  /** The host callback route sent as the flow's `return_to`; omitted to let
   * the server use its configured default. */
  readonly returnTo?: string
  /** Receives each redirect method's authorization URL once built; the host
   * navigates. */
  readonly onAuthorizeUrl?: (provider: SocialProvider, authorizeUrl: string) => void
}

/** The host supplies the session and the screen-wide assembly options; the
 * screen owns discovery, the presented mode until `onModeChange` makes it
 * controlled, and every channel's flow. */
export interface SignInScreenProps {
  /** The session every channel on the screen drives. */
  readonly session: AuthSession
  /**
   * A pre-fetched discovery document to render, instead of reading one (an
   * SSR pass, a test). When given the screen fires no `listMethods` read.
   */
  readonly discovery?: AuthnMethodList
  /** The presentational mode; `'sign-in'` by default. Controlled when
   * `onModeChange` is given. */
  readonly mode?: SignInMode
  /** Fired when the register / sign-in switch is activated. Giving it makes
   * `mode` the only source of the presented mode: the switch reports the
   * choice and the host re-renders. */
  readonly onModeChange?: (mode: SignInMode) => void
  /** Fired once after a sign-in commits on a channel this screen drives, with
   * the mode and the channel's method name. */
  readonly onSignedIn?: (context: SignInContext) => void
  /**
   * Decides whether a channel's first flow may begin. A false answer starts
   * nothing and renders no error; later steps are not gated. The context
   * carries the method and the presented mode.
   */
  readonly beforeStart?: SignInBeforeStart
  /** The redirect / enterprise block of the screen; omitted to run without
   * one. */
  readonly social?: SocialSignInOptions
  /** Host content in the screen's two open places (see SignInScreenSlots). */
  readonly slots?: SignInScreenSlots
  /** The order of the channel classes. A class left out keeps its default
   * position after the listed ones. The first class rendered heads the
   * surface with the "or"-less divider wording. */
  readonly order?: readonly SignInGroup[]
  /** The heading element the register-mode title renders as; `'h2'` by
   * default (the screen normally sits under the host's page heading). */
  readonly headingLevel?: HeadingLevel
  /** The mark rendered before a redirect method's label. */
  readonly providerIcon?: (provider: SocialProvider) => ReactNode
  /**
   * The form channels to offer, by name.
   * @deprecated The channels now come from the discovery document; this
   * override still narrows the form strip for one major.
   */
  readonly channels?: readonly SignInChannel[]
  /**
   * The form channel selected in sign-in mode on first render; when it is not
   * offered, the first offered one shows.
   * @deprecated Presentational; still honored for one major.
   */
  readonly defaultChannel?: SignInChannel
  /**
   * A problem code the host carried here from outside a channel's own
   * attempt, rendered above the channels through the same inline error.
   */
  readonly initialError?: string
  /**
   * The masked address a link-required refusal named, placed under the initial
   * inline error. Read only when `initialError` is `authn.link_required`.
   */
  readonly initialErrorEmail?: string
  /** Reports that the screen stopped presenting `initialError`. */
  readonly onInitialErrorDismiss?: () => void
  /**
   * Reports the password channel's "forgot password" click; the host
   * navigates. The entry renders only where the discovery document publishes
   * an anonymous password reset, and only in sign-in mode.
   */
  readonly onForgotPassword?: () => void
}

/**
 * Reads the deployment's discovery document (or renders the injected one) and
 * renders its methods grouped by kind, switching between sign-in and register.
 */
export function SignInScreen({
  session,
  discovery,
  mode = 'sign-in',
  onModeChange,
  onSignedIn,
  beforeStart,
  social,
  slots,
  order,
  headingLevel = 'h2',
  providerIcon,
  channels,
  defaultChannel = 'password',
  initialError,
  initialErrorEmail,
  onInitialErrorDismiss,
  onForgotPassword,
}: SignInScreenProps) {
  const { t } = useAuthUiTranslation()
  const [attempt, setAttempt] = useState(0)
  // A passkey entry remounts on this epoch, so a refusal from the previous
  // ceremony never stands beside the work now in progress elsewhere.
  const [interactionEpoch, setInteractionEpoch] = useState(0)
  const noteInteraction = (): void => {
    setInteractionEpoch((value) => value + 1)
  }
  const fetched = useSignInDiscovery(session, {
    enabled: discovery === undefined,
    attempt,
  })
  const state: SignInDiscovery =
    discovery === undefined ? fetched : { status: 'ready', discovery }
  // Uncontrolled until the host takes the switch; then `mode` is the only
  // source.
  const [internalMode, setInternalMode] = useState<SignInMode>(mode)
  useEffect(() => {
    setInternalMode(mode)
  }, [mode])
  const controlledMode = onModeChange !== undefined
  const activeMode = controlledMode ? mode : internalMode
  // The key identifies the message last presented, so a different value from
  // the host is presented again and the dismissed one is dropped once.
  const hostErrorKey =
    initialError === undefined
      ? undefined
      : `${initialError}\u0000${initialErrorEmail ?? ''}`
  const [dismissedHostErrorKey, setDismissedHostErrorKey] = useState<
    string | undefined
  >(undefined)
  const dismissHostError = (): void => {
    if (hostErrorKey === undefined || dismissedHostErrorKey === hostErrorKey) {
      return
    }
    setDismissedHostErrorKey(hostErrorKey)
    onInitialErrorDismiss?.()
  }
  useEffect(() => {
    if (initialError === undefined) {
      setDismissedHostErrorKey(undefined)
    }
  }, [initialError])
  const requestMode = (next: SignInMode): void => {
    dismissHostError()
    if (controlledMode) {
      onModeChange?.(next)
    } else {
      setInternalMode(next)
    }
  }
  // The same gate for every channel, with the presented mode added. The
  // dismissal is contained here so a throwing dismissal callback cannot change
  // the gate's answer: it is a host bug, not a refusal.
  const startGate: SignInBeforeStart = (context) => {
    try {
      dismissHostError()
    } catch {
      // The host's dismissal callback failed; the attempt still starts.
    }
    return beforeStart === undefined
      ? true
      : beforeStart({ ...context, mode: activeMode })
  }
  const channelId = useId()
  const [channel, setChannel] = useState<string | null>(null)
  // A selection belongs to the surface it was made on: the register side's
  // default wins over a channel picked on the sign-in side.
  const [channelMode, setChannelMode] = useState<SignInMode | null>(null)
  const pickChannel = (name: string, forMode: SignInMode = activeMode): void => {
    dismissHostError()
    setChannel(name)
    setChannelMode(forMode)
  }
  const [prefillUsername, setPrefillUsername] = useState<string | undefined>(
    undefined,
  )
  const formSurfaceRef = useRef<HTMLDivElement | null>(null)
  // Set by the refused auto-login that also switches the mode; consumed by
  // the commit that unmounts the registration form.
  const signInHandoff = useRef(false)

  // A refused auto-login removes the registration form with the focused control;
  // hand focus to the sign-in form only when that removal took it. The flag lives
  // one commit, so it cannot fire on a later unrelated mode switch.
  useLayoutEffect(() => {
    if (!signInHandoff.current) {
      return
    }
    signInHandoff.current = false
    if (activeMode !== 'sign-in') {
      return
    }
    const surface = formSurfaceRef.current
    if (surface === null) {
      return
    }
    const active = document.activeElement
    const focusCameFromTheForm =
      active === null ||
      active === document.body ||
      active === document.documentElement ||
      (active instanceof HTMLElement && !active.isConnected)
    if (focusCameFromTheForm) {
      firstControl(surface)?.focus()
    }
  })

  if (state.status === 'loading') {
    return (
      <Box
        role="status"
        sx={{ display: 'flex', alignItems: 'center', gap: 1.5, width: '100%' }}
      >
        <CircularProgress size={18} aria-hidden={true} />
        <Typography>{t('discovery.loading')}</Typography>
      </Box>
    )
  }

  if (state.status === 'error') {
    return (
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'flex-start',
          gap: 1.5,
          width: '100%',
        }}
      >
        <InlineError code={state.code} />
        <RetryAction
          label={t('discovery.retry')}
          onClick={() => setAttempt((value) => value + 1)}
        />
      </Box>
    )
  }

  const doc = state.discovery
  const formMethods = doc.methods
    .filter((method) => method.kind === 'form')
    .filter(
      (method) =>
        channels === undefined ||
        channels.length === 0 ||
        (channels as readonly string[]).includes(method.name),
    )
  const redirectMethods = doc.methods.filter((method) => method.kind === 'redirect')
  const enterpriseMethods = doc.methods.filter(
    (method) => method.kind === 'enterprise',
  )
  const webauthnMethods = doc.methods.filter((method) => method.kind === 'webauthn')

  const opensAccount = (method: AuthnMethodDescriptor): boolean =>
    method.opens_account === true
  const inMode = (
    methods: readonly AuthnMethodDescriptor[],
  ): readonly AuthnMethodDescriptor[] =>
    activeMode === 'register' ? methods.filter(opensAccount) : methods

  const passwordRegister: AuthnMethodDescriptor | null =
    activeMode === 'register' && doc.signup?.password?.enabled === true
      ? {
          name: PASSWORD_REGISTER_CHANNEL,
          kind: 'form',
          label: t('register.title'),
        }
      : null
  const visibleForms =
    passwordRegister === null
      ? inMode(formMethods)
      : [passwordRegister, ...inMode(formMethods)]
  const visibleRedirects = inMode(redirectMethods)
  const visibleEnterprise = inMode(enterpriseMethods)
  const visibleWebauthn = inMode(webauthnMethods)
  const formNames = visibleForms.map((method) => method.name)
  const selected =
    channel !== null && channelMode === activeMode && formNames.includes(channel)
      ? channel
      : activeMode === 'register'
        ? (formNames[0] ?? '')
        : formNames.includes(defaultChannel)
          ? defaultChannel
          : (formNames[0] ?? '')
  const activeDescriptor = visibleForms.find((method) => method.name === selected)

  const signInRedirects =
    social?.providers === undefined
      ? redirectMethods
      : redirectMethods.filter((method) =>
          social.providers?.some((config) => config.provider === method.name),
        )

  const renderedMethods = [
    ...formMethods,
    ...signInRedirects,
    ...enterpriseMethods,
    ...webauthnMethods,
  ]

  const registerVisible =
    doc.signup?.enabled === true &&
    (renderedMethods.some(opensAccount) ||
      doc.signup.password.enabled === true)

  const smsOpensAccount =
    doc.signup?.enabled === true &&
    visibleForms.some((method) => method.name === 'sms' && opensAccount(method))

  // A deprecated provider override names channels without descriptors: in
  // register mode it keeps only those whose descriptor opens an account.
  const redirectConfigs: readonly SocialProviderConfig[] =
    social?.providers === undefined
      ? visibleRedirects.map((method) => ({
          provider: method.name,
          label: method.label,
          redirectUri: social?.returnTo ?? '',
        }))
      : activeMode === 'register'
        ? social.providers.filter((config) =>
            visibleRedirects.some(
              (method) => method.name === config.provider,
            ),
          )
        : social.providers

  const tabIdOf = (name: string): string => `${channelId}-${name}-tab`
  const panelIdOf = (name: string): string => `${channelId}-${name}-panel`

  const completed = (channelName: string): void => {
    onSignedIn?.({ mode: activeMode, channel: channelName })
  }

  const forgotPasswordEntry =
    activeMode === 'sign-in' &&
    selected === 'password' &&
    doc.password_reset?.enabled === true &&
    onForgotPassword !== undefined ? (
      <ActionButton type="button" onClick={onForgotPassword}>
        {t('passwordSignIn.forgotPassword')}
      </ActionButton>
    ) : null

  const renderForm = (method: AuthnMethodDescriptor): ReactNode => {
    switch (method.name) {
      case PASSWORD_REGISTER_CHANNEL:
        return (
          <PasswordRegisterForm
            session={session}
            beforeStart={startGate}
            humanGate={doc.human}
            onSignedIn={onSignedIn}
            onSignInRequired={(username) => {
              signInHandoff.current = true
              setPrefillUsername(username)
              pickChannel('password', 'sign-in')
              requestMode('sign-in')
            }}
          />
        )
      case 'password':
        return (
          <PasswordSignInForm
            session={session}
            beforeStart={startGate}
            defaultUsername={prefillUsername}
            humanGate={doc.human}
            actionsLeading={forgotPasswordEntry}
            onSignedIn={() => completed('password')}
          />
        )
      case 'sms':
        return (
          <SMSSignInForm
            session={session}
            beforeStart={startGate}
            onSignedIn={() => completed('sms')}
            humanGate={doc.human}
          />
        )
      default:
        return (
          <DescriptorSignInForm
            session={session}
            method={method.name}
            fields={method.fields ?? []}
            beforeStart={startGate}
            humanGate={doc.human}
            onSignedIn={() => completed(method.name)}
          />
        )
    }
  }

  const formBlock = (
    <>
      {visibleForms.length > 1 ? (
        <Tabs
          value={selected}
          onChange={(_event, value: string) => pickChannel(value)}
          variant="fullWidth"
        >
          {visibleForms.map((method) => (
            <Tab
              key={method.name}
              label={methodLabel(method, t)}
              value={method.name}
              id={tabIdOf(method.name)}
              aria-controls={panelIdOf(method.name)}
            />
          ))}
        </Tabs>
      ) : null}
      {visibleForms.length > 1 ? (
        <Box
          key={selected}
          role="tabpanel"
          id={panelIdOf(selected)}
          aria-labelledby={tabIdOf(selected)}
        >
          {activeDescriptor ? (
            <Box ref={formSurfaceRef} sx={{ display: 'contents' }}>
              {renderForm(activeDescriptor)}
            </Box>
          ) : null}
        </Box>
      ) : activeDescriptor ? (
        <Box ref={formSurfaceRef} sx={{ display: 'contents' }}>
          {renderForm(activeDescriptor)}
        </Box>
      ) : null}
      {activeMode === 'sign-in' && smsOpensAccount && selected === 'sms' ? (
        <Typography variant="body2" color="text.secondary">
          {t('mode.signInAccountHint')}
        </Typography>
      ) : null}
      {slots?.belowForms}
    </>
  )

  const requestedOrder = order ?? DEFAULT_GROUP_ORDER
  const groupOrder: readonly SignInGroup[] = [
    ...Array.from(new Set(requestedOrder)),
    ...DEFAULT_GROUP_ORDER.filter((group) => !requestedOrder.includes(group)),
  ]
  const hasGroup: Record<SignInGroup, boolean> = {
    form: visibleForms.length > 0,
    redirect: redirectConfigs.length > 0,
    enterprise: visibleEnterprise.length > 0,
    webauthn: visibleWebauthn.length > 0,
  }
  const firstGroup = groupOrder.find((group) => hasGroup[group]) ?? null

  const shownInitialError =
    initialError !== undefined && dismissedHostErrorKey !== hostErrorKey
      ? initialError
      : undefined

  const renderGroup = (group: SignInGroup): ReactNode => {
    const headingVariant = group === firstGroup ? 'plain' : 'or'
    switch (group) {
      case 'form':
        return (
          <Fragment key="form">
            <Box
              sx={{ display: 'contents' }}
              onFocusCapture={noteInteraction}
              onPointerDownCapture={noteInteraction}
            >
              {formBlock}
            </Box>
          </Fragment>
        )
      case 'redirect':
        return (
          <GroupHeadingProvider key="redirect" value={headingVariant}>
            <Box
              sx={{ display: 'contents' }}
              onFocusCapture={noteInteraction}
              onPointerDownCapture={noteInteraction}
            >
              <SocialSignInSection
                session={session}
                providers={redirectConfigs}
                onAuthorizeUrl={social?.onAuthorizeUrl}
                providerIcon={providerIcon}
                beforeStart={startGate}
              />
            </Box>
          </GroupHeadingProvider>
        )
      case 'enterprise':
        return (
          <GroupHeadingProvider key="enterprise" value={headingVariant}>
            <Box
              sx={{ display: 'contents' }}
              onFocusCapture={noteInteraction}
              onPointerDownCapture={noteInteraction}
            >
              <EnterpriseSignIn
                session={session}
                returnTo={social?.returnTo}
                onAuthorizeUrl={social?.onAuthorizeUrl}
                beforeStart={startGate}
              />
            </Box>
          </GroupHeadingProvider>
        )
      case 'webauthn':
        return (
          <GroupHeadingProvider key="webauthn" value={headingVariant}>
            {visibleWebauthn.map((method) => (
              <PasskeySignIn
                // Remount on a mode/channel switch or a new attempt, so no
                // earlier ceremony's refusal stands beside the current channel.
                key={`${method.name}:${activeMode}:${selected}:${interactionEpoch}`}
                session={session}
                method={method}
                beforeStart={startGate}
                humanGate={doc.human}
                onSignedIn={() => completed(method.name)}
              />
            ))}
          </GroupHeadingProvider>
        )
    }
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, width: '100%' }}>
      {activeMode === 'register' ? (
        <>
          <Typography variant="h5" component={headingLevel}>
            {t('mode.registerTitle')}
          </Typography>
          <Typography variant="body2" color="text.secondary">
            {t('mode.registerHint')}
          </Typography>
          <Button variant="text" onClick={() => requestMode('sign-in')}>
            {t('mode.signInEntry')}
          </Button>
        </>
      ) : registerVisible ? (
        <Button variant="text" onClick={() => requestMode('register')}>
          {t('mode.registerEntry')}
        </Button>
      ) : null}
      {shownInitialError === undefined ? null : (
        <InlineError
          code={shownInitialError}
          detail={
            shownInitialError === 'authn.link_required' &&
            initialErrorEmail !== undefined
              ? t('social.linkRequiredEmail', { email: initialErrorEmail })
              : undefined
          }
        />
      )}
      {slots?.aboveChannels}
      {groupOrder.filter((group) => hasGroup[group]).map(renderGroup)}
    </Box>
  )
}
