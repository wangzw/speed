/** The password channel's account-opening form. */

import { useEffect, useId, useRef, useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useForm } from 'react-hook-form'
import type { SubmitHandler } from 'react-hook-form'
import {
  ActionButton,
  FormField,
  FormLayout,
  errorCodeOf,
  parseEmailAddress,
  useActivationGuard,
  visuallyHiddenSx,
} from '@speed/ui-kit'
import type { AuthSession, AuthnHumanGate } from '@speed/auth-core'
import { useAuthUiTranslation } from './internal/translation.js'
import { InlineError } from './internal/inline-error.js'
import { HumanGatedFlowStep } from './internal/flow-step.js'
import { stepKey, useStepFocus } from './internal/step-focus.js'
import { useFlowDriver } from './internal/use-flow-driver.js'
import { allowedToStart } from './internal/start-gate.js'
import type { SignInBeforeStart } from './internal/start-gate.js'
import { retryWaitMs } from './internal/retry-wait.js'
import type { SignInContext } from './SignInScreen.js'

/** The host supplies the session the registration opens and signs in, and the
 * callbacks around it; the form owns the fields and the step state. */
export interface PasswordRegisterFormProps {
  /** The session the registration opens an account on and signs in. */
  readonly session: AuthSession
  /** Fired once after the auto-login commits; the host navigates. */
  readonly onSignedIn?: (context: SignInContext) => void
  /**
   * Fired after the account was opened but its auto-login was refused, with
   * the username the registration used. A host that omits it gets the
   * in-place "account created, please sign in" notice.
   */
  readonly onSignInRequired?: (username: string) => void
  /**
   * Decides whether the registration may begin. Returning false starts
   * nothing and renders no error; the auto-login after it is not gated again.
   * The context is `{method: 'password', mode: 'register'}`.
   */
  readonly beforeStart?: SignInBeforeStart
  /**
   * The human-verification gate the deployment reported, when the host
   * already read the discovery document. Omitted, the form reads it when a
   * parked step may send.
   */
  readonly humanGate?: AuthnHumanGate
}

interface RegisterFields {
  username: string
  displayName: string
  email: string
  password: string
  confirmPassword: string
}

const REFUSAL_TEXT_KEYS: Readonly<Record<string, string>> = {
  'authn.signup_closed': 'register.signupClosed',
  'authn.throttled': 'register.throttled',
  'authn.unavailable': 'register.unavailable',
}

const USERNAME_TAKEN = 'authn.username_taken'
const USERNAME_RESERVED = 'authn.username_reserved'
const USERNAME_INVALID = 'authn.username_invalid'

const EMAIL_TAKEN = 'authn.email_taken'

/** Opens an account with `session.register`, then signs in with the same
 * credentials. Only a settled auto-login fires `onSignedIn`; a refused one
 * reports `onSignInRequired` or shows the in-place notice. */
