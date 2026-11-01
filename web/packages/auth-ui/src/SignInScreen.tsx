/**
 * SignInScreen: the assembled sign-in surface over the deployment's
 * login methods.
 *
 * The channels are the deployment's own: the screen reads the discovery
 * document (`session.listMethods`, or a document the host injects) and
 * renders exactly what it names -- the ways in are the server's, so a
 * method enabled or disabled backend-side reaches the surface without a
 * frontend change. The document's descriptors are grouped by `kind`:
 *
 *  - `form` methods gather behind a tab strip (a single one renders
 *    without a strip -- a tablist with nothing to switch between is not a
 *    choice). Password and SMS keep their bespoke forms; any other form
 *    method is driven by its descriptor's own fields. Switching tabs
 *    unmounts the previous form, so its half-typed state and whole-attempt
 *    error are gone with it -- a deliberate reset: channel errors must not
 *    leak across surfaces. Where the deployment publishes an anonymous
 *    password reset and the host binds `onForgotPassword`, the password
 *    channel carries a "forgot password" text button at the leading edge
 *    of its submit row; it reports the click and navigates nothing.
 *  - `redirect` methods render as buttons (the social block); picking one
 *    begins the method's flow and hands the host the authorization URL to
 *    navigate.
 *  - `enterprise` methods render an email-first entry that discovers the
 *    viewer's identity providers before it leaves for one.
 *  - `webauthn` methods render a passkey button that starts the method's
 *    flow and renders the ceremony step.
 *
 * An unknown `kind` is ignored rather than rendered; a method whose name
 * the family does not know still renders through its descriptor (see
 * ./internal/labels), so the screen never crashes on a server-side method
 * it has never seen.
 *
 * `mode` is a pure presentational switch over the sign-in surface: the
 * component presents a "register" entry where the deployment allows a
 * first login to open an account (`discovery.signup.enabled` and either
 * `discovery.signup.password.enabled` or at least one rendered method
 * carrying `opens_account`). In register mode every channel on the screen
 * -- the form strip, the social buttons, the enterprise entry and the
 * passkey entry -- narrows to the methods that open an account, and the
 * form strip starts on its own default; a channel picked on the other
 * side of the switch does not carry over. The screen heads its register
 * block "Choose a sign-in method" and offers the way back as "Already
 * have an account? Sign in". When `discovery.signup.password.enabled` is
 * true, register mode
 * adds a synthetic "password registration" channel ahead of the narrowed
 * ones and selects it by default: it is not a discovery method, renders
 * `PasswordRegisterForm`, and reports its completion as
 * `channel: 'password'` -- the password descriptor still carries no
 * `opens_account`. In sign-in mode, where that entry is offered and the
 * screen has an SMS channel that opens an account, one sentence below the
 * channels says an unregistered phone number creates an account, the same
 * sentence for every number; the sentence names a phone number, so it is
 * absent where no such channel is on screen. Registration through a
 * login flow is one channel class; the password registration form is the
 * other.
 * A successful sign-in on a channel the screen drives itself fires
 * onSignedIn once, with the mode the screen was in and that channel's
 * method name (see SignInContext): a host can tell a registration from an
 * ordinary sign-in. Redirect and enterprise channels complete through the
 * callback route rather than this screen, so they carry no context.
 * Nothing here navigates: the page above (branding, the heading) is host
 * content, except that register mode renders its own title -- the page
 * heading still names the sign-in surface the host mounted.
 *
 * `discovery` lets a host inject a pre-fetched document (SSR, a test), in
 * which case the screen fires no read of its own. The deprecated
 * `channels` / `defaultChannel` props still narrow and select the form
 * channels for one major.
 *
 * The screen is assembleable: `order` puts the four channel classes in the
 * host's order (a class left out keeps its default position after the
 * listed ones), and the first class actually rendered heads the surface
 * with the "or"-less divider wording. `slots` opens the two places a host
 * commonly adds content (above the channels, below the forms), and
 * `providerIcon` supplies each redirect method's mark. `mode` is
 * uncontrolled until `onModeChange` is given: then the prop is the only
 * source of the presented mode and the register / sign-in switch merely
 * reports its choice to the host, which re-renders with the new mode.
 * `beforeStart` decides per attempt whether a channel's first flow may
 * begin (a false answer starts nothing and renders no error).
 */

