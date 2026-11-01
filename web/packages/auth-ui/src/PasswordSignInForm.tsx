/**
 * PasswordSignInForm: the password channel of the sign-in family.
 *
 * A controlled react-hook-form form over the session's login flow: the
 * identifier (a username or email -- the account contract's password login
 * name; a phone is not one, it belongs to the SMS channel) and the password
 * are sent as the flow's first step (`session.startFlow`, method
 * `password`), rendered through ui-kit's FormLayout and FormField
 * skeleton. The skeleton owns the <form>, the validation-error rendering
 * and the actions row; this component owns the submission, the busy state
 * (RHF's isSubmitting while the request is in flight) and the channel's
 * feedback scope (see internal/write-feedback): one whole-attempt failure
 * banner at a time, cleared as the next answer begins.
 *
 * A password step that does not complete immediately -- a forced password
 * change (the method answers `challenge` with a `new_password` field), a
 * required second factor, a factor to register -- is rendered by the
 * shared step renderer (see src/internal/flow-step.tsx) in place of this
 * form's own fields; the same flow is advanced step by step through
 * `session.answerFlow` until it settles. Nothing here navigates and
 * nothing reads the session state: a settled login fires onSignedIn once
 * and the host decides what happens next. The heading above the form is
 * host content -- the component renders no title of its own.
 *
 * A submit whose login lost a concurrent race (another sign-in -- the SMS
 * channel's, a social exchange, a second instance of this form --
 * committed to the session while this one was in flight) answers with
 * auth-core's OperationSupersededError; the shared driver treats that as
 * the lost race it is -- no failure banner and no onSignedIn (the winning
 * call fires its own exactly once), and the session being authenticated
 * now is the host's own snapshot to observe through its auth-core hooks.
 *
 * A refused submit (`authn.login_failed`) is this channel's own text and is
 * attached to the password field: the server collapses an unknown
 * identifier, a wrong password and a wrong code into that one code, so the
 * wording names the password without saying which half did not match, and
 * it never mentions a code this surface has no field for. The message is the
 * field's helper text -- associated through aria-describedby and marking the
 * field aria-invalid -- and focus moves to the password field once the
 * refusal lands, so the focused field carries the message to a screen
 * reader. Every other code renders through the whole-attempt banner.
 */

import { useRef } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import TextField from '@mui/material/TextField'
import { useForm } from 'react-hook-form'
import type { SubmitHandler } from 'react-hook-form'
import { ActionButton, FormField, FormLayout } from '@speed/ui-kit'
import type { AuthSession, AuthnHumanGate } from '@speed/auth-core'
import { useAuthUiTranslation } from './internal/translation.js'
import {
  ChannelFeedbackRegion,
  useChannelFeedback,
} from './internal/write-feedback.js'
import { HumanGatedFlowStep } from './internal/flow-step.js'
import { stepKey, useStepFocus } from './internal/step-focus.js'
import { useFlowDriver } from './internal/use-flow-driver.js'
import type { SignInBeforeStart } from './internal/start-gate.js'

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
  /**
   * The identifier the form starts with. A host handing the viewer over
   * after a registration whose auto-login was refused prefills the
   * username the account was opened with.
   */
  readonly defaultUsername?: string
  /**
   * The human-verification gate the deployment reported, when the host
   * already read the discovery document (the sign-in screen does). Omitted,
   * the form reads it when a parked step may send.
   */
  readonly humanGate?: AuthnHumanGate
  /**
   * Host content ahead of the submit key in the form's action row -- the
   * sign-in screen puts its forgot-password entry here, so the entry and
   * the primary action read as one group instead of the entry floating on
   * a line of its own.
   */
  readonly actionsLeading?: ReactNode
}

interface PasswordFields {
  identifier: string
  password: string
}

/** The server's collapsed credential refusal: an unknown identifier, a wrong
 * password and a wrong code all answer under this one code. */
const LOGIN_FAILED = 'authn.login_failed'

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
  // The channel's own feedback scope: one failure banner at a time, cleared
  // when a new answer begins.
  const feedback = useChannelFeedback()
  // Whether this form's own fields are the mounted surface: a refusal of a
  // later step (a second factor) has no password field to attach to and
  // renders through the banner instead.
  const credentialsMounted = useRef(true)
  // The password input node itself: RHF's setFocus would target the control
  // the Controller's ref is attached to, which MUI's TextField forwards to
  // its form-control wrapper rather than the input.
  const passwordInput = useRef<HTMLInputElement>(null)

  const driver = useFlowDriver(session, {
    beforeStart,
    onFailure: (code) => {
      if (code === LOGIN_FAILED && credentialsMounted.current) {
        // The password field carries the refusal: the same helper-text
        // channel its validation errors use, which MUI associates with the
        // input (aria-describedby) and marks invalid. Focus moves there so
        // the message is announced with the field it belongs to.
        form.setError('password', {
          type: 'server',
          message: t('passwordSignIn.loginFailed'),
        })
        passwordInput.current?.focus()
        feedback.reset()
        return
      }
      feedback.fail(code)
    },
    onSettled: () => {
      try {
        onSignedIn?.()
      } catch {
        // A throwing host callback is not a login failure: the login
        // committed, nothing here renders the host's error, and the
        // containment keeps the throw out of this submit promise.
      }
    },
  })

  credentialsMounted.current = driver.step === null

  // A new attempt begins at the first edit: the previous answer's message
  // -- the password field's server refusal or a whole-attempt banner --
  // stops describing the input as soon as the viewer changes it.
  const clearAttempt = (): void => {
    if (form.formState.errors.password !== undefined) {
      form.clearErrors('password')
    }
    feedback.reset()
  }

  // The credentials form and the parked step are two surfaces of this
  // channel; a switch between them moves focus to the new one's first
  // control, while the form's first render moves nothing.
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

  // A step the flow parked after the credentials were accepted (a forced
  // password change, a second factor) replaces this form's fields: only
  // one surface renders at a time. A second factor that sends a code
  // carries the deployment's proof block above it and presents the proof.
  if (driver.step !== null) {
    return (
      <Box ref={surfaceRef} sx={{ display: 'contents' }}>
        <HumanGatedFlowStep
          session={session}
          humanGate={humanGate}
          step={driver.step}
          busy={driver.busy}
          onAnswer={(input, proof) => {
            // Answering a step is a new attempt: the previous failure must
            // not stand beside the step this answer parks.
            feedback.reset()
            void driver.answer(input, proof)
          }}
          onUnsupported={() => {
            // The channel's first fields are the way out of a step it
            // cannot render; the abandoned step's failure goes with it.
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
      onSubmit={onSubmit}
      actions={
        <>
          {actionsLeading}
          <ActionButton
            type="submit"
            emphasis="primary"
            disabled={form.formState.isSubmitting}
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
