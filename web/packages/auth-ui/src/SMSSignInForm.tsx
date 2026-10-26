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
 * and changing the phone returns to the first step. A resend is held for
 * the interval the server states -- the `resend_after` a challenge carried,
 * or the `Retry-After` of a 429 -- and the control states how many seconds
 * remain; the countdown timer stops at its own expiry. A fresh code starts
 * the code field empty: a successful send (the first, a resend, or a
 * request for a changed phone) resets any code typed against the one it
 * invalidates, so a stale code can never ride along to a new code session.
 *
 * The phone field is the shared two-part control: a country/region
 * selector and a local number, assembled into the E.164 string the flow
 * takes. The selector starts on the host's preferred country when it
 * provides one (ui-kit's phone-country default context), else the country
 * the browser locale names (zh-CN -> +86, en-US -> +1), and searches by
 * country name, ISO code or calling code; a pasted international number
 * switches it and fills the local part itself. The number is validated per
 * country before any
 * request: a number too short for the country is refused locally with the
 * calling code named, and never sent. That refusal moves focus to the
 * number field and carries the field's own message into the form's
 * persistent status region, so a viewer who submitted from the button
 * hears which number failed. Every refused submit clears the region before
 * writing the message again, so a repeated identical rejection is
 * announced again rather than staying silent as one unchanged string. The
 * server's refusal copy still renders for the failures it alone can judge
 * (an unsupported region, a send-policy refusal).
 *
 * A wrong or expired code is refused by the server as `authn.login_failed`
 * (a spent login code reads exactly like a wrong one, deliberately). On
 * this form's code step that refusal renders this step's own copy -- the
 * code is wrong or expired, enter it again or request a new one -- never
 * the family's generic sentence naming a password this surface has no
 * field for; a further step (a second factor) keeps the generic banner.
 * Busy states: the send button
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
 *
 * Every send -- the first code, a resend, and a parked second factor whose
 * SMS choice sends one -- presents the deployment's human-verification
 * proof when its gate requires one. The block stands above the step's
 * send control; a send consumes the single-use proof, so the block obtains
 * a fresh one for the next attempt. A host that already read the discovery
 * document passes the gate as `humanGate`; otherwise the form reads it.
 */

import { useEffect, useRef, useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import TextField from '@mui/material/TextField'
import Button from '@mui/material/Button'
import Typography from '@mui/material/Typography'
import { useForm } from 'react-hook-form'
import type { SubmitHandler } from 'react-hook-form'
import {
  FormField,
  FormLayout,
  HumanVerification,
  PhoneNumberField,
  toE164,
  usePhoneCountryDefault,
  visuallyHiddenSx,
} from '@speed/ui-kit'
import type { AuthSession, AuthnHumanGate } from '@speed/auth-core'
import { humanGateNeedsProof, useHumanVerification } from '@speed/auth-core'
import type { OpenFlowState } from './internal/flow.js'
import { answerSends } from './internal/flow.js'
import { useAuthUiTranslation } from './internal/translation.js'
import {
  ChannelFeedbackRegion,
  useChannelFeedback,
} from './internal/write-feedback.js'
import { FlowStepFields } from './internal/flow-step.js'
import { retryWaitMs } from './internal/retry-wait.js'
import { useFlowDriver } from './internal/use-flow-driver.js'
import type { SignInBeforeStart } from './internal/start-gate.js'

export interface SMSSignInFormProps {
  /** The session the SMS sign-in drives. */
  readonly session: AuthSession
  /** Fired once after an SMS-code login commits; the host navigates. */
  readonly onSignedIn?: () => void
  /**
   * Decides whether the SMS flow may begin. Returning false starts
   * nothing and renders no error; only the phone step (the flow's first
   * step) is gated, so a resend that begins a new flow is gated too.
   */
  readonly beforeStart?: SignInBeforeStart
  /**
   * The human-verification gate the deployment reported, when the host
   * already read the discovery document (the sign-in screen does). The
   * form then renders the proof block and presents the proof on every
   * send. Omitted, the form reads the methods document itself.
   */
  readonly humanGate?: AuthnHumanGate
}

interface SmsFields {
  code: string
}

/** The resend note's id, referenced by the resend control while it counts down. */
const RESEND_NOTE_ID = 'sms-sign-in-resend-note'

/** The form's one live status region (sent notice and phone refusal). */
export const SMS_NOTICE_ID = 'sms-sign-in-notice'

/** The server's collapsed credential refusal (see the file header). */
const LOGIN_FAILED = 'authn.login_failed'

/** The SMS method's first code prompt: state `first` with a challenge. */
function isCodePrompt(step: OpenFlowState): boolean {
  return step.state === 'first' && step.next.kind === 'challenge'
}

export function SMSSignInForm({
  session,
  onSignedIn,
  beforeStart,
  humanGate,
}: SMSSignInFormProps) {
  const { t } = useAuthUiTranslation()
  // The deployment's human-verification gate and this form's proof. A host
  // that already read the discovery document hands the gate in; otherwise
  // the form reads it, so a standalone form still presents a proof.
  const human = useHumanVerification(
    session,
    humanGate === undefined ? {} : { gate: humanGate },
  )
  // A send waits for a proof when the gate is known to require one. A gate
  // still being read does not block it: the phone takes longer to type
  // than the document takes to arrive, and the server judges a send that
  // races the read.
  const needsProof = humanGateNeedsProof(human.gate)
  const proofBlocked = needsProof && human.proof === null
  // The host's preferred starting country, else the browser locale's own
  // region. Read once: it is the selector's starting value, not a value
  // that follows a later provider change.
  const preferredCountry = usePhoneCountryDefault()
  const form = useForm<SmsFields>({
    defaultValues: { code: '' },
  })
  // The phone step's two parts. The country starts on the host's preferred
  // country when one is provided, else the browser locale's own region,
  // never blank; the assembled E.164 string is what the flow receives.
  const [country, setCountry] = useState(() => preferredCountry)
  const [nationalNumber, setNationalNumber] = useState('')
  // Set by a submit that could not assemble a number, so the control
  // shows its per-country message only when the viewer asked to send.
  const [phoneAttempted, setPhoneAttempted] = useState(false)
  // Counts the refused phone submits: each one re-reads the control's
  // message into the status region, so the same refusal is announced
  // again rather than tracking every later keystroke.
  const [phoneAttempt, setPhoneAttempt] = useState(0)
  // The field container a refused submit focuses and reads the message
  // from: the shared control forwards no ref of its own.
  const phoneFieldRef = useRef<HTMLDivElement | null>(null)
  // The phone step's announcement: the exact message the control renders
  // under the number, filled after the refused submit has rendered it.
  const [phoneNotice, setPhoneNotice] = useState('')
  const [sentTo, setSentTo] = useState<string | null>(null)
  // The channel's feedback scope: failure codes settle its slot, while the
  // SMS step's own two notices (this step's code refusal and the used-code
  // notice) are bundle copy rather than codes. The local notice shares the
  // scope's one failure position, so the slot and it never stand together.
  const feedback = useChannelFeedback()
  const [localNotice, setLocalNotice] = useState<
    'refused' | 'used' | null
  >(null)
  // The instant the resend control becomes usable again, and the ticking
  // clock its note reads.
  const [resendUntil, setResendUntil] = useState(0)
  const [now, setNow] = useState(() => Date.now())
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
  // Whether the answer in flight is the code step's own: only that answer
  // renders the step's specific refusal instead of the generic slot text.
  const answeringCodeRef = useRef(false)

  /** A new attempt clears every failure source of the scope. */
  function resetScope(): void {
    setLocalNotice(null)
    feedback.reset()
  }

  /** Hold the resend control for the interval the server asked for: a
   * positive `resend_after` from the challenge, or a 429's `Retry-After`. */
  function holdResend(waitMs: number): void {
    setResendUntil(waitMs > 0 ? Date.now() + waitMs : 0)
    setNow(Date.now())
  }

  const driver = useFlowDriver(session, {
    beforeStart,
    onFailure: (code, error) => {
      if (code === LOGIN_FAILED && answeringCodeRef.current) {
        // The code step's own refusal: this surface's copy names no
        // password it has no field for. It shares the scope's one failure
        // position with the slot (see the state above).
        setLocalNotice('refused')
        feedback.reset()
      } else {
        setLocalNotice(null)
        feedback.fail(code)
      }
      const wait = retryWaitMs(error)
      if (wait !== undefined) {
        holdResend(wait)
      }
    },
    onSuperseded: () => {
      const code = lastCodeRef.current
      if (code === null) {
        return
      }
      form.resetField('code', { defaultValue: '' })
      setSpentCode(code)
      setLocalNotice('used')
      feedback.reset()
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

  // After a refused submit the control has rendered its per-country
  // message under the number; mirror that same text into the status
  // region, so a viewer who cannot see the field hears what was wrong.
  // It runs per refused submit, never per keystroke. The submit cleared
  // the region first, so this write is a change even when the rejection
  // repeats the previous one.
  useEffect(() => {
    if (phoneAttempt === 0) {
      return
    }
    const input = phoneFieldRef.current?.querySelector<HTMLInputElement>(
      'input[type="tel"]',
    )
    if (input === null || input === undefined) {
      return
    }
    const describedBy = input.getAttribute('aria-describedby')
    const message =
      describedBy === null
        ? ''
        : (document.getElementById(describedBy)?.textContent ?? '')
    setPhoneNotice(message)
  }, [phoneAttempt])

  // Tick the countdown only while one is running, and stop the timer at its
  // own expiry rather than leaving it ticking for the rest of the mount.
  useEffect(() => {
    if (resendUntil <= Date.now()) {
      return undefined
    }
    const timer = setInterval(() => {
      const current = Date.now()
      setNow(current)
      if (current >= resendUntil) {
        clearInterval(timer)
      }
    }, 1000)
    return () => {
      clearInterval(timer)
    }
  }, [resendUntil])

  const remainingSeconds = Math.max(
    0,
    Math.ceil((resendUntil - now) / 1000),
  )

  const requestCode = async (phone: string): Promise<void> => {
    // A required proof that is not ready yet keeps the send from leaving:
    // the block above the control is already showing why.
    if (proofBlocked) {
      return
    }
    resetScope()
    answeringCodeRef.current = false
    // A request does not yet open a new code session: whether one opens is
    // the server's answer -- the used-code notice clears for the attempt,
    // but the spent-code memory is released only once a fresh code was
    // actually issued (the send succeeded), because a refused request
    // leaves the previous code spent.
    sentToRef.current = phone
    // The proof is single-use: the send spends it, so the control obtains
    // a fresh one for the next send whatever this answer is.
    const presented = needsProof ? human.proof : null
    const issued = await driver.start({
      method: 'sms',
      binding: 'cookie',
      input: { phone },
      ...(presented === null ? {} : { human_proof: presented }),
    })
    if (presented !== null) {
      human.consume()
    }
    if (!issued) {
      return
    }
    // A fresh code was just issued: the previous session's spent-code
    // memory describes a code this send invalidates, and any code typed
    // against the one it replaces is stale too.
    setSpentCode(null)
    form.resetField('code', { defaultValue: '' })
    setSentTo(phone)
    // The reply's challenge may state how long to wait before another code
    // may be requested. It is read from the driver's parked step, because
    // this handler's state copy predates the reply's render.
    const parked = driver.currentStep()
    const seconds =
      parked !== null && parked.next.kind === 'challenge'
        ? parked.next.resend_after
        : undefined
    holdResend(typeof seconds === 'number' ? seconds * 1000 : 0)
  }

  const onSubmit: SubmitHandler<SmsFields> = async (values) => {
    if (driver.step === null || !isCodePrompt(driver.step)) {
      // The phone step's submit is the code request: assemble the two
      // parts first, and answer a number the country rejects locally --
      // no request carries a number the viewer can still fix.
      const phone = toE164(country, nationalNumber)
      if (phone === null) {
        setPhoneAttempted(true)
        // Clear the region before the effect below fills the message in
        // again: a region whose text never changed would announce nothing,
        // so the same rejection submitted twice must pass through empty.
        setPhoneNotice('')
        setPhoneAttempt((value) => value + 1)
        // The refusal is the field's: focus it, so a viewer who submitted
        // from the button lands on the number the message is about.
        phoneFieldRef.current
          ?.querySelector<HTMLInputElement>('input[type="tel"]')
          ?.focus()
        return
      }
      setPhoneAttempted(false)
      setPhoneNotice('')
      await requestCode(phone)
      return
    }
    resetScope()
    if (values.code !== '' && values.code === spentCode) {
      // The very code a superseded answer proved spent is being submitted
      // again: answer with the used-code notice and clear it -- never a
      // doomed round-trip into the server's collapsed invalid-code answer.
      form.resetField('code', { defaultValue: '' })
      setLocalNotice('used')
      return
    }
    lastCodeRef.current = values.code
    answeringCodeRef.current = true
    // The code answer verifies rather than sends, so the proof rides along
    // only for a step that may ask for a phone next; the server ignores it
    // otherwise, and the proof stays unspent.
    await driver.answer(
      { code: values.code },
      needsProof ? (human.proof ?? undefined) : undefined,
    )
  }

  /** Answer a further step the flow parked (a second factor, an
   * enrollment). The proof rides with it, and is consumed when the answer
   * may have sent a message. */
  const answerFurther = async (
    input: Record<string, string>,
  ): Promise<void> => {
    const current = driver.step
    const presented = needsProof ? human.proof : null
    const sends = current !== null && answerSends(current, input)
    // A further step's answer is a new attempt too: the previous refusal's
    // banner goes before the reply lands.
    resetScope()
    answeringCodeRef.current = false
    await driver.answer(input, presented ?? undefined)
    if (sends && presented !== null) {
      human.consume()
    }
  }

  const editPhone = (): void => {
    resetScope()
    // Leaving the code step clears its attempt state: the code-entry
    // surface unmounts with it, and re-entering requires a code request
    // the server accepts.
    setSpentCode(null)
    sentToRef.current = ''
    driver.reset()
  }

  const onCodeStep =
    driver.step !== null && isCodePrompt(driver.step)

  // The deployment's human-verification block, above whichever send
  // control the current step shows. It renders nothing when no proof is
  // required.
  const humanBlock = (
    <HumanVerification
      gate={human.gate ?? undefined}
      proof={human.proof}
      status={human.status}
      onToken={human.adoptToken}
      onRetry={human.retry}
      disabled={driver.busy}
    />
  )

  // A further step the flow parked after the code (a required second
  // factor) is the shared step renderer's, not this form's two-step
  // surface: it renders in place of the SMS form.
  if (driver.step !== null && !onCodeStep) {
    return (
      <>
        {humanBlock}
        <FlowStepFields
          step={driver.step}
          busy={driver.busy}
          onAnswer={(input) => void answerFurther(input)}
          clientFailure={feedback.localFailure}
          onClientFailure={(key) =>
            key === null ? feedback.clearLocal() : feedback.failLocal(key)
          }
        />
        <ChannelFeedbackRegion feedback={feedback} />
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
          <Button
            type="submit"
            variant="contained"
            disabled={driver.busy || proofBlocked}
          >
            {t('smsSignIn.sendCode')}
          </Button>
        )
      }
    >
      {/* The form's one status region: the sent notice on the code step,
          the number field's refusal on the phone step. It must never
          mount together with its text: a role="status" region announces
          only content changes that follow its own insertion, so a notice
          born in the same commit as the region would be silent. The
          region therefore stands for the whole life of the form --
          mounted empty and visually silent (clipped, never display:none)
          while nothing is announced -- and each accepted notice fills
          the text into a region the screen reader already knows. */}
      <Alert
        id={SMS_NOTICE_ID}
        severity="info"
        role="status"
        sx={onCodeStep ? { width: '100%' } : visuallyHiddenSx}
      >
        {onCodeStep
          ? t('smsSignIn.sentNotice', { phone: sentTo ?? '' })
          : phoneNotice}
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
          {/* The resend action starts a fresh flow that sends another
              code, so it needs its own proof; the block stays on this step
              for it. */}
          {humanBlock}
          <Box sx={{ display: 'flex', gap: 1 }}>
            <Button
              size="small"
              onClick={() => void requestCode(sentToRef.current)}
              disabled={driver.busy || proofBlocked || remainingSeconds > 0}
              aria-describedby={
                remainingSeconds > 0 ? RESEND_NOTE_ID : undefined
              }
            >
              {t('smsSignIn.resendCode')}
            </Button>
            <Button size="small" onClick={editPhone}>
              {t('smsSignIn.editPhone')}
            </Button>
          </Box>
          {/* The server's stated wait, filled while the control is held and
              removed when it ends. */}
          {remainingSeconds > 0 ? (
            <Typography
              id={RESEND_NOTE_ID}
              variant="body2"
              color="text.secondary"
            >
              {t('smsSignIn.resendIn', { seconds: remainingSeconds })}
            </Typography>
          ) : null}
        </>
      ) : (
        <>
          <Box ref={phoneFieldRef} sx={{ width: '100%' }}>
            <PhoneNumberField
              country={country}
              nationalNumber={nationalNumber}
              onCountryChange={setCountry}
              onNationalNumberChange={setNationalNumber}
              countryLabel={t('smsSignIn.countryLabel')}
              numberLabel={t('smsSignIn.phoneLabel')}
              numberHelperText={t('smsSignIn.phoneHint')}
              showValidationError={phoneAttempted}
              autoComplete="tel"
            />
          </Box>
          {humanBlock}
        </>
      )}
      <ChannelFeedbackRegion feedback={feedback} />
      {localNotice === 'used' ? (
        <Alert severity="error" role="alert" sx={{ width: '100%', mt: 1 }}>
          {t('smsSignIn.codeUsed')}
        </Alert>
      ) : localNotice === 'refused' ? (
        // The code step's own refusal: the server collapses a wrong,
        // expired, spent or exhausted code into this one answer, so the
        // text says what this step can promise -- try it again or ask
        // for a new code -- and never names a password this surface has
        // no field for.
        <Alert severity="error" role="alert" sx={{ width: '100%', mt: 1 }}>
          {t('smsSignIn.loginFailed')}
        </Alert>
      ) : null}
    </FormLayout>
  )
}
