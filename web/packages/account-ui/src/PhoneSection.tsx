/**
 * PhoneSection: the phone-number surface of the account page, the two steps
 * that add or replace the account's number.
 */

import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import Box from '@mui/material/Box'
import TextField from '@mui/material/TextField'
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
  AnnouncementRegion,
  errorCodeOf,
  HumanVerification,
  PhoneNumberField,
  FORM_MAX_WIDTH,
  SectionCard,
  toE164,
  useActivationGuard,
  usePhoneCountryDefault,
  useWriteFeedback,
} from '@speed/ui-kit'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'

/** The host-owned add form's callbacks: both or neither. */
type PhoneHostCallbacks =
  | {
      readonly onCancel?: undefined
      readonly onAdded?: undefined
    }
  | {
      /** Closes the host's form instead of stepping back to the number field. */
      readonly onCancel: () => void
      /** Receives the bound number in E.164 after the verification lands and
       * the profile invalidation's re-read has settled. Called only while the
       * section is mounted; a throwing callback leaves the confirmation in
       * place because the write already landed. */
      readonly onAdded: (phone: string) => void
    }

/**
 * The host supplies the session, the deployment's gate and an add form's
 * callbacks; the section sends and verifies one number and hands it back
 * once settled.
 */
export type PhoneSectionProps = {
  /** The session the human-verification proof is obtained for. */
  readonly session: AuthSession
  /** The deployment's human-verification gate, when the host already read the
   * discovery document; omitted, the section reads it itself. */
  readonly humanGate?: AuthnHumanGate
} & PhoneHostCallbacks

