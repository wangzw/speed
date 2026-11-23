/**
 * PasswordRegisterForm: the password channel's account-opening form.
 *
 * Registration and sign-in are two operations. A submit first opens an
 * account with `session.register` -- one request carrying the username,
 * the optional display name, the email address and the password, which
 * establishes no session and writes no credential -- and then, with the
 * same credentials, starts
 * the ordinary `method: 'password'` login flow through the shared
 * stepping driver. The registration itself fires no callback: only the
 * auto-login settling successfully reports `onSignedIn` once, with
 * `{mode: 'register', channel: 'password'}`. A parked step after the
 * password (a forced password change, a second factor) renders through
 * the shared step renderer, exactly as it does for the sign-in form.
 *
 * The confirmation field is client-only: the two passwords are compared
 * in the form and the confirmation never travels. The email field is
 * checked against the server's address rule before it is sent -- trimmed,
 * printable ASCII, exactly one @ with a part on each side, at most 254
 * bytes -- so a malformed address is refused in the field rather than
 * answered with the generic bad-request. Everything else -- username
 * normalization, the password policy, a taken email address, the pending
 * contact -- is the server's; a refusal renders its own mapped text and
 * never restates the policy's numbers. The username refusals are the
 * username field's error and a taken email address the email field's
 * (invalid, described by the message, focused), because the server refused
 * that one value; every other mapped refusal is one whole-attempt banner.
 * The three username refusals -- taken, reserved, a shape the server
 * cannot normalize -- each carry their own message under the field, and a
 * bad request that is not one of them stays a banner: it may describe the
 * password policy or the address.
 *
 * An account that was opened but whose auto-login was refused is not a
 * registration failure: the account exists, so the form must never
 * re-open it. It says the account was created and asks the viewer to
 * sign in, keeping the username in place; a host that supplies
 * `onSignInRequired` takes the username and moves the viewer to its own
 * sign-in surface. The retry action re-runs only the password login with
 * the credentials the registration used, replacing the submit in the same
 * slot so React reconciles it onto the same element and a focused control
 * keeps its focus. The fields go read-only rather than disabled: the
 * opened account must not be edited into a second one, but a viewer who
 * submitted from a field (Enter) keeps the focus there. The form's submit
 * path is closed once the account is open -- the auto-login refused or
 * settled: the submit key is gone, but the form element remains, so a
 * synthetic submit must not re-open the account.
 *
 * An auto-login that settles -- the first one or the retry -- moves the
 * form to `signedIn`: the account-created and sign-in-required notices
 * and the retry action are gone, replaced by the signed-in text, so a
 * host that does not navigate never leaves "you are signed in" beside
 * "please sign in".
 *
 * A registration refused with 429 is a throttle. When the response
 * stated a `Retry-After` wait, the submit key is unavailable until it
 * elapses: it stays focusable and carries the banner's wait sentence as
 * its description while the banner counts the seconds down. Once the wait
 * elapses the key is available again without the viewer reloading and the
 * banner stays with the wait-free sentence. A throttle that stated no
 * wait keeps the key available at once and says so through the same
 * register-section text.
 *
 * The heading above the form is host content. Nothing here navigates,
 * reads storage or issues a request outside the session's operations.
 */

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