import { Fragment, useEffect, useId, useState } from 'react'
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
import { InlineError } from './internal/inline-error.js'
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

/** The synthetic form channel register mode adds when password
 * registration is enabled. It is not a discovery method: the password
 * descriptor keeps its sign-in meaning (and no `opens_account`). */
const PASSWORD_REGISTER_CHANNEL = 'password-register'

export type { SignInBeforeStart, SignInMode, SignInStartContext }

/** A channel class the screen assembles, in the order it renders them. */
export type SignInGroup = 'form' | 'redirect' | 'enterprise' | 'webauthn'

/** The order the screen renders the channel classes in when `order` is
 * omitted. */
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

/**
 * What a completion on this screen reports to the host: the mode the
 * screen was in when the login settled, and the name of the method that
 * drove it. The channel is the method's own name -- `password`, `sms`, a
 * passkey method's name, or a descriptor-driven form method's name.
 */
export interface SignInContext {
  readonly mode: SignInMode
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

export interface SignInScreenProps {
  /** The session every channel on the screen drives. */
  readonly session: AuthSession
  /**
   * A pre-fetched discovery document to render, instead of reading one (an
   * SSR pass, a test). When given the screen fires no `listMethods` read.
   */
  readonly discovery?: AuthnMethodList
  /** The presentational mode; `'sign-in'` by default. Controlled when
   * `onModeChange` is given (see there). */
  readonly mode?: SignInMode
  /** Fired when the register / sign-in switch is activated. Giving it makes
   * `mode` the only source of the presented mode: the switch reports the
   * choice and the host re-renders with it, instead of the screen keeping
   * its own state. */
  readonly onModeChange?: (mode: SignInMode) => void
  /** Fired once after a sign-in commits on a channel this screen drives,
   * with the mode and the channel's method name; the host decides what
   * follows. A host that ignores the argument is unaffected. */
  readonly onSignedIn?: (context: SignInContext) => void
  /**
   * Decides whether a channel's first flow may begin. A false answer
   * starts nothing and renders no error; the flow's later steps are not
   * gated. The context carries the method and the presented mode.
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
   * The form channel selected in sign-in mode on first render; password by
   * default. When the named channel is not offered, the first offered one
   * is shown. Register mode always starts on the first account-opening
   * channel.
   * @deprecated The channel selection is presentational; still honored for
   * one major.
   */
  readonly defaultChannel?: SignInChannel
  /**
   * A problem code the host carried here from outside a channel's own
   * attempt, rendered above the channels with the same inline error the
   * channels use. A failed social callback lands back on this screen with
   * its code in the URL fragment, and the host hands it over here; an
   * unknown code renders the generic message.
   */
  readonly initialError?: string
  /**
   * The masked address a link-required refusal named, rendered as a plain
   * line under the initial inline error so the viewer can tell which of
   * their addresses collided. The server has already masked it; the screen
   * only places it. It is read only when `initialError` is
   * `authn.link_required`; every other code renders no line even if the
   * host supplies a value, because the server never names an address for
   * them.
   */
  readonly initialErrorEmail?: string
  /**
   * Reports that the screen stopped presenting `initialError`: choosing
   * another mode or channel, or starting any new attempt, clears the host's
   * message. The host drops the landing it supplied, so a remount does not
   * present the same refusal again. A host that binds nothing keeps only
   * the screen's local hiding.
   */
  readonly onInitialErrorDismiss?: () => void
  /**
   * Reports the password channel's "forgot password" click; the host
   * navigates to its reset surface. The entry renders only where the
   * discovery document publishes an anonymous password reset
   * (`password_reset.enabled`) and only in sign-in mode, beneath the
   * password channel's form. A host that binds nothing offers no entry.
   */
  readonly onForgotPassword?: () => void
}

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
  const fetched = useSignInDiscovery(session, {
    enabled: discovery === undefined,
    attempt,
  })
  const state: SignInDiscovery =
    discovery === undefined ? fetched : { status: 'ready', discovery }
  // Uncontrolled by default: the screen switches its own mode. When the
  // host takes the switch (`onModeChange` given), `mode` is the only
  // source and the internal copy is never consulted.
  const [internalMode, setInternalMode] = useState<SignInMode>(mode)
  useEffect(() => {
    setInternalMode(mode)
  }, [mode])
  const controlledMode = onModeChange !== undefined
  const activeMode = controlledMode ? mode : internalMode
  // The host's error stands until the viewer starts something else: choosing
  // another mode or channel, or any attempt. The key identifies the message
  // last presented, so a different value from the host -- a fresh landing --
  // is presented again, and the host is told once to drop the dismissed one.
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
  // A host that drops its landing leaves nothing to dismiss; a later landing
  // supplies a new value, which is presented again.
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
  // The screen's start gate: the same decision for every channel, with
  // the mode the screen currently presents added to the context. Every
  // attempt passes through it, so it also clears the host's landing
  // refusal: that message does not stand beside the answer this attempt
  // is about to produce. The dismissal is not part of the gate's answer:
  // `allowedToStart` contains a throwing gate as a refusal, and a host
  // dismissal callback that throws is a host bug, not a decision to block
  // the attempt. It is contained here so the answer stays `beforeStart`'s.
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
  // The id base of the tab/tabpanel pair per form channel: each channel's
  // tab names its panel through aria-controls, and the panel -- the
  // mounted form, unmounted on switch -- answers through aria-labelledby.
  const channelId = useId()
  const [channel, setChannel] = useState<string | null>(null)
  // The mode the viewer picked `channel` in. A selection belongs to the
  // surface it was made on: the register side's default must win over a
  // channel picked on the sign-in side (and the sign-in side keeps its
  // own default when the viewer comes back).
  const [channelMode, setChannelMode] = useState<SignInMode | null>(null)
  const pickChannel = (name: string, forMode: SignInMode = activeMode): void => {
    dismissHostError()
    setChannel(name)
    setChannelMode(forMode)
  }
  // The username a registration handed over after its auto-login was
  // refused; the sign-in form it returns to starts with it.
  const [prefillUsername, setPrefillUsername] = useState<string | undefined>(
    undefined,
  )

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
        <Button variant="outlined" onClick={() => setAttempt((value) => value + 1)}>
          {t('discovery.retry')}
        </Button>
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

