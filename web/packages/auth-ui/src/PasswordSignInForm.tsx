/** The password channel of the sign-in family. */

import { useRef } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import TextField from '@mui/material/TextField'
import { useForm } from 'react-hook-form'
import type { SubmitHandler } from 'react-hook-form'
import {
  ActionButton,
  FormField,
  FormLayout,
  useActivationGuard,
} from '@speed/ui-kit'
import type { AuthSession, AuthnHumanGate } from '@speed/auth-core'
import { useAuthUiTranslation } from './internal/translation.js'
import {
  ChannelFeedbackRegion,
  useChannelFeedback,
} from './internal/write-feedback.js'
import { HumanGatedFlowStep } from './internal/flow-step.js'
import { retryWaitMs } from './internal/retry-wait.js'
import { stepKey, useStepFocus } from './internal/step-focus.js'
import { useFlowDriver } from './internal/use-flow-driver.js'
import type { SignInBeforeStart } from './internal/start-gate.js'

/** The host supplies the session the login drives and the callbacks around
 * it; the form owns the fields and the flow's step state. */
export interface PasswordSignInFormProps {
  /** The session the password login drives. */
  readonly session: AuthSession
  /** Fired once after a password login commits; the host navigates. */
  readonly onSignedIn?: () => void
  /**
   * Decides whether the password flow may begin. Returning false starts
   * nothing and renders no error; only the first step is gated.
   */
  readonly beforeStart?: SignInBeforeStart
  /** The identifier the form starts with. */
  readonly defaultUsername?: string
  /**
   * The human-verification gate the deployment reported, when the host
   * already read the discovery document. Omitted, the form reads it when a
   * parked step may send.
   */
  readonly humanGate?: AuthnHumanGate
  /** Host content ahead of the submit key in the form's action row. */
  readonly actionsLeading?: ReactNode
}

interface PasswordFields {
  identifier: string
  password: string
}

/** The server's collapsed credential refusal: an unknown identifier, a wrong
 * password and a wrong code all answer under this one code. */
const LOGIN_FAILED = 'authn.login_failed'

/**
 * The password channel: drives a `method: 'password'` login flow from the
 * identifier and password fields, then renders any parked step through the
 * shared step renderer. A settled login fires `onSignedIn` once.
 */
export function PasswordSignInForm({
  session,
  onSignedIn,
  beforeStart,
  defaultUsername,
  humanGate,
  actionsLeading,
}: PasswordSignInFormProps) {
  const { t } = useAuthUiTranslation()
  const form = useForm<PasswordFields>({
    defaultValues: { identifier: defaultUsername ?? '', password: '' },
  })
  const feedback = useChannelFeedback()
  // False once a parked step replaces these fields: its refusals have no
  // password field to attach to and render through the banner instead.
  const credentialsMounted = useRef(true)
  // MUI's TextField forwards a ref to its form-control wrapper, not the
  // input, so RHF's setFocus would target the wrong node.
  const passwordInput = useRef<HTMLInputElement>(null)

  const driver = useFlowDriver(session, {
    beforeStart,
    onFailure: (code, error) => {
      if (code === LOGIN_FAILED && credentialsMounted.current) {
        form.setError('password', {
          type: 'server',
          message: t('passwordSignIn.loginFailed'),
        })
        passwordInput.current?.focus()
        feedback.reset()
        return
      }
      feedback.fail(code, retryWaitMs(error))
    },
    onSettled: () => {
      try {
        onSignedIn?.()
      } catch {
        // A throwing host callback is not a login failure: the login
        // committed and the throw stays out of this submit promise.
      }
    },
  })

  credentialsMounted.current = driver.step === null

  const clearAttempt = (): void => {
    if (form.formState.errors.password !== undefined) {
      form.clearErrors('password')
    }
    feedback.reset()
  }

  const surfaceRef = useStepFocus(
    driver.step === null ? 'password-credentials' : stepKey(driver.step),
  )

  const onSubmit: SubmitHandler<PasswordFields> = async ({
    identifier,
    password,
  }) => {
    feedback.reset()
    form.clearErrors('password')
    await driver.start({
      method: 'password',
      binding: 'cookie',
      input: { identifier, password },
    })
  }

  // RHF raises `isSubmitting` before calling the handler, so a guard judged
  // by it would swallow the handler's first call; `driver.busy` stays true
  // until the request settles.
  const submitBusy = form.formState.isSubmitting || driver.busy

  const guardedSubmit = useActivationGuard(onSubmit, { busy: driver.busy })

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
        <>
          {actionsLeading}
          <ActionButton
            type="submit"
            emphasis="primary"
            busy={submitBusy}
          >
            {t('passwordSignIn.submit')}
          </ActionButton>
        </>
      }
    >
      <FormField
        name="identifier"
        required
        render={({ field, invalid, errorText }) => (
          <TextField
            {...field}
            onChange={(event) => {
              field.onChange(event)
              clearAttempt()
            }}
            label={t('passwordSignIn.identifierLabel')}
            autoComplete="username"
            fullWidth
            error={invalid}
            helperText={errorText ?? undefined}
          />
        )}
      />
      <FormField
        name="password"
        required
        render={({ field, invalid, errorText }) => (
          <TextField
            {...field}
            inputRef={passwordInput}
            onChange={(event) => {
              field.onChange(event)
              clearAttempt()
            }}
            label={t('passwordSignIn.passwordLabel')}
            type="password"
            autoComplete="current-password"
            fullWidth
            error={invalid}
            helperText={errorText ?? undefined}
          />
        )}
      />
      <ChannelFeedbackRegion feedback={feedback} />
    </FormLayout>
  )
}
