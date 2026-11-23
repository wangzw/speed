/**
 * The discovery-driven channel renderers of the sign-in family.
 *
 * The screen collects a deployment's login methods into three rendering
 * classes beyond the two known forms, and each class lives here:
 *
 *  - `DescriptorSignInForm` is the fallback form for a `form` method the
 *    family has no bespoke component for: it renders the descriptor's own
 *    first-step fields and starts that method's flow, then advances any
 *    parked step through the shared step renderer. The two known form
 *    methods (password, sms) keep their bespoke components; everything
 *    else a deployment adds is driven by its descriptor. A deployment that
 *    requires a human-verification proof renders its control only once the
 *    viewer asks to start and the gate turns out to want one: the start is
 *    held until the proof arrives, the submit control gives way to the
 *    block meanwhile, and editing a field abandons the wait. The start
 *    presents the proof the control holds. Once the held start leaves, the
 *    submit is back on screen and stays busy while that request is in
 *    flight, so it cannot begin a second flow.
 *  - `EnterpriseSignIn` is an `enterprise` method's email-first entry: the
 *    viewer gives an email, the session discovers the identity providers
 *    its domain maps to (`session.discoverSso`), and picking one starts
 *    that provider's flow, whose redirect step the host navigates.
 *  - `PasskeySignIn` is a `webauthn` method's passkey button: it starts the
 *    method's login flow and renders the ceremony step the flow answers
 *    (see ./flow-step).
 *
 * Each drives its own slice of the session contract and renders every
 * string from the auth-ui namespace; a brand name (a provider's own name)
 * comes from the discovery document, not the bundle (see ./labels).
 */