  // Register mode can only mean the methods that open an account; the same
  // criterion narrows every channel class, and a channel the viewer picked
  // in sign-in mode is honored only while it survives the form narrowing.
  const opensAccount = (method: AuthnMethodDescriptor): boolean =>
    method.opens_account === true
  const inMode = (
    methods: readonly AuthnMethodDescriptor[],
  ): readonly AuthnMethodDescriptor[] =>
    activeMode === 'register' ? methods.filter(opensAccount) : methods

  // Register mode's synthetic password-registration channel: not a
  // discovery method, so it sits ahead of the narrowed form methods only
  // while password registration is enabled.
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

  // The redirect methods the screen renders in sign-in mode: the discovery
  // set, or -- under the deprecated provider override -- the descriptors of
  // the providers the host named, in the document's order. A provider the
  // document does not describe carries no `opens_account` fact.
  const signInRedirects =
    social?.providers === undefined
      ? redirectMethods
      : redirectMethods.filter((method) =>
          social.providers?.some((config) => config.provider === method.name),
        )

  // The methods this screen actually renders in sign-in mode: the form
  // channels left after the deprecated narrowing, the redirect methods,
  // and the enterprise / webauthn entries. A method with an unknown kind
  // renders nothing, so it cannot make the register entry visible.
  const renderedMethods = [
    ...formMethods,
    ...signInRedirects,
    ...enterpriseMethods,
    ...webauthnMethods,
  ]

