/** The SMS-code channel of the sign-in family: a phone step and a code step
 * over the session's `sms` flow. */

import { useEffect, useRef, useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import TextField from '@mui/material/TextField'
import Button from '@mui/material/Button'
import Typography from '@mui/material/Typography'
import { useForm } from 'react-hook-form'
import type { SubmitHandler } from 'react-hook-form'
import {
  ActionButton,
  AnnouncementRegion,
  FormField,
  FormLayout,
  HumanVerification,
  PhoneNumberField,
  toE164,
  useActivationGuard,
  usePhoneCountryDefault,
  visuallyHiddenSx,
} from '@speed/ui-kit'
import type { AuthSession, AuthnHumanGate } from '@speed/auth-core'
import { humanGateNeedsProof, useHumanVerification } from '@speed/auth-core'
import type { OpenFlowState } from './internal/flow.js'
import { useAuthUiTranslation } from './internal/translation.js'
import {
  ChannelFeedbackRegion,
  useChannelFeedback,
} from './internal/write-feedback.js'
import { HumanGatedFlowStep, signupFieldRefusal } from './internal/flow-step.js'
import { FlowExpired } from './internal/flow-expired.js'
import { retryWaitMs } from './internal/retry-wait.js'
import { stepKey, useStepFocus } from './internal/step-focus.js'
import { useFlowDriver } from './internal/use-flow-driver.js'
import type { SignInBeforeStart } from './internal/start-gate.js'

/** The host supplies the session the SMS login drives and the callbacks
 * around it; the form owns the phone, code and resend state. */
export interface SMSSignInFormProps {
  /** The session the SMS sign-in drives. */
  readonly session: AuthSession
  /** Fired once after an SMS-code login commits; the host navigates. */
  readonly onSignedIn?: () => void
  /**
   * Decides whether the SMS flow may begin. Returning false starts nothing and
   * renders no error; each flow start the form makes, a resend included, is
   * gated.
   */
  readonly beforeStart?: SignInBeforeStart
  /**
   * The human-verification gate the deployment reported, when the host already
   * read the discovery document. Omitted, the form reads the methods document
   * itself.
   */
  readonly humanGate?: AuthnHumanGate
}

interface SmsFields {
  code: string
}

const RESEND_NOTE_ID = 'sms-sign-in-resend-note'

/** The form's one live status region (sent notice and phone refusal). */
export const SMS_NOTICE_ID = 'sms-sign-in-notice'

const LOGIN_FAILED = 'authn.login_failed'

const FLOW_INVALID = 'authn.flow_invalid'

function isCodePrompt(step: OpenFlowState): boolean {
  return step.state === 'first' && step.next.kind === 'challenge'
}

/**
 * Drives a `method: 'sms'` flow through a phone step and a code step, with
 * resend and change-phone. A superseded code is never offered as retryable,
 * because a phone-login code is single-use.
 */
export function SMSSignInForm({
  session,
  onSignedIn,
  beforeStart,
  humanGate,
}: SMSSignInFormProps) {
  const { t } = useAuthUiTranslation()
  // A send the viewer asked for, waiting on a proof. The gate is read and the
  // proof obtained only while this is set, so the steps alone show no challenge.
  const [pendingSend, setPendingSend] = useState<string | null>(null)
  const preferredCountry = usePhoneCountryDefault()
  const form = useForm<SmsFields>({
    defaultValues: { code: '' },
  })
  const [country, setCountry] = useState(() => preferredCountry)
  const [nationalNumber, setNationalNumber] = useState('')
  const [phoneAttempted, setPhoneAttempted] = useState(false)
  // Counts refused phone submits: each re-reads the control's message into the
  // status region, so a repeated refusal is announced again.
  const [phoneAttempt, setPhoneAttempt] = useState(0)
  // The shared control forwards no ref, so the field container is held here to
  // focus and read the message from.
  const phoneFieldRef = useRef<HTMLDivElement | null>(null)
  const [phoneNotice, setPhoneNotice] = useState('')
  // The number the code session went to. The sentence is rendered from it on
  // every render, so a language switch re-translates it; it is cleared before
  // a send leaves, so an identical resend is still a change.
  const [sentPhone, setSentPhone] = useState('')
  // One send at a time: the direct path and the proof-waiting path can reach
  // the send in the same tick.
  const sendingRef = useRef(false)
  // The resend control a cancelled wait hands focus back to; the cancel
  // control unmounts with the wait.
  const resendRef = useRef<HTMLButtonElement | null>(null)
  const focusResendAfterCancel = useRef(false)
  // The driver's busy flag covers a send and an answer alike, so the controls
  // need the owner: only it reports busy, its sibling reports unavailable.
  const [flightOwner, setFlightOwner] = useState<'send' | 'answer' | null>(null)
  // The SMS step's own notices are bundle copy, not codes; they share the
  // scope's one failure position, so they never stand together.
  const feedback = useChannelFeedback()
  const [localNotice, setLocalNotice] = useState<
    'refused' | 'used' | null
  >(null)
  const [resendUntil, setResendUntil] = useState(0)
  // Only a successful send's interval gets the note beside the control; a
  // failure's wait is the failure banner's own countdown.
  const [holdFromFailure, setHoldFromFailure] = useState(false)
  const [now, setNow] = useState(() => Date.now())
  // The code a superseded answer proved spent, kept until a fresh code is
  // actually issued; re-submitting it is refused locally.
  const [spentCode, setSpentCode] = useState<string | null>(null)
  // The username step's flow is gone: the terminal notice replaces it until a
  // new flow starts.
  const [signupExpired, setSignupExpired] = useState(false)
  const answeredSignupRef = useRef(false)
  const lastCodeRef = useRef<string | null>(null)
  const sentToRef = useRef<string>('')
  const answeringCodeRef = useRef(false)

  function resetScope(): void {
    setLocalNotice(null)
    feedback.reset()
  }

  function clearAttempt(): void {
    resetScope()
    if (form.formState.errors.code !== undefined) {
      form.clearErrors('code')
    }
  }

  function holdResend(waitMs: number, fromFailure: boolean): void {
    setResendUntil(waitMs > 0 ? Date.now() + waitMs : 0)
    setHoldFromFailure(fromFailure)
    setNow(Date.now())
  }

  const driver = useFlowDriver(session, {
    beforeStart,
    onFailure: (code, error) => {
      if (code === FLOW_INVALID && answeredSignupRef.current) {
        setSignupExpired(true)
        setLocalNotice(null)
        feedback.reset()
        return
      }
      const wait = retryWaitMs(error)
      if (code === LOGIN_FAILED && answeringCodeRef.current) {
        // This step's own refusal, naming no password it has no field for.
        setLocalNotice('refused')
        feedback.reset()
      } else {
        setLocalNotice(null)
        feedback.fail(code, wait)
      }
      if (wait !== undefined) {
        holdResend(wait, true)
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
        // committed.
      }
    },
  })

  const onCodeStep =
    driver.step !== null && isCodePrompt(driver.step)
  // Rendered from the number on every render, so a language switch
  // re-translates it.
  const sentText =
    sentPhone === '' ? '' : t('smsSignIn.sentNotice', { phone: sentPhone })
  // The two SMS steps read the gate and obtain a proof only once a send is
  // asked for; a host that already read the document hands the gate in.
  const human = useHumanVerification(session, {
    scope: 'phone',
    enabled: pendingSend !== null,
    ...(humanGate === undefined ? {} : { gate: humanGate }),
  })
  const gateDocument = humanGate ?? human.gate
  const needsProof = humanGateNeedsProof(gateDocument, 'phone')
  const proofBlocked = needsProof && human.proof === null

  // Mirror the control's per-country message into the status region, once per
  // refused submit; the submit cleared the region first, so a repeat is still
  // a change.
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

  const sendCode = async (phone: string): Promise<void> => {
    if (proofBlocked) {
      return
    }
    resetScope()
    setPendingSend(null)
    // Clear the previous "sent to" notice before the request leaves, so a
    // refusal never leaves an older sentence beside the new failure.
    setSentPhone('')
    answeringCodeRef.current = false
    answeredSignupRef.current = false
    sentToRef.current = phone
    setFlightOwner('send')
    try {
      // The proof is single-use: a fresh one is obtained whatever this answer is.
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
      // A fresh code was issued: release the spent-code memory and clear any
      // stale typed code.
      setSpentCode(null)
      form.resetField('code', { defaultValue: '' })
      // `sentToRef` was cleared by the send start, so even a resend producing
      // the same sentence is still a change the region announces.
      setSentPhone(sentToRef.current)
      // Read the resend interval from the parked step: this handler's state
      // copy predates the reply's render.
      const parked = driver.currentStep()
      const seconds =
        parked !== null && parked.next.kind === 'challenge'
          ? parked.next.resend_after
          : undefined
      holdResend(typeof seconds === 'number' ? seconds * 1000 : 0, false)
    } finally {
      setFlightOwner(null)
    }
  }

  /** One send at a time, whichever path asked for it. */
  const requestCode = async (phone: string): Promise<void> => {
    if (sendingRef.current) {
      return
    }
    sendingRef.current = true
    try {
      await sendCode(phone)
    } finally {
      sendingRef.current = false
    }
  }

  const requestCodeRef = useRef(requestCode)
  requestCodeRef.current = requestCode

  /**
   * Ask for a code: send now when the gate wants no proof, otherwise hold the
   * send until the effect below sees a proof.
   */
  const askToSend = (phone: string): void => {
    if (pendingSend !== null) {
      return
    }
    resetScope()
    if (
      !human.pending &&
      gateDocument !== null &&
      (!needsProof || human.proof !== null)
    ) {
      void requestCode(phone)
      return
    }
    setPendingSend(phone)
  }

  const onSubmit: SubmitHandler<SmsFields> = async (values) => {
    if (driver.step === null || !isCodePrompt(driver.step)) {
      const phone = toE164(country, nationalNumber)
      if (phone === null) {
        setPhoneAttempted(true)
        // Clear the region first so the same rejection submitted twice passes
        // through empty and announces again.
        setPhoneNotice('')
        setPhoneAttempt((value) => value + 1)
        phoneFieldRef.current
          ?.querySelector<HTMLInputElement>('input[type="tel"]')
          ?.focus()
        return
      }
      setPhoneAttempted(false)
      setPhoneNotice('')
      askToSend(phone)
      return
    }
    resetScope()
    if (values.code !== '' && values.code === spentCode) {
      // A re-submitted spent code is answered locally, never sent into the
      // server's collapsed invalid-code refusal.
      form.resetField('code', { defaultValue: '' })
      setLocalNotice('used')
      return
    }
    setPendingSend(null)
    lastCodeRef.current = values.code
    answeringCodeRef.current = true
    answeredSignupRef.current = false
    setFlightOwner('answer')
    try {
      await driver.answer(
        { code: values.code },
        needsProof ? (human.proof ?? undefined) : undefined,
      )
    } finally {
      setFlightOwner(null)
    }
  }

  // The control that started the flight reports it busy; the other reports
  // the same context as unavailable.
  const answerInFlight = flightOwner === 'answer'
  const sendInFlight = flightOwner === 'send'
  // The guard reads the driver's flight, never RHF's `isSubmitting`: RHF marks
  // the form submitting before invoking the handler, so a guard on that would
  // swallow the submit that starts the write.
  const guardedSubmit = useActivationGuard(onSubmit, {
    // A held phone-step send is this form's own action in waiting.
    busy: driver.busy || (!onCodeStep && pendingSend !== null),
    unavailable: !onCodeStep && remainingSeconds > 0,
  })
  // A successful send's hold has a note beside the control; a throttled wait is
  // the failure banner's countdown, so the control's own description names it.
  const resendHoldExplanation =
    remainingSeconds > 0
      ? holdFromFailure
        ? {
            unavailableReason: t('smsSignIn.resendIn', {
              seconds: remainingSeconds,
            }),
          }
        : { unavailableDescribedBy: RESEND_NOTE_ID }
      : {}

  const answerFurther = async (
    input: Record<string, string>,
    presented?: string,
  ): Promise<void> => {
    const current = driver.step
    resetScope()
    answeringCodeRef.current = false
    answeredSignupRef.current = current !== null && current.state === 'signup'
    await driver.answer(input, presented)
  }

  const editPhone = (): void => {
    resetScope()
    setSentPhone('')
    setPendingSend(null)
    setSignupExpired(false)
    setSpentCode(null)
    // The hold belonged to the old number's session; the new number must not
    // inherit a wait the phone step cannot explain.
    holdResend(0, false)
    sentToRef.current = ''
    driver.reset()
  }

  const restartSignIn = (): void => {
    const phone = sentToRef.current
    editPhone()
    if (phone === '') {
      return
    }
    askToSend(phone)
  }

  // A held send leaves as soon as the gate is known and its proof is in hand.
  // The `busy` guard is defensive: the code submit abandons a held resend
  // before it answers, so no held send survives into an in-flight answer.
  useEffect(() => {
    if (pendingSend === null || human.pending || gateDocument === null) {
      return
    }
    if (needsProof && human.proof === null) {
      return
    }
    if (driver.busy) {
      return
    }
    const phone = pendingSend
    setPendingSend(null)
    void requestCodeRef.current(phone)
  }, [
    pendingSend,
    human.pending,
    gateDocument,
    needsProof,
    human.proof,
    driver.busy,
  ])

  // A cancelled resend hands focus back to the control that asked. The
  // cancel control unmounts with the wait, so the transfer happens after the
  // commit that dropped it: the resend is still in its row, mounted.
  useEffect(() => {
    if (pendingSend !== null || !focusResendAfterCancel.current) {
      return
    }
    focusResendAfterCancel.current = false
    resendRef.current?.focus()
  }, [pendingSend])

  const cancelResend = (): void => {
    focusResendAfterCancel.current = true
    setPendingSend(null)
  }

  const surfaceKey = onCodeStep
    ? 'sms-code'
    : driver.step === null
      ? 'sms-phone'
      : stepKey(driver.step)
  const surfaceRef = useStepFocus(surfaceKey)
  const parkedFailure =
    feedback.outcome !== null && feedback.outcome.kind === 'failure'
      ? feedback.outcome.code
      : null
  const fieldRefusal =
    driver.step !== null && parkedFailure !== null
      ? signupFieldRefusal(driver.step, parkedFailure)
      : null

  const humanBlock = (
    <HumanVerification
      scope="phone"
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
  )

  if (signupExpired) {
    return (
      <Box ref={surfaceRef} sx={{ display: 'contents' }}>
        <FlowExpired onRestart={restartSignIn} />
      </Box>
    )
  }

  if (driver.step !== null && !onCodeStep) {
    return (
      <Box ref={surfaceRef} sx={{ display: 'contents' }}>
        <HumanGatedFlowStep
          session={session}
          humanGate={humanGate}
          step={driver.step}
          busy={driver.busy}
          onAttemptStart={resetScope}
          onAnswer={(input, proof) => void answerFurther(input, proof)}
          onUnsupported={editPhone}
          clientFailure={feedback.localFailure}
          onClientFailure={(key) =>
            key === null ? feedback.clearLocal() : feedback.failLocal(key)
          }
          failure={parkedFailure}
        />
        {fieldRefusal === null ? (
          <ChannelFeedbackRegion feedback={feedback} />
        ) : null}
      </Box>
    )
  }

  return (
    <FormLayout
      ref={surfaceRef}
      form={form}
      onSubmit={guardedSubmit}
      actions={
        onCodeStep ? (
          <ActionButton
            type="submit"
            emphasis="primary"
            busy={answerInFlight}
            // A held resend is not a write in flight, and the viewer may
            // already hold a code: this submit stays usable. Only the
            // resend's own request, once it leaves, holds it.
            unavailable={sendInFlight}
          >
            {t('smsSignIn.submit')}
          </ActionButton>
        ) : (
          <ActionButton
            type="submit"
            emphasis="primary"
            // The control that asked reports the wait on its own proof as
            // busy; it stays mounted, so it keeps the focus it had.
            busy={sendInFlight || pendingSend !== null}
            // Changing the phone while the code answer is in flight returns
            // here with that answer still running: report the context
            // unavailable, or the form guard would swallow the click silently.
            unavailable={answerInFlight || remainingSeconds > 0}
            {...(remainingSeconds > 0
              ? {
                  unavailableReason: t('smsSignIn.resendIn', {
                    seconds: remainingSeconds,
                  }),
                }
              : {})}
          >
            {t('smsSignIn.sendCode')}
          </ActionButton>
        )
      }
    >
      {/* The form's one status region. The live role and empty-first write
          belong to the shared region; the skin follows
          `data-announcement-written` so it never paints an empty box. */}
      <Alert
        severity="info"
        role="none"
        sx={
          onCodeStep && sentText !== ''
            ? {
                width: '100%',
                '&:not(:has([data-announcement-written]))': visuallyHiddenSx,
              }
            : visuallyHiddenSx
        }
      >
        <AnnouncementRegion
          id={SMS_NOTICE_ID}
          message={onCodeStep ? sentText : phoneNotice}
        />
      </Alert>
      {onCodeStep ? (
        <>
          <FormField
            name="code"
            required
            render={({ field, invalid, errorText }) => (
              <TextField
                {...field}
                onChange={(event) => {
                  field.onChange(event)
                  clearAttempt()
                }}
                label={t('smsSignIn.codeLabel')}
                inputMode="numeric"
                autoComplete="one-time-code"
                fullWidth
                error={invalid}
                helperText={errorText ?? undefined}
              />
            )}
          />
          <ChannelFeedbackRegion feedback={feedback} />
          {localNotice === 'used' ? (
            <Alert severity="error" role="alert" sx={{ width: '100%' }}>
              {t('smsSignIn.codeUsed')}
            </Alert>
          ) : localNotice === 'refused' ? (
            // The server collapses every bad code into this one answer, so the
            // text names no password this surface has no field for.
            <Alert severity="error" role="alert" sx={{ width: '100%' }}>
              {t('smsSignIn.loginFailed')}
            </Alert>
          ) : null}
          {pendingSend !== null ? (
            <>
              {humanBlock}
              <Box sx={{ display: 'flex', gap: 1 }}>
                <Button size="small" onClick={cancelResend}>
                  {t('smsSignIn.cancelResend')}
                </Button>
              </Box>
            </>
          ) : null}
          <Box sx={{ display: 'flex', gap: 1 }}>
            <ActionButton
              ref={resendRef}
              size="small"
              onClick={() => askToSend(sentToRef.current)}
              busy={sendInFlight || pendingSend !== null}
              unavailable={answerInFlight || remainingSeconds > 0}
              {...resendHoldExplanation}
            >
              {t('smsSignIn.resendCode')}
            </ActionButton>
            <Button size="small" onClick={editPhone}>
              {t('smsSignIn.editPhone')}
            </Button>
          </Box>
          {remainingSeconds > 0 && !holdFromFailure ? (
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
              onCountryChange={(next) => {
                clearAttempt()
                setPendingSend(null)
                setCountry(next)
              }}
              onNationalNumberChange={(next) => {
                clearAttempt()
                setPendingSend(null)
                setNationalNumber(next)
              }}
              countryLabel={t('smsSignIn.countryLabel')}
              numberLabel={t('smsSignIn.phoneLabel')}
              numberHelperText={t('smsSignIn.phoneHint')}
              showValidationError={phoneAttempted}
              autoComplete="tel"
            />
          </Box>
          {pendingSend !== null ? humanBlock : null}
          <ChannelFeedbackRegion feedback={feedback} />
        </>
      )}
    </FormLayout>
  )
}
