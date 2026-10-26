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
 *    else a deployment adds is driven by its descriptor.
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

import { useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
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
import { FormField, FormLayout, errorCodeOf, visuallyHiddenSx } from '@speed/ui-kit'
import { useAuthUiTranslation } from './translation.js'
import { fieldLabel, methodLabel } from './labels.js'
import {
  ChannelFeedbackRegion,
  useChannelFeedback,
} from './write-feedback.js'
import { HumanGatedFlowStep } from './flow-step.js'
import { stepKey, useStepFocus } from './step-focus.js'
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
   * the form reads it.
   */
  readonly humanGate?: AuthnHumanGate
}

export function DescriptorSignInForm({
  session,
  method,
  fields,
  onSignedIn,
  beforeStart,
  humanGate,
}: DescriptorSignInFormProps) {
  const { t } = useAuthUiTranslation()
  const defaults: Record<string, string> = {}
  for (const field of fields) {
    defaults[field.name] = ''
  }
  const form = useForm<Record<string, string>>({ defaultValues: defaults })
  const feedback = useChannelFeedback()

  const driver = useFlowDriver(session, {
    beforeStart,
    onFailure: (code) => feedback.fail(code),
    onSettled: () => {
      try {
        onSignedIn?.()
      } catch {
        // A throwing host callback is not a login failure; see the
        // bespoke channel forms for the same containment.
      }
    },
  })

  const onSubmit: SubmitHandler<Record<string, string>> = async (values) => {
    feedback.reset()
    await driver.start({
      method,
      binding: 'cookie',
      input: values,
    })
  }

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
          onAnswer={(input, proof) => {
            feedback.reset()
            void driver.answer(input, proof)
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
      onSubmit={onSubmit}
      actions={
        <Button
          type="submit"
          variant="contained"
          disabled={form.formState.isSubmitting}
        >
          {t('signInFlow.submit')}
        </Button>
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
  const form = useForm<{ email: string }>({ defaultValues: { email: '' } })
  const [providers, setProviders] = useState<readonly AuthnSsoProvider[] | null>(
    null,
  )
  const [busy, setBusy] = useState(false)
  // The enterprise entry's own feedback scope: a discovery or start failure
  // renders in its region and a new attempt clears the previous one.
  const feedback = useChannelFeedback()

  const discover: SubmitHandler<{ email: string }> = async ({ email }) => {
    feedback.reset()
    setBusy(true)
    try {
      setProviders(await session.discoverSso(email))
    } catch (error) {
      feedback.fail(errorCodeOf(error))
    } finally {
      setBusy(false)
    }
  }

  const authorize = async (provider: AuthnSsoProvider): Promise<void> => {
    // Take the busy flag before the gate resolves as well: an async gate
    // suspends under the same disabled button, so a second click cannot
    // pass the gate and begin a second flow.
    setBusy(true)
    // The gate decides whether this provider's flow begins; a refusal
    // starts nothing and renders no error of its own.
    if (!(await allowedToStart(beforeStart, { method: provider.protocol }))) {
      setBusy(false)
      return
    }
    feedback.reset()
    let authorizeUrl: string | null = null
    let failure: string | null = null
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
    } finally {
      setBusy(false)
    }
    if (failure !== null) {
      feedback.fail(failure)
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

  return (
    <Box
      sx={{ display: 'flex', flexDirection: 'column', gap: 1, width: '100%' }}
    >
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
          onSubmit={discover}
          actions={
            <Button type="submit" variant="contained" disabled={busy}>
              {t('enterprise.continue')}
            </Button>
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
        <Button variant="text" onClick={editEmail}>
          {t('enterprise.editEmail')}
        </Button>
      ) : (
        <>
          {providers.map((provider) => (
            <Button
              key={provider.id}
              variant="outlined"
              fullWidth
              disabled={busy}
              onClick={() => void authorize(provider)}
            >
              {provider.name}
            </Button>
          ))}
          <Button variant="text" onClick={editEmail}>
            {t('enterprise.editEmail')}
          </Button>
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
    onFailure: (code) => feedback.fail(code),
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
        <Button
          variant="outlined"
          fullWidth
          disabled={driver.busy}
          onClick={() => void begin()}
        >
          {methodLabel(method, t)}
        </Button>
      ) : (
        <HumanGatedFlowStep
          session={session}
          humanGate={humanGate}
          step={driver.step}
          busy={driver.busy}
          onAnswer={(input, proof) => {
            feedback.reset()
            void driver.answer(input, proof)
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