  // The register entry appears where signup is on and either a rendered
  // method opens an account or the deployment offers password
  // registration, which brings its own form.
  const registerVisible =
    doc.signup?.enabled === true &&
    (renderedMethods.some(opensAccount) ||
      doc.signup.password.enabled === true)

  // The sign-in mode's account sentence names a phone number, so it belongs
  // under the SMS channel alone: it renders only where signup is on, an SMS
  // channel that opens an account is on screen, and that channel is the one
  // the viewer is looking at.
  const smsOpensAccount =
    doc.signup?.enabled === true &&
    visibleForms.some((method) => method.name === 'sms' && opensAccount(method))

  // A deprecated provider override names channels without descriptors; in
  // register mode it can only keep the ones whose discovery descriptor
  // opens an account, in the host's own order. The override is honored as
  // given in sign-in mode.
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

  // The context the screen reports when a channel it drives completes. The
  // channel forms keep their own no-argument onSignedIn (a leaf knows only
  // its flow); the screen binds the method name here, and the mode is the
  // one currently presented -- the register entry runs the same flow.
  const completed = (channelName: string): void => {
    onSignedIn?.({ mode: activeMode, channel: channelName })
  }

  // The password channel's reset entry: it rides the channel's submit row,
  // at its leading edge, so the link and the primary action read as one
  // row rather than the link floating on a line of its own.
  const forgotPasswordEntry =
    activeMode === 'sign-in' &&
    selected === 'password' &&
    doc.password_reset?.enabled === true &&
    onForgotPassword !== undefined ? (
      <Button
        variant="text"
        onClick={onForgotPassword}
        sx={{ alignSelf: 'flex-start' }}
      >
        {t('passwordSignIn.forgotPassword')}
      </Button>
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
              // The account exists; the viewer is handed to the sign-in
              // form with the username the registration used. The
              // selection belongs to the sign-in side this handoff picks.
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

  // The form class: the strip (when there is a choice), the selected
  // form, the sign-in-mode account sentence, and the host's below-forms
  // slot.
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
          {activeDescriptor ? renderForm(activeDescriptor) : null}
        </Box>
      ) : activeDescriptor ? (
        renderForm(activeDescriptor)
      ) : null}
      {activeMode === 'sign-in' && smsOpensAccount && selected === 'sms' ? (
        <Typography variant="body2" color="text.secondary">
          {t('mode.signInAccountHint')}
        </Typography>
      ) : null}
      {slots?.belowForms}
    </>
  )

  // The discovery order runs the classes; a class the host left out keeps
  // its default position after the listed ones, so a partial order cannot
  // silently drop a channel set. The first class actually rendered heads
  // the surface, and its divider drops the "or" that would refer to
  // nothing above it.
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

  // The host's message is presented until the viewer starts something else.
  const shownInitialError =
    initialError !== undefined && dismissedHostErrorKey !== hostErrorKey
      ? initialError
      : undefined

  const renderGroup = (group: SignInGroup): ReactNode => {
    const headingVariant = group === firstGroup ? 'plain' : 'or'
    switch (group) {
      case 'form':
        return <Fragment key="form">{formBlock}</Fragment>
      case 'redirect':
        return (
          <GroupHeadingProvider key="redirect" value={headingVariant}>
            <SocialSignInSection
              session={session}
              providers={redirectConfigs}
              onAuthorizeUrl={social?.onAuthorizeUrl}
              providerIcon={providerIcon}
              beforeStart={startGate}
            />
          </GroupHeadingProvider>
        )
      case 'enterprise':
        return (
          <GroupHeadingProvider key="enterprise" value={headingVariant}>
            <EnterpriseSignIn
              session={session}
              returnTo={social?.returnTo}
              onAuthorizeUrl={social?.onAuthorizeUrl}
              beforeStart={startGate}
            />
          </GroupHeadingProvider>
        )
      case 'webauthn':
        return (
          <GroupHeadingProvider key="webauthn" value={headingVariant}>
            {visibleWebauthn.map((method) => (
              <PasskeySignIn
                key={method.name}
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
