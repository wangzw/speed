/**
 * PhoneSection: the phone-number surface of the account page, the two
 * steps that add or replace the account's number.
 *
 * The write is the login module's account self-service pair:
 * POST /api/v1/auth/phone (authn.login.startPhone) sends a code to the number and
 * answers how long it lives and how soon another may be asked for without
 * saying whether the number belongs to another account; POST
 * /api/v1/auth/phone/verify (authn.login.verifyPhone) submits the code and
 * binds the number to the account as a verified identifier (replacing any
 * phone it already held). Registering a number sends a code without asking
 * for a fresh authentication. Proving it does not make it a way to sign in,
 * so the verification rotates nothing and refetches the profile alone,
 * running outside the identity-transition window; but when the number
 * replaces the one an active SMS factor sends codes to, the server may answer
 * a step-up refusal, and the verify step then arms the dialog with the same
 * code and replays it once a challenge is won. The number is
 * taken from the first step's record, keyed to the session, so the second
 * step carries the code alone -- the client never chooses which number is
 * verified.
 *
 * The section is the two steps in sequence: the shared two-part phone
 * control (country/region selector plus local number, assembled into the
 * E.164 string the request takes) and a send action, then -- once a code
 * has been sent -- a code field and a verify action, with a way back to a
 * different number. The selector starts on the browser locale's own
 * region and validates the number per country before anything is sent, so
 * a number too short for its country is refused locally with the calling
 * code named. A refusal resolves through the code whitelist into the
 * section's write-feedback region beneath the controls (a number another
 * account holds is the 409 conflict; a wrong code is the 401 login-failed).
 * A throttled refusal's banner names the wait the answer stated and counts
 * it down, so the section renders no separate wait note.
 * A verified number refetches the profile so the identifiers surface shows
 * it, and the region confirms with a message naming the number. The "code
 * sent" notice is the code step's own state, not a settled action, so it
 * stays outside the outcome slot: it is its own role="status" region, mounted
 * empty and filled when the code lands, and focus moves to the code field.
 * The slot holds at most one result, so a success and a failure never stand
 * beside each other.
 *
 * The section takes the session for the human-verification block and the
 * step-up dialog the verify step can open, while the two writes travel on the
 * shared access token through the generated calls.
 *
 * The send also presents the deployment's human-verification proof when
 * its gate requires one. The gate comes from the methods document (or
 * from a `humanGate` a host already read). The block appears only once a
 * send has been asked for: entering a number shows no challenge, and the
 * send the viewer asked for leaves by itself when the proof arrives. The
 * control that asked gives way to the block while the proof is
 * outstanding, so one action is on screen at a time. The proof is
 * single-use, so every attempt -- a refusal included -- is followed by a
 * fresh one. The verify step sends nothing and presents none.
 */

import { useEffect, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnProfileGetQueryKey,
  useAuthnLoginStartPhone,
  useAuthnLoginVerifyPhone,
} from '@speed/api-sdk'
import type { AuthSession, AuthnHumanGate } from '@speed/auth-core'
import {
  humanGateNeedsProof,
  stepUpReasonOf,
  useHumanVerification,
  useStepUpGate,
} from '@speed/auth-core'
import {
  ActionButton,
  ActionRow,
  errorCodeOf,
  HumanVerification,
  PhoneNumberField,
  FORM_MAX_WIDTH,
  SectionCard,
  toE164,
  usePhoneCountryDefault,
  useWriteFeedback,
} from '@speed/ui-kit'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'

export interface PhoneSectionProps {
  /** The session the human-verification proof is obtained for. */
  readonly session: AuthSession
  /**
   * The human-verification gate the deployment reported, when the host
   * already read the discovery document. Omitted, the section reads the
   * methods document itself.
   */
  readonly humanGate?: AuthnHumanGate
  /**
   * Closes the host's form. Given, every step's cancel leaves the section
   * instead of only stepping back to the number field.
   */
  readonly onCancel?: () => void
}