export interface PasswordRegisterFormProps {
  /** The session the registration opens an account on and signs in. */
  readonly session: AuthSession
  /** Fired once after the auto-login commits; the host navigates. */
  readonly onSignedIn?: (context: SignInContext) => void
  /**
   * Fired after the account was opened but its auto-login was refused,
   * with the username the registration used. A host that provides it
   * moves the viewer to its own sign-in surface; a host that does not
   * gets the in-place "account created, please sign in" notice.
   */
  readonly onSignInRequired?: (username: string) => void
  /**
   * Decides whether the registration may begin. Returning false starts
   * nothing and renders no error; the auto-login after it is not gated
   * again. The context is `{method: 'password', mode: 'register'}`.
   */
  readonly beforeStart?: SignInBeforeStart
  /**
   * The human-verification gate the deployment reported, when the host
   * already read the discovery document (the sign-in screen does). Omitted,
   * the form reads it when a parked step may send.
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

/** The refusals registered here that have a dedicated text in the
 * register section; every other code -- a 400 included -- renders
 * through the family's error banner, which is where the generic
 * `errors.authn.*` vocabulary lives. */
const REFUSAL_TEXT_KEYS: Readonly<Record<string, string>> = {
  'authn.signup_closed': 'register.signupClosed',
  'authn.throttled': 'register.throttled',
  'authn.unavailable': 'register.unavailable',
}

/** The username refusals: the username field carries each with its own
 * text -- a name another account holds, one the system keeps for itself,
 * and one the server cannot normalize. */
const USERNAME_TAKEN = 'authn.username_taken'
const USERNAME_RESERVED = 'authn.username_reserved'
const USERNAME_INVALID = 'authn.username_invalid'

/** The taken-email refusal: the registration reserves the address, so an
 * address another account holds is the email field's error -- that one
 * value was refused, not the whole attempt. */
const EMAIL_TAKEN = 'authn.email_taken'

export function PasswordRegisterForm({
  session,
  onSignedIn,
  onSignInRequired,
  beforeStart,
  humanGate,
}: PasswordRegisterFormProps) {
  const { t } = useAuthUiTranslation()
  // The throttle banner's id: the submit key it holds names the wait
  // sentence as its reason, so the reason is not rendered a second time.
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
  // A mapped refusal with its own text, or any other code for the shared
  // error banner. A throttle carries the wait the answer stated, so the
  // banner can count it down.
  const [failure, setFailure] = useState<
    | { readonly text: string }
    | { readonly code: string; readonly retryAfterSeconds?: number }
    | null
  >(null)
  // The tick the throttle banner re-renders on; it is only a clock, never
  // a second source of the deadline.
  const [, setRetryTick] = useState(0)
  // `created` announces the opened account; `signInRequired` is the
  // auto-login refusal that is not a registration failure; `signedIn`
  // is a settled auto-login the host may not have navigated away from.
  const [status, setStatus] = useState<
    'created' | 'signInRequired' | 'signedIn' | null
  >(null)
  // The moment a throttled registration may be retried, when the
  // response stated one; null otherwise.
  const [retryUntil, setRetryUntil] = useState<number | null>(null)
  // Whether this form's registration attempt is running. RHF's
  // `isSubmitting` cannot carry this: it is raised before the submit
  // handler is called, and cleared as soon as the handler returns (the
  // activation guard returns synchronously by contract, so the async
  // handler's promise never reaches RHF). A guard keyed to it would
  // swallow the submission that set it and report the flight as ended.
  // This flag starts inside the handler and spans the same attempt, the
  // opening and the auto-login that follows.
  const [attempting, setAttempting] = useState(false)
  // The username input itself: RHF's setFocus targets the control the
  // Controller's ref is attached to, not MUI's inner input.
  const usernameInput = useRef<HTMLInputElement>(null)
  // The email input, focused when the server refuses a taken address.
  const emailInput = useRef<HTMLInputElement>(null)
  // Whether the current submit has opened an account: from then on a
  // password-flow failure is an auto-login refusal, not a registration
  // failure.
  const registered = useRef(false)
  // The credentials the registration used, for the retry of the login.
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
      // The account exists; the session belongs to another sign-in, so
      // the viewer is asked to sign in rather than told this one worked.
      if (registered.current) {
        setStatus('signInRequired')
      }
    },
    onSettled: () => {
      if (!registered.current) {
        return
      }
      // The session is authenticated now: the stale "please sign in"
      // notice and its retry must not stand beside it when the host
      // leaves this surface mounted.
      setStatus('signedIn')
      try {
        onSignedIn?.({ mode: 'register', channel: 'password' })
      } catch {
        // A throwing host callback is not a login failure: the login
        // committed, nothing here renders the host's error.
      }
    },
  })

  // A stated Retry-After wait counts down on its own: one tick a second
  // re-renders the banner while the deadline is ahead, and the tick that
  // crosses it clears the deadline so the submit key re-enables. The
  // failure itself stays, so the banner remains with the wait-free
  // sentence instead of vanishing.
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
    // The gate decides whether this registration begins; a refusal starts
    // nothing and renders no error (nothing left the page). The login
    // that follows the opening is part of the same attempt and is not
    // gated again.
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
        // The display name is optional; absent means the account holds
        // none, so an empty field sends no key rather than an empty string.
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

  // The credentials are never in a request body beyond the flows this
  // form drives, and the form holds them only in memory.

  const autoSignInFailed = status === 'signInRequired'
  const signedIn = status === 'signedIn'
  const busy = attempting || driver.busy
  // A stated wait is a precondition, not a flight: the submit cannot run
  // yet, but nothing this form started is still running.
  const throttled = retryUntil !== null
  // The form's own submit path judges the same state as its key, so an
  // Enter press in a field cannot start a second attempt either. An open
  // account closes the path as well, whether its auto-login was refused or
  // settled: the submit key is gone, but the form element is still in the
  // page and a synthetic submit would otherwise re-open the account. The
  // driver's own flight covers the retry action, which this form does not
  // submit.
  const guardedSubmit = useActivationGuard(onSubmit, {
    busy,
    unavailable: throttled || autoSignInFailed || signedIn,
  })

  // The registration fields and a parked step of the auto-login are two
  // surfaces of this channel; a switch between them moves focus to the
  // new one's first control (the signed-in notice is no step).
  const shownStep =
    driver.step !== null && !autoSignInFailed && !signedIn ? driver.step : null
  const surfaceRef = useStepFocus(
    shownStep !== null ? stepKey(shownStep) : signedIn ? null : 'register-fields',
  )

  // A parked step after the password (a forced password change, a second
  // factor) replaces this form's fields: only one surface renders at a
  // time. A second factor that sends a code carries the deployment's proof
  // block above it and presents the proof.
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

  // The registration fields; a settled auto-login replaces them with the
  // signed-in notice, so a mounted surface never edits registration
  // fields over an authenticated session.
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
          // The server's address rule (trimmed, printable ASCII, exactly
          // one @ with a part on each side, at most 254 bytes): a
          // malformed address is refused here rather than sent, where the
          // answer could only be the generic bad-request.
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
      {/* The form's status region stands for its whole life, born empty:
          a live region announces only changes that follow its own
          insertion, so the registration result fills a region the screen
          reader already knows. */}
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
          // The banner is the submit key's stated reason while the wait
          // holds: the key names it rather than the sentence being
          // rendered twice.
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
