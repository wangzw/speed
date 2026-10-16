/**
 * SMSSignInForm: the SMS-code channel of the sign-in family.
 *
 * The two steps are this form's own, over the session's `sms` login flow:
 * the phone step sends the number as the flow's start input
 * (`session.startFlow`, method `sms`, whose Begin sends the code and asks
 * for it back), then the code step answers that challenge
 * (`session.answerFlow`). The `challenge` step the server answers with is
 * what tells the form a code was issued; the sent notice announces the
 * receiving number, resend begins the flow again against the same number,
 * and changing the phone returns to the first step. A fresh code starts
 * the code field empty: a successful send (the first, a resend, or a
 * request for a changed phone) resets any code typed against the one it
 * invalidates, so a stale code can never ride along to a new code session.
 *
 * A wrong or expired code is refused by the server as `authn.login_failed`
 * (a spent login code reads exactly like a wrong one, deliberately), and
 * renders through the one InlineError banner. Busy states: the send button
 * disables for the request's flight, the code step's submit while the
 * answer is in flight (RHF's isSubmitting). A further step the flow parks
 * after the code (a required second factor) is rendered by the shared step
 * renderer; this form's own two-step surface is only the SMS method's
 * first factor. Nothing navigates; a settled login fires onSignedIn once
 * and the host decides what follows -- the callback runs only after the
 * login verdict settled, and a throwing host callback is contained (it is
 * not a login failure, never renders an error, never escapes as an
 * unhandled rejection). The heading above the form is host content.
 *
 * A code-step answer whose login lost a concurrent race (another sign-in
 * committed to the session while this answer was in flight) answers with
 * auth-core's OperationSupersededError, and the shared driver treats it as
 * the lost race it is: no failure banner, no onSignedIn. The SMS channel
 * must also hear what a lost race cost it -- a phone-login code is
 * single-use server-side, so the losing submit's own 2xx spent the very
 * code it verified even though the login never landed. The form therefore
 * never advertises that code as retryable: it renders the used-code notice,
 * clears the code field, and remembers the exact spent code -- re-submitting
 * that string is answered locally with the same notice rather than sent
 * into the server's deliberately collapsed invalid-code refusal, and only a
 * fresh code (a resend, or a request for a changed phone) can sign in. The
 * spent-code memory clears only when a fresh code is actually issued: a
 * resend the send policy refuses issues none, and the exhausted code stays
 * refused locally.
 */