export function PhoneSection({
  session,
  humanGate,
  onCancel,
}: PhoneSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const startPhone = useAuthnLoginStartPhone()
  const verifyPhone = useAuthnLoginVerifyPhone()
  // The host's preferred starting country when one is provided, otherwise
  // the browser-locale answer (ui-kit's context does the fallback).
  const defaultCountry = usePhoneCountryDefault()
  // The number a send the viewer asked for is waiting on, because the gate
  // wants a proof this client does not hold yet. The gate is read and the
  // proof obtained only while this is set, so the number step shows no
  // challenge until its send is asked for. Once the proof arrives the send
  // the viewer already asked for leaves by itself.
  const [pendingSend, setPendingSend] = useState<string | null>(null)
  // The deployment's human-verification gate and this section's proof. The
  // send presents it; the verify step sends nothing, so it does not.
  const human = useHumanVerification(session, {
    scope: 'phone',
    enabled: pendingSend !== null,
    ...(humanGate === undefined ? {} : { gate: humanGate }),
  })
  // A host that already read the discovery document hands the gate in; a
  // standalone section learns it through the hook once a send asks for one.
  const gateDocument = humanGate ?? human.gate
  const needsProof = humanGateNeedsProof(gateDocument, 'phone')
  const proofBlocked = needsProof && human.proof === null

  const [country, setCountry] = useState(defaultCountry)
  const [nationalNumber, setNationalNumber] = useState('')
  // Set by a submit that could not assemble a number, so the control
  // shows its per-country message only when the viewer asked to send.
  const [phoneAttempted, setPhoneAttempted] = useState(false)
  const [code, setCode] = useState('')
  // The number a code was sent to, or null while the number is being
  // entered -- the phase's own state (entering a number vs. entering a
  // code) and the value the sent notice names.
  const [sentTo, setSentTo] = useState<string | null>(null)
  // The sent notice's live region must never mount together with its text:
  // it fills one commit after the code step mounts, so the announcement is a
  // content change the screen reader already knows.
  const [sentCommitted, setSentCommitted] = useState(false)
  const feedback = useWriteFeedback()
  // The verify step's step-up gate: a proof that replaces the number an
  // active SMS factor sends codes to is refused until the session proves a
  // fresh authentication, and a won challenge replays the same code.
  const gate = useStepUpGate<string>((nextCode) => {
    void verifyCode(nextCode)
  })

  // Disabled through the whole attempt, including the refetches that follow
  // the mutation, so the controls cannot change context under a pending
  // scope.
  const busy =
    startPhone.isPending || verifyPhone.isPending || feedback.pending

  // Arm the sent notice's live region one commit after the code step mounts,
  // so the notice fills an already-mounted region rather than arriving with
  // it. The region is empty again when the viewer returns to the number.
  useEffect(() => {
    setSentCommitted(sentTo !== null)
  }, [sentTo])

  function sendCode(nextPhone: string): Promise<void> {
    // A required proof that is not ready yet keeps the send from leaving:
    // the block above the control is already showing why.
    if (proofBlocked) {
      return Promise.resolve()
    }
    setPendingSend(null)
    return feedback.run(async () => {
      const presented = needsProof ? human.proof : null
      try {
        await startPhone.mutateAsync({
          data:
            presented === null
              ? { phone: nextPhone }
              : { phone: nextPhone, human_proof: presented },
        })
      } catch (error) {
        // The refusal reached the send's own checks, and the single-use
        // proof was spent on the way: obtain a fresh one.
        if (presented !== null) {
          human.consume()
        }
        throw error
      }
      if (presented !== null) {
        human.consume()
      }
      setSentTo(nextPhone)
      setCode('')
      // The "code sent" notice is the step's own state: the intermediate
      // answer is not a settled action and leaves the slot empty.
      return null
    })
  }

  const sendCodeRef = useRef(sendCode)
  sendCodeRef.current = sendCode

  /**
   * Ask for a code: send now when the gate is known to want no proof,
   * otherwise hold the send and let the block obtain one; the effect that
   * watches the postponed send sends when it arrives.
   */
  function askToSend(phone: string): void {
    if (pendingSend !== null) {
      // A send is already waiting on its proof; asking again changes nothing.
      return
    }
    // The attempt begins here, whichever path it takes: the previous
    // result must not stand beside the verification the new one renders.
    feedback.reset()
    if (
      !human.pending &&
      gateDocument !== null &&
      (!needsProof || human.proof !== null)
    ) {
      void sendCode(phone)
      return
    }
    setPendingSend(phone)
  }

  // A send asked for while the gate held it leaves as soon as the gate is
  // known and the proof it wants is in hand: the viewer's one request is
  // not turned into a click followed by a second one. A gate still being
  // read keeps the send waiting; a gate that wants nothing sends at once.
  useEffect(() => {
    if (pendingSend === null || human.pending || gateDocument === null) {
      return
    }
    if (needsProof && human.proof === null) {
      return
    }
    const phone = pendingSend
    setPendingSend(null)
    void sendCodeRef.current(phone)
  }, [pendingSend, human.pending, gateDocument, needsProof, human.proof])

  function verifyCode(nextCode: string): Promise<void> {
    // The step shows the verify controls only after a send, so the number
    // the code was sent to is the one this attempt names on success.
    const number = sentTo
    return feedback.run(async () => {
      try {
        // The verify binds the number without rotating this session's
        // credential or revoking the others, so no window is opened. A number
        // that would replace the one an active SMS factor uses is refused
        // until the session proves a fresh authentication: arm the dialog with
        // the same code so a won challenge replays it.
        await verifyPhone.mutateAsync({ data: { code: nextCode } })
      } catch (error) {
        const reason = stepUpReasonOf(errorCodeOf(error))
        if (reason !== null) {
          gate.arm(nextCode, reason)
          return null
        }
        throw error
      }
      setSentTo(null)
      setNationalNumber('')
      setPhoneAttempted(false)
      setCode('')
      await queryClient.invalidateQueries({
        queryKey: getAuthnProfileGetQueryKey(),
      })
      return t('phone.added', {
        phone: number ?? '',
        interpolation: { escapeValue: false },
      })
    })
  }

  return (
    <>
      <SectionCard
        title={t('phone.title')}
        description={t('phone.securityNote')}
      >
        <Box
          component="form"
          noValidate
          onSubmit={(event) => {
            event.preventDefault()
            if (sentTo === null) {
              const phone = toE164(country, nationalNumber)
              if (phone === null) {
                setPhoneAttempted(true)
                return
              }
              setPhoneAttempted(false)
              askToSend(phone)
            } else {
              void verifyCode(code)
            }
          }}
          sx={{
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'flex-start',
            gap: 2,
            maxWidth: FORM_MAX_WIDTH,
          }}
        >
        {sentTo === null ? (
          <>
            <PhoneNumberField
              country={country}
              nationalNumber={nationalNumber}
              onCountryChange={(next) => {
                setPendingSend(null)
                setCountry(next)
              }}
              onNationalNumberChange={(next) => {
                setPendingSend(null)
                setNationalNumber(next)
              }}
              countryLabel={t('phone.country.label')}
              numberLabel={t('phone.phone.label')}
              numberHelperText={t('phone.phone.helper')}
              showValidationError={phoneAttempted}
              disabled={busy}
              autoComplete="tel"
            />
            {/* The first send needs a proof too, so the block appears only
                after the viewer asks for that send and no proof is in hand. */}
            {pendingSend !== null ? (
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
                disabled={busy}
              />
            ) : null}
            {onCancel === undefined && pendingSend !== null ? null : (
              <ActionRow sx={{ width: '100%' }}>
                {onCancel === undefined ? null : (
                  <ActionButton type="button" disabled={busy} onClick={onCancel}>
                    {t('phone.close')}
                  </ActionButton>
                )}
                {/* The control that asked gives way to the block while the
                    proof is outstanding, so one action is on screen at a
                    time. */}
                {pendingSend === null ? (
                  <ActionButton
                    type="submit"
                    emphasis="primary"
                    disabled={busy || nationalNumber === ''}
                  >
                    {t('phone.send')}
                  </ActionButton>
                ) : null}
              </ActionRow>
            )}
          </>
        ) : (
          <>
            {/* The destination text stays through the step; being the live
                region itself, it announces the code's landing once (empty,
                then filled one commit later). */}
            <Typography
              component="div"
              role="status"
              variant="body2"
              color="text.secondary"
            >
              {sentCommitted
                ? t('phone.sent', {
                    phone: sentTo,
                    interpolation: { escapeValue: false },
                  })
                : ''}
            </Typography>
            <TextField
              fullWidth
              autoFocus
              inputMode="numeric"
              autoComplete="one-time-code"
              label={t('phone.code.label')}
              value={code}
              disabled={busy}
              onChange={(event) => setCode(event.target.value)}
            />
            <ActionRow sx={{ width: '100%' }}>
              <ActionButton
                type="button"
                disabled={busy}
                onClick={() => {
                  if (onCancel !== undefined) {
                    onCancel()
                    return
                  }
                  setSentTo(null)
                  setCode('')
                  feedback.reset()
                }}
              >
                {onCancel === undefined ? t('phone.cancel') : t('phone.close')}
              </ActionButton>
              <ActionButton
                type="submit"
                emphasis="primary"
                disabled={busy || code.trim() === ''}
              >
                {t('phone.verify')}
              </ActionButton>
            </ActionRow>
          </>
        )}

        {/* One scope's result, below the controls of the active step. */}
        <WriteFeedbackRegion
          outcome={feedback.outcome}
          sx={{ alignSelf: 'stretch', width: '100%' }}
        />
        </Box>
      </SectionCard>

      <StepUpChallenge
        open={gate.open}
        reason={gate.reason}
        session={session}
        onSuccess={gate.succeed}
        onCancel={gate.cancel}
      />
    </>
  )
}