import { useEffect, useRef, useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Divider from '@mui/material/Divider'
import TextField from '@mui/material/TextField'
import { useForm } from 'react-hook-form'
import type { SubmitHandler } from 'react-hook-form'
import type {
  AuthSession,
  AuthnField,
  AuthnHumanGate,
  AuthnMethodDescriptor,
  AuthnSsoProvider,
} from '@speed/auth-core'
import {
  ActionButton,
  FormField,
  FormLayout,
  HumanVerification,
  errorCodeOf,
  useActivationGuard,
  visuallyHiddenSx,
} from '@speed/ui-kit'
import { humanGateNeedsProof, useHumanVerification } from '@speed/auth-core'
import type { HumanScope } from '@speed/auth-core'
import { useAuthUiTranslation } from './translation.js'
import { fieldLabel, methodLabel } from './labels.js'
import {
  ChannelFeedbackRegion,
  useChannelFeedback,
} from './write-feedback.js'
import { HumanGatedFlowStep } from './flow-step.js'
import { retryWaitMs } from './retry-wait.js'
import { firstControl, stepKey, useStepFocus } from './step-focus.js'
import { useFlowDriver } from './use-flow-driver.js'
import { isOpenFlow } from './flow.js'
import { useGroupHeadingVariant } from './group-heading.js'
import { allowedToStart } from './start-gate.js'
import type { SignInBeforeStart } from './start-gate.js'

/** The protocol code an answer that is not the expected redirect step
 * renders (a contract-violating 2xx is not a successful start). */
const UNEXPECTED_ANSWER = 'client.protocol'

/**
 * The generic form for a descriptor-only `form` method: one text input
 * per first-step field, submitted as the flow's start input.
 */
export interface DescriptorSignInFormProps {
  /** The session the method's login drives. */
  readonly session: AuthSession
  /** The method name the flow starts with. */
  readonly method: string
  /** The method's first-step fields, from its descriptor. */
  readonly fields: readonly AuthnField[]
  /** Fired once after the login commits; the host navigates. */
  readonly onSignedIn?: () => void
  /** Decides whether the method's flow may begin (see ./start-gate). */
  readonly beforeStart?: SignInBeforeStart
  /**
   * The human-verification gate the deployment reported, when the host
   * already read the discovery document (the sign-in screen does). Omitted,
   * the form reads it. A gate that requires a proof renders the control on
   * the first step, and the start presents the proof it holds.
   */
  readonly humanGate?: AuthnHumanGate
  /**
   * The send surface the method's first step runs: `phone` when the start
   * may deliver a message to a phone number, `other` when it sends nothing
   * that reaches one. Omitted, the descriptor's fields decide: a `phone`
   * field is an SMS send, every other field is the other surface.
   */
  readonly humanScope?: HumanScope
}

export function DescriptorSignInForm({
  session,
  method,
  fields,
  onSignedIn,
  beforeStart,
  humanGate,
  humanScope,
}: DescriptorSignInFormProps) {
  const { t } = useAuthUiTranslation()
  const defaults: Record<string, string> = {}
  for (const field of fields) {
    defaults[field.name] = ''
  }
  const form = useForm<Record<string, string>>({ defaultValues: defaults })
  const feedback = useChannelFeedback()
  // The values a start the viewer asked for is waiting on, because the gate
  // wants a proof this client does not hold yet. The gate is read and the
  // proof obtained only while this is set, so entering the form shows no
  // challenge. Once the proof arrives the start the viewer already asked
  // for leaves by itself.
  const [pendingStart, setPendingStart] = useState<
    Record<string, string> | null
  >(null)

  const driver = useFlowDriver(session, {
    beforeStart,
    onFailure: (code, error) => feedback.fail(code, retryWaitMs(error)),
    onSettled: () => {
      try {
        onSignedIn?.()
      } catch {
        // A throwing host callback is not a login failure; see the
        // bespoke channel forms for the same containment.
      }
    },
  })

  // The deployment's gate and the proof this form's first step presents.
  // The hook stays inactive until the viewer asks to start and the gate
  // turns out to want a proof, so entering the form reads and solves
  // nothing; a parked step's proof belongs to the shared step renderer
  // below, which reads its own. The first step's surface follows the
  // descriptor: a method whose first step asks for a phone number sends
  // SMS, so it must satisfy the phone surface's mechanism; anything else
  // runs the other surface's. A host that knows better names the surface
  // itself.
  const sendScope: HumanScope =
    humanScope ??
    (fields.some((field) => field.kind === 'phone') ? 'phone' : 'other')
  const human = useHumanVerification(session, {
    scope: sendScope,
    ...(humanGate === undefined ? {} : { gate: humanGate }),
    enabled: pendingStart !== null,
  })
  // The gate the host handed in is known before the hook reads anything; a
  // standalone form learns it through the hook once a start asks for one.
  const gateDocument = humanGate ?? human.gate
  const needsProof = humanGateNeedsProof(gateDocument, sendScope)
  const proofBlocked = needsProof && human.proof === null

  /** Start the flow with the values the viewer submitted. The proof rides
   * along when the gate wants one, and is consumed for the next attempt. */
  const start = async (values: Record<string, string>): Promise<void> => {
    // Last line of defence: a required proof that is not ready yet keeps
    // the start from leaving, whatever path reached this.
    if (proofBlocked) {
      return
    }
    // The proof is single-use: the start spends it, so the control obtains
    // a fresh one for the next attempt.
    const presented = needsProof ? human.proof : null
    await driver.start({
      method,
      binding: 'cookie',
      input: values,
      ...(presented === null ? {} : { human_proof: presented }),
    })
    if (presented !== null) {
      human.consume()
    }
  }

  const startRef = useRef(start)
  startRef.current = start

  /**
   * Ask to start: run now when the gate is known to want no proof (or one
   * is in hand), otherwise hold the values and let the block obtain the
   * proof; the effect below starts when it arrives. A start is asked for
   * only once: while one is held or already in flight, another ask is
   * ignored.
   */
  const askToStart = async (
    values: Record<string, string>,
  ): Promise<void> => {
    if (driver.busy || pendingStart !== null) {
      // The start is already in flight, or waiting on its proof: asking
      // again changes nothing and must not begin a second flow.
      return
    }
    // The attempt begins here, whichever path it takes: the previous
    // refusal must not stand beside the verification the new one renders.
    feedback.reset()
    if (
      !human.pending &&
      gateDocument !== null &&
      (!needsProof || human.proof !== null)
    ) {
      await start(values)
      return
    }
    setPendingStart(values)
  }

  // A start asked for while the gate held it leaves as soon as the gate is
  // known and the proof it wants is in hand: the viewer's one request is
  // not turned into a click followed by a second one. A gate still being
  // read keeps the start waiting; a gate that wants nothing starts at once.
  useEffect(() => {
    if (pendingStart === null || human.pending || gateDocument === null) {
      return
    }
    if (needsProof && human.proof === null) {
      return
    }
    const values = pendingStart
    setPendingStart(null)
    void startRef.current(values)
  }, [pendingStart, human.pending, gateDocument, needsProof, human.proof])

  const onSubmit: SubmitHandler<Record<string, string>> = async (values) => {
    await askToStart(values)
  }

  // The form's own submit is guarded as well: an implicit submission the
  // submit button does not carry (Enter in a field, a programmatic submit)
  // is swallowed while the start is in flight or held for its proof. The
  // form's own `isSubmitting` is already true when this handler runs, so it
  // is not the guard's measure; the start's own state is.
  const guardedSubmit = useActivationGuard(onSubmit, {
    busy: driver.busy || pendingStart !== null,
  })

  // The descriptor's own fields and a parked step are two surfaces of
  // this channel; a switch moves focus to the new one's first control.
  const surfaceRef = useStepFocus(
    driver.step === null ? 'descriptor-fields' : stepKey(driver.step),
  )

  if (driver.step !== null) {
    return (
      <Box ref={surfaceRef} sx={{ display: 'contents' }}>
        <HumanGatedFlowStep
          session={session}
          humanGate={humanGate}
          step={driver.step}
          busy={driver.busy}
          onAttemptStart={() => feedback.reset()}
          onAnswer={(input, proof) => {
            feedback.reset()
            void driver.answer(input, proof)
          }}
          onUnsupported={() => {
            feedback.reset()
            driver.reset()
          }}
          clientFailure={feedback.localFailure}
          onClientFailure={(key) =>
            key === null ? feedback.clearLocal() : feedback.failLocal(key)
          }
        />
        <ChannelFeedbackRegion feedback={feedback} />
      </Box>
    )
  }

  return (
    <FormLayout
      ref={surfaceRef}
      form={form}
      onSubmit={guardedSubmit}
      actions={
        pendingStart === null ? (
          <ActionButton
            type="submit"
            emphasis="primary"
            busy={form.formState.isSubmitting || driver.busy}
          >
            {t('signInFlow.submit')}
          </ActionButton>
        ) : undefined
      }
    >
      {fields.map((field) => (
        <FormField
          key={field.name}
          name={field.name}
          required={field.required === true}
          render={({ field: bound, invalid, errorText }) => (
            <TextField
              {...bound}
              onChange={(event) => {
                bound.onChange(event)
                // Editing a field abandons a start held for its proof: the
                // values the wait would send no longer describe what the
                // viewer typed.
                setPendingStart(null)
              }}
              label={fieldLabel(field, t)}
              type={
                field.kind === 'password' || field.secret === true
                  ? 'password'
                  : 'text'
              }
              inputMode={field.kind === 'code' ? 'numeric' : undefined}
              autoComplete="off"
              fullWidth
              error={invalid}
              helperText={errorText ?? undefined}
            />
          )}
        />
      ))}
      {pendingStart !== null ? (
        <HumanVerification
          scope={sendScope}
          gate={human.gate ?? humanGate}
          proof={human.proof}
          status={human.status}
          captcha={human.captcha}
          onToken={human.adoptToken}
          onRetry={human.retry}
          onRefreshCaptcha={human.refreshCaptcha}
          onRequestAudio={human.requestAudio}
          audioRetryAfterSeconds={human.audioRetryAfterSeconds}
          disabled={driver.busy}
        />
      ) : null}
      <ChannelFeedbackRegion feedback={feedback} />
    </FormLayout>
  )
}

/**
 * An `enterprise` method's email-first entry. The provider set is the
 * server's (a domain's verified IdPs), so the entry renders exactly the
 * providers the viewer's email maps to; the flow's redirect step is handed
 * to the host to navigate.
 */
export interface EnterpriseSignInProps {
  /** The session that discovers the providers and starts their flows. */
  readonly session: AuthSession
  /** The host callback route sent as the flow's `return_to`; omitted to
   * let the server use its configured default. */
  readonly returnTo?: string
  /** Receives a provider's authorization URL once built; the host
   * navigates. */
  readonly onAuthorizeUrl?: (method: string, authorizeUrl: string) => void
  /** Decides whether a provider's flow may begin (see ./start-gate). */
  readonly beforeStart?: SignInBeforeStart
}

export function EnterpriseSignIn({
  session,
  returnTo,
  onAuthorizeUrl,
  beforeStart,
}: EnterpriseSignInProps) {
  const { t } = useAuthUiTranslation()
  const headingVariant = useGroupHeadingVariant()
  const form = useForm<{ email: string }>({ defaultValues: { email: '' } })
  const [providers, setProviders] = useState<readonly AuthnSsoProvider[] | null>(
    null,
  )
  // Which write the entry has in flight: a discovery, or one provider's
  // start. Only the control that started it is busy; the entry's other
  // context controls report unavailable.
  const [pending, setPending] = useState<
    | { readonly kind: 'discover' }
    | { readonly kind: 'provider'; readonly id: string }
    | null
  >(null)
  // The enterprise entry's own feedback scope: a discovery or start failure
  // renders in its region and a new attempt clears the previous one.
  const feedback = useChannelFeedback()

  const discover: SubmitHandler<{ email: string }> = async ({ email }) => {
    feedback.reset()
    setPending({ kind: 'discover' })
    try {
      setProviders(await session.discoverSso(email))
    } catch (error) {
      // A throttled discovery states its wait; the banner names it.
      feedback.fail(errorCodeOf(error), retryWaitMs(error))
    } finally {
      setPending(null)
    }
  }

  const authorize = async (provider: AuthnSsoProvider): Promise<void> => {
    // Take the in-flight state before the gate resolves as well: an async
    // gate suspends under the same busy control, so a second click cannot
    // pass the gate and begin a second flow.
    setPending({ kind: 'provider', id: provider.id })
    // The gate decides whether this provider's flow begins; a refusal
    // starts nothing and renders no error of its own.
    if (!(await allowedToStart(beforeStart, { method: provider.protocol }))) {
      setPending(null)
      return
    }
    feedback.reset()
    let authorizeUrl: string | null = null
    let failure: string | null = null
    let wait: number | undefined
    try {
      const request = {
        method: provider.protocol,
        binding: 'cookie' as const,
        input: { provider: provider.id },
        ...(returnTo === undefined ? {} : { return_to: returnTo }),
      }
      const reply = await session.startFlow(request)
      authorizeUrl =
        isOpenFlow(reply) && reply.next.kind === 'redirect' ? reply.next.url : null
      if (authorizeUrl === null) {
        failure = UNEXPECTED_ANSWER
      }
    } catch (error) {
      failure = errorCodeOf(error)
      // The wait a throttled start stated rides with its code, so the
      // banner names how long the viewer has to wait.
      wait = retryWaitMs(error)
    } finally {
      setPending(null)
    }
    if (failure !== null) {
      feedback.fail(failure, wait)
      return
    }
    if (authorizeUrl !== null) {
      try {
        onAuthorizeUrl?.(provider.protocol, authorizeUrl)
      } catch {
        // A throwing host callback is not an authorization failure; the
        // URL was built, nothing here renders the host's error.
      }
    }
  }

  const editEmail = (): void => {
    feedback.reset()
    setProviders(null)
  }

  // The form's own submit is guarded as well: an implicit submission the
  // submit button does not carry (Enter in the field, a programmatic
  // submit) is swallowed while the discovery is in flight.
  const guardedSubmit = useActivationGuard(discover, {
    busy: pending?.kind === 'discover',
  })

  // The discovery result's notice in the entry's one status region. The
  // region stands for the whole life of the entry, born empty: a live
  // region announces only content changes that follow its own insertion,
  // so a notice that mounted together with its region -- the no-match
  // notice or the providers title -- would be silent. Returning to the
  // email field clears it, making a repeated identical result a change
  // again rather than a rewrite.
  const notice =
    providers === null
      ? null
      : providers.length === 0
        ? t('enterprise.noProviders')
        : t('enterprise.providersTitle')

  // The entry swaps its whole surface when a discovery lands -- the email
  // form gives way to the provider list, or to the one way back when
  // nothing matched -- and again when the viewer returns to the email
  // form. The control that had focus unmounts with its surface, so the
  // browser leaves focus on the body; the new surface's first control
  // takes it from there (the first provider, the way back, or the email
  // field). Focus on anything still in the document is where the viewer
  // moved it, and taking it would undo their choice.
  const surfaceRef = useRef<HTMLDivElement>(null)
  const surfaceKey =
    providers === null
      ? 'email-form'
      : providers.length === 0
        ? 'no-providers'
        : 'providers'
  const previousSurface = useRef(surfaceKey)
  useEffect(() => {
    const replaced = previousSurface.current !== surfaceKey
    previousSurface.current = surfaceKey
    if (!replaced) {
      return
    }
    const container = surfaceRef.current
    if (container === null) {
      return
    }
    const active = document.activeElement
    const focusWasWithTheReplacedSurface =
      active === null ||
      active === document.body ||
      active === document.documentElement ||
      !active.isConnected
    if (focusWasWithTheReplacedSurface) {
      firstControl(container)?.focus()
    }
  }, [surfaceKey])

  return (
    <Box
      ref={surfaceRef}
      sx={{ display: 'flex', flexDirection: 'column', gap: 1, width: '100%' }}
    >
      <Divider textAlign="center" sx={{ color: 'text.secondary' }}>
        {t(headingVariant === 'plain' ? 'enterprise.titleFirst' : 'enterprise.title')}
      </Divider>
      <Alert
        severity="info"
        role="status"
        sx={notice === null ? visuallyHiddenSx : { width: '100%' }}
      >
        {notice ?? ''}
      </Alert>
      {providers === null ? (
        <FormLayout
          form={form}
          onSubmit={guardedSubmit}
          actions={
            <ActionButton
              type="submit"
              emphasis="primary"
              busy={pending?.kind === 'discover'}
            >
              {t('enterprise.continue')}
            </ActionButton>
          }
        >
          <FormField
            name="email"
            required
            render={({ field, invalid, errorText }) => (
              <TextField
                {...field}
                label={t('enterprise.emailLabel')}
                type="email"
                autoComplete="email"
                fullWidth
                error={invalid}
                helperText={errorText ?? undefined}
              />
            )}
          />
          <ChannelFeedbackRegion feedback={feedback} />
        </FormLayout>
      ) : providers.length === 0 ? (
        <ActionButton unavailable={pending !== null} onClick={editEmail}>
          {t('enterprise.editEmail')}
        </ActionButton>
      ) : (
        <>
          {providers.map((provider) => {
            // Only the provider whose start is in flight is busy; the
            // other providers and the way back to the email field change
            // this entry's context, so they report unavailable.
            const starting =
              pending?.kind === 'provider' && pending.id === provider.id
            return (
              <ActionButton
                key={provider.id}
                emphasis="primary"
                fullWidth
                busy={starting}
                unavailable={pending !== null && !starting}
                onClick={() => void authorize(provider)}
              >
                {provider.name}
              </ActionButton>
            )
          })}
          <ActionButton unavailable={pending !== null} onClick={editEmail}>
            {t('enterprise.editEmail')}
          </ActionButton>
          <ChannelFeedbackRegion feedback={feedback} />
        </>
      )}
    </Box>
  )
}

/**
 * A `webauthn` method's passkey button. Clicking it starts the method's
 * login flow; the ceremony step the flow answers is rendered by the shared
 * step renderer in place of the button, and the button returns when the
 * step is abandoned.
 */
export interface PasskeySignInProps {
  /** The session the passkey login drives. */
  readonly session: AuthSession
  /** The webauthn method's descriptor (its name and brand label). */
  readonly method: AuthnMethodDescriptor
  /** Fired once after the passkey login commits; the host navigates. */
  readonly onSignedIn?: () => void
  /** Decides whether the method's flow may begin (see ./start-gate). */
  readonly beforeStart?: SignInBeforeStart
  /**
   * The human-verification gate the deployment reported, when the host
   * already read the discovery document (the sign-in screen does). Omitted,
   * the form reads it when a parked step may send.
   */
  readonly humanGate?: AuthnHumanGate
}

export function PasskeySignIn({
  session,
  method,
  onSignedIn,
  beforeStart,
  humanGate,
}: PasskeySignInProps) {
  const { t } = useAuthUiTranslation()
  const headingVariant = useGroupHeadingVariant()
  const feedback = useChannelFeedback()

  const driver = useFlowDriver(session, {
    beforeStart,
    onFailure: (code, error) => {
      // The server answers every refused assertion with the uniform
      // login_failed code. Its sentence names credentials this channel never
      // asked for, so this channel words that code itself; the wire stays
      // uniform.
      if (code === 'authn.login_failed') {
        feedback.failLocal('signInFlow.passkeyRefused')
        return
      }
      feedback.fail(code, retryWaitMs(error))
    },
    onSettled: () => {
      try {
        onSignedIn?.()
      } catch {
        // Contained; see the bespoke channel forms.
      }
    },
  })

  const begin = async (): Promise<void> => {
    feedback.reset()
    await driver.start({
      method: method.name,
      binding: 'cookie',
    })
  }

  // The passkey button and the ceremony step it opens are two surfaces;
  // a switch between them moves focus to the new one's first control.
  const surfaceRef = useStepFocus(
    driver.step === null ? 'passkey-entry' : stepKey(driver.step),
  )

  return (
    <Box
      ref={surfaceRef}
      sx={{ display: 'flex', flexDirection: 'column', gap: 1, width: '100%' }}
    >
      <Divider textAlign="center" sx={{ color: 'text.secondary' }}>
        {t(headingVariant === 'plain' ? 'passkey.titleFirst' : 'passkey.title')}
      </Divider>
      {driver.step === null ? (
        <ActionButton
          emphasis="primary"
          fullWidth
          busy={driver.busy}
          onClick={() => void begin()}
        >
          {methodLabel(method, t)}
        </ActionButton>
      ) : (
        <HumanGatedFlowStep
          session={session}
          humanGate={humanGate}
          step={driver.step}
          busy={driver.busy}
          onAttemptStart={() => feedback.reset()}
          onAnswer={(input, proof) => {
            feedback.reset()
            void driver.answer(input, proof)
          }}
          onUnsupported={() => {
            feedback.reset()
            driver.reset()
          }}
          clientFailure={feedback.localFailure}
          onClientFailure={(key) =>
            key === null ? feedback.clearLocal() : feedback.failLocal(key)
          }
        />
      )}
      <ChannelFeedbackRegion feedback={feedback} />
    </Box>
  )
}