export function PasswordRegisterForm({
  session,
  onSignedIn,
  onSignInRequired,
  beforeStart,
  humanGate,
}: PasswordRegisterFormProps) {
  const { t } = useAuthUiTranslation()
  const throttleNoteId = useId()
  const form = useForm<RegisterFields>({
    defaultValues: {
      username: '',
      displayName: '',
      email: '',
      password: '',
      confirmPassword: '',
    },
  })
  const [failure, setFailure] = useState<
    | { readonly text: string }
    | { readonly code: string; readonly retryAfterSeconds?: number }
    | null
  >(null)
  const [, setRetryTick] = useState(0)
  const [status, setStatus] = useState<
    'created' | 'signInRequired' | 'signedIn' | null
  >(null)
  const [retryUntil, setRetryUntil] = useState<number | null>(null)
  // RHF's `isSubmitting` is raised before the handler runs, so a guard keyed to
  // it would swallow the submission that set it; this flag starts inside the
  // handler and spans the opening and the auto-login.
  const [attempting, setAttempting] = useState(false)
  // MUI's TextField ref points at the wrapper, not the input, so RHF's
  // setFocus would target the wrong node.
  const usernameInput = useRef<HTMLInputElement>(null)
  const emailInput = useRef<HTMLInputElement>(null)
  // Once the account is open, a password-flow failure is an auto-login
  // refusal, not a registration failure.
  const registered = useRef(false)
  const credentials = useRef<{ username: string; password: string } | null>(
    null,
  )

  const driver = useFlowDriver(session, {
    onFailure: () => {
      if (!registered.current) {
        return
      }
      setStatus('signInRequired')
      onSignInRequired?.(credentials.current?.username ?? '')
    },
    onSuperseded: () => {
      if (registered.current) {
        setStatus('signInRequired')
      }
    },
    onSettled: () => {
      if (!registered.current) {
        return
      }
      setStatus('signedIn')
      try {
        onSignedIn?.({ mode: 'register', channel: 'password' })
      } catch {
        // A throwing host callback is not a login failure: the login
        // committed.
      }
    },
  })

  // A stated Retry-After wait counts down on its own; when the tick crosses
  // the deadline it clears, re-enabling the submit key, while the failure
  // text stays.
  useEffect(() => {
    if (retryUntil === null) {
      return undefined
    }
    if (Date.now() >= retryUntil) {
      setRetryUntil(null)
      return undefined
    }
    const timer = setInterval(() => {
      if (Date.now() >= retryUntil) {
        setRetryUntil(null)
        clearInterval(timer)
        return
      }
      setRetryTick((tick) => tick + 1)
    }, 1000)
    return () => clearInterval(timer)
  }, [retryUntil])

  const startSignIn = async (): Promise<void> => {
    const held = credentials.current
    if (held === null) {
      return
    }
    await driver.start({
      method: 'password',
      binding: 'cookie',
      input: { identifier: held.username, password: held.password },
    })
  }

  const submitRegistration = async (
    values: RegisterFields,
  ): Promise<void> => {
    setFailure(null)
    form.clearErrors()
    if (
      !(await allowedToStart(beforeStart, {
        method: 'password',
        mode: 'register',
      }))
    ) {
      return
    }
    try {
      await session.register({
        username: values.username,
        email: values.email,
        password: values.password,
        // An empty display name sends no key rather than an empty string.
        ...(values.displayName.trim() === ''
          ? {}
          : { display_name: values.displayName.trim() }),
      })
    } catch (error) {
      const code = errorCodeOf(error)
      const usernameKey =
        code === USERNAME_TAKEN
          ? 'register.usernameTaken'
          : code === USERNAME_RESERVED
            ? 'register.usernameReserved'
            : code === USERNAME_INVALID
              ? 'register.usernameInvalid'
              : null
      if (usernameKey !== null) {
        form.setError('username', {
          type: 'server',
          message: t(usernameKey),
        })
        usernameInput.current?.focus()
        return
      }
      if (code === EMAIL_TAKEN) {
        form.setError('email', {
          type: 'server',
          message: t('register.emailTaken'),
        })
        emailInput.current?.focus()
        return
      }
      const wait = retryWaitMs(error)
      if (wait !== undefined) {
        setRetryUntil(Date.now() + wait)
        setFailure({
          code: 'authn.throttled',
          retryAfterSeconds: Math.max(1, Math.ceil(wait / 1000)),
        })
        return
      }
      const key = REFUSAL_TEXT_KEYS[code]
      setFailure(key === undefined ? { code } : { text: t(key) })
      return
    }
    registered.current = true
    credentials.current = {
      username: values.username,
      password: values.password,
    }
    setStatus('created')
    await startSignIn()
  }

  const onSubmit: SubmitHandler<RegisterFields> = async (values) => {
    setAttempting(true)
    try {
      await submitRegistration(values)
    } finally {
      setAttempting(false)
    }
  }

  const autoSignInFailed = status === 'signInRequired'
  const signedIn = status === 'signedIn'
  const busy = attempting || driver.busy
  const throttled = retryUntil !== null
  // An open account closes the submit path too: the submit key is gone, but
  // the form element remains and a synthetic submit must not re-open the
  // account.
  const guardedSubmit = useActivationGuard(onSubmit, {
    busy,
    unavailable: throttled || autoSignInFailed || signedIn,
  })

  const shownStep =
    driver.step !== null && !autoSignInFailed && !signedIn ? driver.step : null
  const surfaceRef = useStepFocus(
    shownStep !== null ? stepKey(shownStep) : signedIn ? null : 'register-fields',
  )

  if (shownStep !== null) {
    return (
      <Box ref={surfaceRef} sx={{ display: 'contents' }}>
        <HumanGatedFlowStep
          session={session}
          humanGate={humanGate}
          step={shownStep}
          busy={driver.busy}
          onAttemptStart={() => setFailure(null)}
          onAnswer={(input, proof) => void driver.answer(input, proof)}
          onUnsupported={() => driver.reset()}
        />
        <InlineError code={failure !== null && 'code' in failure ? failure.code : null} />
      </Box>
    )
  }

  const fields = (
    <>
      <FormField
        name="username"
        required
        render={({ field, invalid, errorText }) => (
          <TextField
            {...field}
            inputRef={usernameInput}
            label={t('register.usernameLabel')}
            autoComplete="username"
            fullWidth
            slotProps={{ input: { readOnly: autoSignInFailed } }}
            error={invalid}
            helperText={errorText ?? undefined}
          />
        )}
      />
      <FormField
        name="displayName"
        render={({ field, invalid, errorText }) => (
          <TextField
            {...field}
            label={t('register.displayNameLabel')}
            autoComplete="name"
            fullWidth
            slotProps={{ input: { readOnly: autoSignInFailed } }}
            error={invalid}
            helperText={errorText ?? t('register.displayNameHelp')}
          />
        )}
      />
      <FormField
        name="email"
        required
        rules={{
          validate: (value) =>
            parseEmailAddress(value) !== null || t('register.invalidEmail'),
        }}
        render={({ field, invalid, errorText }) => (
          <TextField
            {...field}
            inputRef={emailInput}
            label={t('register.emailLabel')}
            type="email"
            autoComplete="email"
            fullWidth
            slotProps={{ input: { readOnly: autoSignInFailed } }}
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
            label={t('register.passwordLabel')}
            type="password"
            autoComplete="new-password"
            fullWidth
            slotProps={{ input: { readOnly: autoSignInFailed } }}
            error={invalid}
            helperText={errorText ?? undefined}
          />
        )}
      />
      <FormField
        name="confirmPassword"
        required
        rules={{
          validate: (value) =>
            value === form.getValues('password') ||
            t('register.passwordMismatch'),
        }}
        render={({ field, invalid, errorText }) => (
          <TextField
            {...field}
            label={t('register.confirmPasswordLabel')}
            type="password"
            autoComplete="new-password"
            fullWidth
            slotProps={{ input: { readOnly: autoSignInFailed } }}
            error={invalid}
            helperText={errorText ?? undefined}
          />
        )}
      />
    </>
  )

  return (
    <FormLayout
      ref={surfaceRef}
      form={form}
      onSubmit={guardedSubmit}
      actions={
        signedIn ? undefined : autoSignInFailed ? (
          <ActionButton
            type="button"
            emphasis="primary"
            busy={driver.busy}
            onClick={() => void startSignIn()}
          >
            {t('register.retrySignIn')}
          </ActionButton>
        ) : (
          <ActionButton
            type="submit"
            emphasis="primary"
            busy={busy}
            unavailable={throttled}
            unavailableDescribedBy={throttled ? throttleNoteId : undefined}
          >
            {t('register.submit')}
          </ActionButton>
        )
      }
    >
      {signedIn ? null : (
        <Typography variant="body2" color="text.secondary">
          {t('register.description')}
        </Typography>
      )}
      {/* Born empty and kept mounted: a live region announces only changes
          that follow its own insertion. */}
      <Alert
        severity={autoSignInFailed ? 'info' : 'success'}
        role="status"
        sx={status === null ? visuallyHiddenSx : { width: '100%' }}
      >
        {status === 'created'
          ? t('register.created')
          : status === 'signInRequired'
            ? t('register.signInRequired')
            : status === 'signedIn'
              ? t('register.signedIn')
              : ''}
      </Alert>
      {signedIn ? null : fields}
      {failure !== null ? (
        'text' in failure ? (
          <Alert severity="error" role="alert" sx={{ width: '100%' }}>
            {failure.text}
          </Alert>
        ) : (
          <Box id={throttleNoteId}>
            <InlineError
              code={failure.code}
              retryAfterSeconds={failure.retryAfterSeconds}
              liveRetryAfterSeconds={
                failure.retryAfterSeconds === undefined
                  ? undefined
                  : retryUntil === null
                    ? 0
                    : Math.max(0, Math.ceil((retryUntil - Date.now()) / 1000))
              }
            />
          </Box>
        )
      ) : null}
    </FormLayout>
  )
}