import { useRef, useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import TextField from '@mui/material/TextField'
import Button from '@mui/material/Button'
import { useForm } from 'react-hook-form'
import type { SubmitHandler } from 'react-hook-form'
import { FormField, FormLayout, visuallyHiddenSx } from '@speed/ui-kit'
import type { AuthSession } from '@speed/auth-core'
import type { OpenFlowState } from './internal/flow.js'
import { useAuthUiTranslation } from './internal/translation.js'
import { InlineError } from './internal/inline-error.js'
import { FlowStepFields } from './internal/flow-step.js'
import { useFlowDriver } from './internal/use-flow-driver.js'

export interface SMSSignInFormProps {
  /** The session the SMS sign-in drives. */
  readonly session: AuthSession
  /** Fired once after an SMS-code login commits; the host navigates. */
  readonly onSignedIn?: () => void
}

interface SmsFields {
  phone: string
  code: string
}

/** The SMS method's first code prompt: state `first` with a challenge. */
function isCodePrompt(step: OpenFlowState): boolean {
  return step.state === 'first' && step.next.kind === 'challenge'
}

export function SMSSignInForm({ session, onSignedIn }: SMSSignInFormProps) {
  const { t } = useAuthUiTranslation()
  const form = useForm<SmsFields>({
    defaultValues: { phone: '', code: '' },
  })
  const [sentTo, setSentTo] = useState<string | null>(null)
  const [errorCode, setErrorCode] = useState<string | null>(null)
  // The used-code notice: the current code session's last answer lost a
  // concurrent sign-in race after its code verified server-side -- the
  // code is SPENT (see the file header) -- or re-submitted the very code
  // one did. Renders in the code step, never while an error is showing.
  const [codeUsed, setCodeUsed] = useState(false)
  // The exact code a superseded answer proved spent, kept until the next
  // code request ISSUES a fresh code (a request the server refuses issues
  // none, and this memory survives it -- the old code is no less spent),
  // at which point a re-submission of this string is refused locally.
  const [spentCode, setSpentCode] = useState<string | null>(null)
  // The code of the submit in flight: a superseded answer names the code
  // it spent through this ref, which the driver's onSuperseded reads.
  const lastCodeRef = useRef<string | null>(null)
  // The number the current code session was sent to, so resend repeats
  // against it rather than trusting a re-typed field.
  const sentToRef = useRef<string>('')

  const driver = useFlowDriver(session, {
    onFailure: setErrorCode,
    onSuperseded: () => {
      const code = lastCodeRef.current
      if (code === null) {
        return
      }
      form.resetField('code', { defaultValue: '' })
      setSpentCode(code)
      setCodeUsed(true)
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

  const requestCode = async (phone: string): Promise<void> => {
    setErrorCode(null)
    // A request does not yet open a new code session: whether one opens is
    // the server's answer -- the used-code notice clears for the attempt,
    // but the spent-code memory is released only once a fresh code was
    // actually issued (the send succeeded), because a refused request
    // leaves the previous code spent.
    setCodeUsed(false)
    sentToRef.current = phone
    const issued = await driver.start({
      method: 'sms',
      binding: 'cookie',
      input: { phone },
    })
    if (!issued) {
      return
    }
    // A fresh code was just issued: the previous session's spent-code
    // memory describes a code this send invalidates, and any code typed
    // against the one it replaces is stale too.
    setSpentCode(null)
    form.resetField('code', { defaultValue: '' })
    setSentTo(phone)
  }

  const onSubmit: SubmitHandler<SmsFields> = async (values) => {
    if (driver.step === null || !isCodePrompt(driver.step)) {
      // The phone step's submit is the code request; the code field is
      // not mounted yet, so RHF validates the phone alone.
      await requestCode(values.phone)
      return
    }
    setErrorCode(null)
    setCodeUsed(false)
    if (values.code !== '' && values.code === spentCode) {
      // The very code a superseded answer proved spent is being submitted
      // again: answer with the used-code notice and clear it -- never a
      // doomed round-trip into the server's collapsed invalid-code answer.
      form.resetField('code', { defaultValue: '' })
      setCodeUsed(true)
      return
    }
    lastCodeRef.current = values.code
    await driver.answer({ code: values.code })
  }

  const editPhone = (): void => {
    setErrorCode(null)
    // Leaving the code step clears its attempt state: the code-entry
    // surface unmounts with it, and re-entering requires a code request
    // the server accepts.
    setCodeUsed(false)
    setSpentCode(null)
    sentToRef.current = ''
    driver.reset()
  }

  const onCodeStep =
    driver.step !== null && isCodePrompt(driver.step)

  // A further step the flow parked after the code (a required second
  // factor) is the shared step renderer's, not this form's two-step
  // surface: it renders in place of the SMS form.
  if (driver.step !== null && !onCodeStep) {
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
        onCodeStep ? (
          <Button
            type="submit"
            variant="contained"
            disabled={form.formState.isSubmitting}
          >
            {t('smsSignIn.submit')}
          </Button>
        ) : (
          <Button type="submit" variant="contained" disabled={driver.busy}>
            {t('smsSignIn.sendCode')}
          </Button>
        )
      }
    >
      {/* The sent-notice live region. It must never mount together with
          its text: a role="status" region announces only content changes
          that follow its own insertion, so a notice born in the same
          commit as the region would be silent. The region therefore
          stands for the whole life of the form -- mounted empty and
          visually silent (clipped, never display:none) while nothing was
          sent -- and the accepted request fills the text into a region
          the screen reader already knows. */}
      <Alert
        severity="info"
        role="status"
        sx={onCodeStep ? { width: '100%' } : visuallyHiddenSx}
      >
        {onCodeStep ? t('smsSignIn.sentNotice', { phone: sentTo ?? '' }) : ''}
      </Alert>
      {onCodeStep ? (
        <>
          <FormField
            name="code"
            required
            render={({ field, invalid, errorText }) => (
              <TextField
                {...field}
                label={t('smsSignIn.codeLabel')}
                inputMode="numeric"
                autoComplete="one-time-code"
                fullWidth
                error={invalid}
                helperText={errorText ?? undefined}
              />
            )}
          />
          <Box sx={{ display: 'flex', gap: 1 }}>
            <Button
              size="small"
              onClick={() => void requestCode(sentToRef.current)}
              disabled={driver.busy}
            >
              {t('smsSignIn.resendCode')}
            </Button>
            <Button size="small" onClick={editPhone}>
              {t('smsSignIn.editPhone')}
            </Button>
          </Box>
        </>
      ) : (
        <FormField
          name="phone"
          required
          render={({ field, invalid, errorText }) => (
            <TextField
              {...field}
              label={t('smsSignIn.phoneLabel')}
              type="tel"
              autoComplete="tel"
              fullWidth
              error={invalid}
              helperText={errorText ?? undefined}
            />
          )}
        />
      )}
      {codeUsed ? (
        <Alert severity="error" role="alert" sx={{ width: '100%' }}>
          {t('smsSignIn.codeUsed')}
        </Alert>
      ) : (
        <InlineError code={errorCode} />
      )}
    </FormLayout>
  )
}
