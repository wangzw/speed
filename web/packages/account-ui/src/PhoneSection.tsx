/**
 * PhoneSection: the two-step phone-number surface of the account page.
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
       * the profile invalidation's re-read has settled; a throwing callback
       * leaves the confirmation in place because the write already landed. */
      readonly onAdded: (phone: string) => void
    }

/**
 * The host supplies the session, the deployment's gate and an add form's
 * callbacks.
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
  const [pendingSend, setPendingSend] = useState<string | null>(null)
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
  const [phoneAttempted, setPhoneAttempted] = useState(false)
  const [code, setCode] = useState('')
  const [sentTo, setSentTo] = useState<string | null>(null)
  // Held until the host dismisses the section, so the number-entry form does
  // not come back beside the confirmation while the profile re-read runs.
  const [verified, setVerified] = useState(false)
  const feedback = useWriteFeedback()
  const mountedRef = useRef(true)
  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])
  const gate = useStepUpGate<string>((nextCode) => {
    void verifyCode(nextCode)
  })

  // The inputs stay focusable but unchangeable while a write runs.
  const busy =
    startPhone.isPending || verifyPhone.isPending || feedback.pending
  const sendBusy = pendingSend !== null || startPhone.isPending
  const verifyBusy = verifyPhone.isPending || feedback.pending
  const formRef = useRef<HTMLElement | null>(null)
  const verifyButton = useRef<HTMLButtonElement | null>(null)
  const verifyLanding = useRef<HTMLButtonElement | null>(null)

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
        // The proof is single-use: obtain a fresh one after a refusal.
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
      return null
    })
  }

  const sendCodeRef = useRef(sendCode)
  sendCodeRef.current = sendCode

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
    const landed: { number: string | null } = { number: null }
    return (async () => {
      await feedback.run(async () => {
        try {
          await verifyPhone.mutateAsync({ data: { code: nextCode } })
        } catch (error) {
          const reason = stepUpReasonOf(errorCodeOf(error))
          if (reason !== null) {
            gate.arm(nextCode, reason)
            return null
          }
          throw error
        }
        feedback.succeed(
          t('phone.added', {
            phone: number ?? '',
            interpolation: { escapeValue: false },
          }),
        )
        verifyLanding.current = verifyButton.current
        setVerified(true)
        setSentTo(null)
        setNationalNumber('')
        setPhoneAttempted(false)
        setCode('')
        // Refresh before the host is told; a failed re-read is contained.
        try {
          await queryClient.invalidateQueries(
            { queryKey: getAuthnProfileGetQueryKey() },
            { throwOnError: true },
          )
        } catch {
          // The write already landed.
        }
        landed.number = number
        return null
      })
      // An unmounted section hands nothing over; a throwing callback must not
      // reclassify a landed write.
      if (landed.number !== null && mountedRef.current) {
        try {
          onAdded?.(landed.number)
        } catch {
          // The write already landed; the confirmation and Close stay recoverable.
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
