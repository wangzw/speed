/**
 * PasswordSignInForm: the password channel of the sign-in family.
 *
 * A controlled react-hook-form form over the session's login flow: the
 * identifier (email, phone, or username -- the backend decides which) and
 * the password are sent as the flow's first step (`session.startFlow`, method
 * `password`), rendered through ui-kit's FormLayout and FormField
 * skeleton. The skeleton owns the <form>, the validation-error rendering
 * and the actions row; this component owns the submission, the busy state
 * (RHF's isSubmitting while the request is in flight) and the
 * whole-attempt failure banner (one InlineError per failed submit, code
 * resolved to current-language text).
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
 */

import { useState } from 'react'
import TextField from '@mui/material/TextField'
import Button from '@mui/material/Button'
import { useForm } from 'react-hook-form'
import type { SubmitHandler } from 'react-hook-form'
import { FormField, FormLayout } from '@speed/ui-kit'
import type { AuthSession } from '@speed/auth-core'
import { useAuthUiTranslation } from './internal/translation.js'
import { InlineError } from './internal/inline-error.js'
import { FlowStepFields } from './internal/flow-step.js'
import { useFlowDriver } from './internal/use-flow-driver.js'

export interface PasswordSignInFormProps {
  /** The session the password login drives. */
  readonly session: AuthSession
  /** Fired once after a password login commits; the host navigates. */
  readonly onSignedIn?: () => void
}

interface PasswordFields {
  identifier: string
  password: string
}

export function PasswordSignInForm({
  session,
  onSignedIn,
}: PasswordSignInFormProps) {
  const { t } = useAuthUiTranslation()
  const form = useForm<PasswordFields>({
    defaultValues: { identifier: '', password: '' },
  })
  const [errorCode, setErrorCode] = useState<string | null>(null)

  const driver = useFlowDriver(session, {
    onFailure: setErrorCode,
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

  const onSubmit: SubmitHandler<PasswordFields> = async ({
    identifier,
    password,
  }) => {
    setErrorCode(null)
    await driver.start({
      method: 'password',
      binding: 'cookie',
      input: { identifier, password },
    })
  }

  // A step the flow parked after the credentials were accepted (a forced
  // password change, a second factor) replaces this form's fields: only
  // one surface renders at a time.
  if (driver.step !== null) {
    return (
      <>
        <FlowStepFields
          step={driver.step}
          busy={driver.busy}
          onAnswer={(input) => void driver.answer(input)}
        />
        <InlineError code={errorCode} />
      </>
    )
  }

  return (
    <FormLayout
      form={form}
      onSubmit={onSubmit}
      actions={
        <Button
          type="submit"
          variant="contained"
          disabled={form.formState.isSubmitting}
        >
          {t('passwordSignIn.submit')}
        </Button>
      }
    >
      <FormField
        name="identifier"
        required
        render={({ field, invalid, errorText }) => (
          <TextField
            {...field}
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
            label={t('passwordSignIn.passwordLabel')}
            type="password"
            autoComplete="current-password"
            fullWidth
            error={invalid}
            helperText={errorText ?? undefined}
          />
        )}
      />
      <InlineError code={errorCode} />
    </FormLayout>
  )
}
