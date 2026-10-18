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
 *    leak across surfaces.
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
 * `mode` is a pure presentational switch over the one sign-in flow: the
 * component presents a "register" entry (and a matching "sign in" entry
 * back) where the deployment allows a first login to open an account
 * (`discovery.signup.enabled` and at least one visible method carries
 * `opens_account`). Registration is not a separate form -- it is a login
 * flow's account-resolution outcome under the deployment's signup setting.
 * A successful sign-in on a channel the screen drives itself fires
 * onSignedIn once, with the mode the screen was in and that channel's
 * method name (see SignInContext): a host can tell a registration from an
 * ordinary sign-in. Redirect and enterprise channels complete through the
 * callback route rather than this screen, so they carry no context. The
 * screen renders no heading and nothing here navigates: the page above
 * (branding, the heading) is host content.
 *
 * `discovery` lets a host inject a pre-fetched document (SSR, a test), in
 * which case the screen fires no read of its own. The deprecated
 * `channels` / `defaultChannel` props still narrow and select the form
 * channels for one major.
 */

import { useEffect, useId, useState } from 'react'
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

/** The form-channel names the deprecated override recognises. */
export type SignInChannel = 'password' | 'sms'

/** The presentational mode of the surface (both run the one sign-in flow). */
export type SignInMode = 'sign-in' | 'register'

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
  /** The presentational mode; `'sign-in'` by default. */
  readonly mode?: SignInMode
  /** Fired once after a sign-in commits on a channel this screen drives,
   * with the mode and the channel's method name; the host decides what
   * follows. A host that ignores the argument is unaffected. */
  readonly onSignedIn?: (context: SignInContext) => void
  /** The redirect / enterprise block of the screen; omitted to run without
   * one. */
  readonly social?: SocialSignInOptions
  /**
   * The form channels to offer, by name.
   * @deprecated The channels now come from the discovery document; this
   * override still narrows the form strip for one major.
   */
  readonly channels?: readonly SignInChannel[]
  /**
   * The form channel selected on first render; password by default. When
   * the named channel is not offered, the first offered one is shown.
   * @deprecated The channel selection is presentational; still honored for
   * one major.
   */
  readonly defaultChannel?: SignInChannel
}

export function SignInScreen({
  session,
  discovery,
  mode = 'sign-in',
  onSignedIn,
  social,
  channels,
  defaultChannel = 'password',
}: SignInScreenProps) {
  const { t } = useAuthUiTranslation()
  const [attempt, setAttempt] = useState(0)
  const fetched = useSignInDiscovery(session, {
    enabled: discovery === undefined,
    attempt,
  })
  const state: SignInDiscovery =
    discovery === undefined ? fetched : { status: 'ready', discovery }
  const [activeMode, setActiveMode] = useState<SignInMode>(mode)
  useEffect(() => {
    setActiveMode(mode)
  }, [mode])
  // The id base of the tab/tabpanel pair per form channel: each channel's
  // tab names its panel through aria-controls, and the panel -- the
  // mounted form, unmounted on switch -- answers through aria-labelledby.
  const channelId = useId()
  const [channel, setChannel] = useState<string | null>(null)

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

  const formNames = formMethods.map((method) => method.name)
  const selected =
    channel !== null && formNames.includes(channel)
      ? channel
      : formNames.includes(defaultChannel)
        ? defaultChannel
        : (formNames[0] ?? '')
  const activeDescriptor = formMethods.find((method) => method.name === selected)

  // The methods this screen actually renders: the form channels left after
  // the deprecated narrowing, the redirect methods (unless the deprecated
  // provider override replaces them), and the enterprise / webauthn
  // entries. A method with an unknown kind renders nothing, so it cannot
  // make the register entry visible.
  const renderedMethods = [
    ...formMethods,
    ...(social?.providers === undefined ? redirectMethods : []),
    ...enterpriseMethods,
    ...webauthnMethods,
  ]

  const registerVisible =
    doc.signup?.enabled === true &&
    renderedMethods.some((method) => method.opens_account === true)

  const redirectConfigs: readonly SocialProviderConfig[] =
    social?.providers ??
    redirectMethods.map((method) => ({
      provider: method.name,
      label: method.label,
      redirectUri: social?.returnTo ?? '',
    }))

  const tabIdOf = (name: string): string => `${channelId}-${name}-tab`
  const panelIdOf = (name: string): string => `${channelId}-${name}-panel`

  // The context the screen reports when a channel it drives completes. The
  // channel forms keep their own no-argument onSignedIn (a leaf knows only
  // its flow); the screen binds the method name here, and the mode is the
  // one currently presented -- the register entry runs the same flow.
  const completed = (channelName: string): void => {
    onSignedIn?.({ mode: activeMode, channel: channelName })
  }

  const renderForm = (method: AuthnMethodDescriptor): ReactNode => {
    switch (method.name) {
      case 'password':
        return (
          <PasswordSignInForm
            session={session}
            onSignedIn={() => completed('password')}
          />
        )
      case 'sms':
        return (
          <SMSSignInForm
            session={session}
            onSignedIn={() => completed('sms')}
          />
        )
      default:
        return (
          <DescriptorSignInForm
            session={session}
            method={method.name}
            fields={method.fields ?? []}
            onSignedIn={() => completed(method.name)}
          />
        )
    }
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, width: '100%' }}>
      {registerVisible ? (
        <>
          {activeMode === 'register' ? (
            <Typography variant="body2" color="text.secondary">
              {t('mode.registerHint')}
            </Typography>
          ) : null}
          <Button
            variant="text"
            onClick={() =>
              setActiveMode(activeMode === 'sign-in' ? 'register' : 'sign-in')
            }
          >
            {activeMode === 'sign-in'
              ? t('mode.registerEntry')
              : t('mode.signInEntry')}
          </Button>
        </>
      ) : null}
      {formMethods.length > 1 ? (
        <Tabs
          value={selected}
          onChange={(_event, value: string) => setChannel(value)}
          variant="fullWidth"
        >
          {formMethods.map((method) => (
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
      {formMethods.length > 1 ? (
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
      {redirectConfigs.length > 0 ? (
        <SocialSignInSection
          session={session}
          providers={redirectConfigs}
          onAuthorizeUrl={social?.onAuthorizeUrl}
        />
      ) : null}
      {enterpriseMethods.length > 0 ? (
        <EnterpriseSignIn
          session={session}
          returnTo={social?.returnTo}
          onAuthorizeUrl={social?.onAuthorizeUrl}
        />
      ) : null}
      {webauthnMethods.map((method) => (
        <PasskeySignIn
          key={method.name}
          session={session}
          method={method}
          onSignedIn={() => completed(method.name)}
        />
      ))}
    </Box>
  )
}