/** The phone-number surface: send a code, then verify it. */
export function PhoneSection({
  session,
  humanGate,
  onCancel,
  onAdded,
}: PhoneSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const startPhone = useAuthnLoginStartPhone()
  const verifyPhone = useAuthnLoginVerifyPhone()
  const defaultCountry = usePhoneCountryDefault()
  // The number a send is waiting on because a proof is not in hand yet: the
  // challenge appears only once a send is asked for.
  const [pendingSend, setPendingSend] = useState<string | null>(null)
  // The send presents the proof; the verify step sends nothing, so it does not.
  const human = useHumanVerification(session, {
    scope: 'phone',
    enabled: pendingSend !== null,
    ...(humanGate === undefined ? {} : { gate: humanGate }),
  })
  const gateDocument = humanGate ?? human.gate
  const needsProof = humanGateNeedsProof(gateDocument, 'phone')
  const proofBlocked = needsProof && human.proof === null

  const [country, setCountry] = useState(defaultCountry)
  const [nationalNumber, setNationalNumber] = useState('')
  // Shows the per-country message only once the viewer asked to send.
  const [phoneAttempted, setPhoneAttempted] = useState(false)
  const [code, setCode] = useState('')
  // The sent-to number names the phase (entering a number vs. a code).
  const [sentTo, setSentTo] = useState<string | null>(null)
  // Held until the host dismisses the section, so the number-entry form does
  // not come back beside the confirmation while the profile re-read runs.
  const [verified, setVerified] = useState(false)
  const feedback = useWriteFeedback()
  // A re-read that settles after the reader left must not fire the host
  // callback on a page it already left.
  const mountedRef = useRef(true)
  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])
  // A won challenge replays the same code.
  const gate = useStepUpGate<string>((nextCode) => {
    void verifyCode(nextCode)
  })

  // In flight through the refetches that follow, so the inputs stay
  // focusable but unchangeable.
  const busy =
    startPhone.isPending || verifyPhone.isPending || feedback.pending
  // A send waiting on its proof is still one attempt: its control stays
  // mounted and busy.
  const sendBusy = pendingSend !== null || startPhone.isPending
  const verifyBusy = verifyPhone.isPending || feedback.pending
  // The focus landing when a landed verify removes the control that started it.
  const formRef = useRef<HTMLElement | null>(null)
  const verifyButton = useRef<HTMLButtonElement | null>(null)
  const verifyLanding = useRef<HTMLButtonElement | null>(null)

  // Focus moves to the group only when the removal is what took it.
  useLayoutEffect(() => {
    const removed = verifyLanding.current
    if (removed === null) {
      return
    }
    verifyLanding.current = null
    if (sentTo !== null) {
      return
    }
    const active = document.activeElement
    const fell =
      active === null ||
      active === document.body ||
      !(active instanceof HTMLElement) ||
      !active.isConnected ||
      removed.contains(active)
    if (!fell) {
      return
    }
    formRef.current?.focus()
  }, [sentTo])

  function sendCode(nextPhone: string): Promise<void> {
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
        // The single-use proof was spent on the way: obtain a fresh one.
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
      // The sent notice is the step's own state, so the slot stays empty.
      return null
    })
  }

  const sendCodeRef = useRef(sendCode)
  sendCodeRef.current = sendCode

  /** Sends now when the gate wants no proof, else holds the send for the
   * effect to release once the proof arrives. */
  function askToSend(phone: string): void {
    if (pendingSend !== null) {
      return
    }
    feedback.reset()
    setVerified(false)
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

  // A held send leaves as soon as the gate is known and the proof is in hand.
  useEffect(() => {
    if (pendingSend === null || human.pending || gateDocument === null) {
      return
    }
    if (needsProof && human.proof === null) {
      return
    }
    if (busy) {
      return
    }
    const phone = pendingSend
    setPendingSend(null)
    void sendCodeRef.current(phone)
  }, [
    pendingSend,
    human.pending,
    gateDocument,
    needsProof,
    human.proof,
    busy,
  ])

  function verifyCode(nextCode: string): Promise<void> {
    const number = sentTo
    // Only a settled success sets it, so a step-up refusal hands nothing over.
    const landed: { number: string | null } = { number: null }
    return (async () => {
      await feedback.run(async () => {
        try {
          // No window: the verify rotates no credential. A step-up refusal
          // arms the dialog so a won challenge replays the same code.
          await verifyPhone.mutateAsync({ data: { code: nextCode } })
        } catch (error) {
          const reason = stepUpReasonOf(errorCodeOf(error))
          if (reason !== null) {
            gate.arm(nextCode, reason)
            return null
          }
          throw error
        }
        // Settle before the profile read so the slot is never blank.
        feedback.succeed(
          t('phone.added', {
            phone: number ?? '',
            interpolation: { escapeValue: false },
          }),
        )
        // Name the control that leaves, so it hands focus to the group.
        verifyLanding.current = verifyButton.current
        setVerified(true)
        setSentTo(null)
        setNationalNumber('')
        setPhoneAttempted(false)
        setCode('')
        // Refresh before the host is told; the invalidation settles once the
        // host's own re-read does. A failed re-read is contained, since the
        // write already landed.
        try {
          await queryClient.invalidateQueries(
            { queryKey: getAuthnProfileGetQueryKey() },
            { throwOnError: true },
          )
        } catch {
          // Contained on purpose: the write already landed.
        }
        landed.number = number
        return null
      })
      // A throw must not reclassify a landed write, so it is contained
      // here; an unmounted section hands nothing over.
      if (landed.number !== null && mountedRef.current) {
        try {
          onAdded?.(landed.number)
        } catch {
          // Swallowed on purpose: the write already landed. The
          // confirmation and the Close action stay on screen, so the section
          // remains recoverable.
        }
      }
    })()
  }

  const holdsSuccess = onAdded !== undefined && verified

  const guardedSubmit = useActivationGuard(
    (event: FormEvent<HTMLFormElement>) => {
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
    },
    sentTo === null
      ? { busy: sendBusy, unavailable: nationalNumber === '' || busy }
      : { busy: verifyBusy, unavailable: code.trim() === '' },
  )

  return (
    <>
      <SectionCard
        title={t('phone.title')}
        description={t('phone.securityNote')}
      >
        <Box
          // Programmatically focusable without joining the Tab order.
          ref={formRef}
          component="form"
          noValidate
          tabIndex={-1}
          aria-label={t('phone.title')}
          onSubmit={guardedSubmit}
          sx={{
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'flex-start',
            gap: 2,
            maxWidth: FORM_MAX_WIDTH,
          }}
        >
        {sentTo === null ? (
          holdsSuccess ? (
            // One Close action, so a hung profile re-read cannot trap the reader.
            onCancel === undefined ? null : (
              <ActionRow sx={{ width: '100%' }}>
                <ActionButton type="button" onClick={onCancel}>
                  {t('phone.close')}
                </ActionButton>
              </ActionRow>
            )
          ) : (
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
                readOnly={busy}
                autoComplete="tel"
              />
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
              <ActionRow sx={{ width: '100%' }}>
                {onCancel === undefined ? null : (
                  <ActionButton
                    type="button"
                    unavailable={busy}
                    onClick={onCancel}
                  >
                    {t('phone.close')}
                  </ActionButton>
                )}
                <ActionButton
                  type="submit"
                  emphasis="primary"
                  busy={sendBusy}
                  unavailable={nationalNumber === '' || busy}
                  unavailableReason={
                    nationalNumber === ''
                      ? t('phone.sendUnavailable')
                      : undefined
                  }
                >
                  {t('phone.send')}
                </ActionButton>
              </ActionRow>
            </>
          )
        ) : (
          <>
            <AnnouncementRegion
              message={t('phone.sent', {
                phone: sentTo,
                interpolation: { escapeValue: false },
              })}
              sx={{ typography: 'body2', color: 'text.secondary' }}
            />
            <TextField
              fullWidth
              autoFocus
              inputMode="numeric"
              autoComplete="one-time-code"
              label={t('phone.code.label')}
              value={code}
              slotProps={{ htmlInput: { readOnly: busy } }}
              onChange={(event) => setCode(event.target.value)}
            />
            <ActionRow sx={{ width: '100%' }}>
              <ActionButton
                type="button"
                unavailable={busy}
                onClick={() => {
                  if (onCancel !== undefined) {
                    onCancel()
                    return
                  }
                  setSentTo(null)
                  setCode('')
                  setVerified(false)
                  feedback.reset()
                }}
              >
                {onCancel === undefined ? t('phone.cancel') : t('phone.close')}
              </ActionButton>
              <ActionButton
                ref={verifyButton}
                type="submit"
                emphasis="primary"
                busy={verifyBusy}
                unavailable={code.trim() === ''}
                unavailableReason={t('phone.verifyUnavailable')}
              >
                {t('phone.verify')}
              </ActionButton>
            </ActionRow>
          </>
        )}

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
