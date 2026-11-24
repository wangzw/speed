/** The discovery-driven channel renderers of the sign-in family: the
 * descriptor form, the enterprise entry and the passkey entry. */

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

const UNEXPECTED_ANSWER = 'client.protocol'

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
   * The human-verification gate the deployment reported, when the host already
   * read the discovery document. Omitted, the form reads it.
   */
  readonly humanGate?: AuthnHumanGate
  /**
   * The send surface the method's first step runs. Omitted, a `phone` field
   * means `phone`, every other field `other`.
   */
  readonly humanScope?: HumanScope
}

/**
 * The fallback form for a `form` method the family has no bespoke component
 * for: renders the descriptor's first-step fields and starts that method's
 * flow, then advances a parked step through the shared renderer.
 */
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
  // Values of a start waiting on a proof. The gate and proof are read only
  // while this is set, so the form alone shows no challenge.
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
        // A throwing host callback is not a login failure.
      }
    },
  })

  // The first step's surface follows the descriptor; a host may name it.
  const sendScope: HumanScope =
    humanScope ??
    (fields.some((field) => field.kind === 'phone') ? 'phone' : 'other')
  const human = useHumanVerification(session, {
    scope: sendScope,
    ...(humanGate === undefined ? {} : { gate: humanGate }),
    enabled: pendingStart !== null,
  })
  const gateDocument = humanGate ?? human.gate
  const needsProof = humanGateNeedsProof(gateDocument, sendScope)
  const proofBlocked = needsProof && human.proof === null

  /** Start the flow with the submitted values, presenting the proof a gated
   * gate requires. */
  const start = async (values: Record<string, string>): Promise<void> => {
    if (proofBlocked) {
      return
    }
    // The proof is single-use: a fresh one is obtained for the next attempt.
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
   * Ask to start: run now when the gate wants no proof, else hold the values
   * until the effect below sees a proof. A start is asked for only once.
   */
  const askToStart = async (
    values: Record<string, string>,
  ): Promise<void> => {
    if (driver.busy || pendingStart !== null) {
      return
    }
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

  // A held start leaves as soon as the gate is known and its proof is in hand;
  // an in-flight write keeps it waiting.
  useEffect(() => {
    if (pendingStart === null || human.pending || gateDocument === null) {
      return
    }
    if (needsProof && human.proof === null) {
      return
    }
    if (driver.busy) {
      return
    }
    const values = pendingStart
    setPendingStart(null)
    void startRef.current(values)
  }, [
    pendingStart,
    human.pending,
    gateDocument,
    needsProof,
    human.proof,
    driver.busy,
  ])

  const onSubmit: SubmitHandler<Record<string, string>> = async (values) => {
    await askToStart(values)
  }

  // Guard the implicit submit too, by the start's own state (RHF's
  // `isSubmitting` is already true when the handler runs).
  const guardedSubmit = useActivationGuard(onSubmit, {
    busy: driver.busy || pendingStart !== null,
  })

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
        <ActionButton
          type="submit"
          emphasis="primary"
          // The submit that asked stays mounted and reports the wait on its
          // own proof as busy, so it keeps the focus it had.
          busy={
            form.formState.isSubmitting ||
            driver.busy ||
            pendingStart !== null
          }
        >
          {t('signInFlow.submit')}
        </ActionButton>
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

/**
 * Collects an email, discovers the providers its domain maps to, and starts
 * the picked provider's flow; the redirect step is handed to the host.
 */
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
  // The write in flight: a discovery or one provider's start. Only the control
  // that started it is busy; the others report unavailable.
  const [pending, setPending] = useState<
    | { readonly kind: 'discover' }
    | { readonly kind: 'provider'; readonly id: string }
    | null
  >(null)
  const feedback = useChannelFeedback()

  const discover: SubmitHandler<{ email: string }> = async ({ email }) => {
    feedback.reset()
    setPending({ kind: 'discover' })
    try {
      setProviders(await session.discoverSso(email))
    } catch (error) {
      feedback.fail(errorCodeOf(error), retryWaitMs(error))
    } finally {
      setPending(null)
    }
  }

  const authorize = async (provider: AuthnSsoProvider): Promise<void> => {
    // Take the in-flight state before the gate resolves, so a second click
    // cannot pass an async gate and begin a second flow.
    setPending({ kind: 'provider', id: provider.id })
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
        // A throwing host callback is not an authorization failure.
      }
    }
  }

  const editEmail = (): void => {
    feedback.reset()
    setProviders(null)
  }

  const guardedSubmit = useActivationGuard(discover, {
    busy: pending?.kind === 'discover',
  })

  // The region is born empty: a live region announces only changes after its
  // own insertion, so a notice mounted together with it would be silent.
  const notice =
    providers === null
      ? null
      : providers.length === 0
        ? t('enterprise.noProviders')
        : t('enterprise.providersTitle')

  // A landed discovery swaps the whole surface; the new surface's first
  // control takes focus only if the replaced one left it on the body.
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
            // Only the provider being started is busy; the others report
            // unavailable because this entry's context is changing.
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

export interface PasskeySignInProps {
  /** The session the passkey login drives. */
  readonly session: AuthSession
  /** The webauthn method's descriptor (its name and brand label). */
  readonly method: AuthnMethodDescriptor
  /** Fired once after the passkey login commits; the host navigates. */
  readonly onSignedIn?: () => void
  /** Decides whether the method's flow may begin (see ./start-gate). */
  readonly beforeStart?: SignInBeforeStart
  /** The human-verification gate the deployment reported. Omitted, the form
   * reads it when a parked step may send. */
  readonly humanGate?: AuthnHumanGate
}

/**
 * The passkey button: starts the method's flow, and the ceremony step it opens
 * is rendered in place of the button.
 */
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
      // The server's uniform login_failed sentence names credentials this
      // channel never asked for, so the channel words that code itself.
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
        // Contained: a throwing host callback is not a login failure.
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
