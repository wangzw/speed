/**
 * PhoneSection: the phone-number surface of the account page, the two
 * steps that add or replace the account's number.
 *
 * The write is the login module's account self-service pair:
 * POST /api/v1/auth/phone (authn.login.startPhone) sends a code to the number and
 * answers how long it lives and how soon another may be asked for without
 * saying whether the number belongs to another account; POST
 * /api/v1/auth/phone/verify (authn.login.verifyPhone) submits the code, binds the
 * number to the account as a verified identifier (replacing any phone it
 * already held) and revokes the account's other sessions. The verification
 * replaces this session's credential, so it runs inside an
 * identity-transition window (session.beginIdentityTransition): a read
 * that lands with the outgoing credential and answers anonymous is
 * discarded instead of ending the session, and the window closes once the
 * refetch that follows the write has settled. The number is
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
 * code named. Each step's request can
 * answer 401 authn.insufficient_user_authentication (the route asks for a
 * fresh authentication); the section opens the step-up dialog with that
 * step's inputs remembered and a won challenge re-runs exactly that step.
 * Every other refusal resolves through the code whitelist into the
 * section's write-feedback region beneath the controls (a number another
 * account holds is the 409 conflict; a wrong code is the 401 login-failed).
 * A verified number refetches the profile so the identifiers surface shows
 * it, and the region confirms with a message naming the number. The "code
 * sent" notice is the code step's own state, not a settled action, so it
 * stays outside the outcome slot: it is its own role="status" region, mounted
 * empty and filled when the code lands, and focus moves to the code field.
 * The slot holds at most one result, so a success and a failure never stand
 * beside each other.
 *
 * The section takes the session as its only prop -- the step-up dialog
 * drives it -- while the two writes travel on the shared access token
 * through the generated calls.
 *
 * The send also presents the deployment's human-verification proof when
 * its gate requires one. The gate comes from the methods document (or
 * from a `humanGate` a host already read), the block stands above the
 * send control, and the control stays disabled until a proof is ready.
 * The proof is single-use, so every attempt -- a refusal included -- is
 * followed by a fresh one. The verify step sends nothing and presents
 * none.
 */

import { useEffect, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Stack from '@mui/material/Stack'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnProfileGetQueryKey,
  getAuthnSessionListQueryKey,
  useAuthnLoginStartPhone,
  useAuthnLoginVerifyPhone,
} from '@speed/api-sdk'
import type { AuthSession, AuthnHumanGate } from '@speed/auth-core'
import { humanGateNeedsProof, useStepUpGate, useHumanVerification } from '@speed/auth-core'
import {
  HumanVerification,
  PhoneNumberField,
  FORM_MAX_WIDTH,
  SectionCard,
  toE164,
  usePhoneCountryDefault,
  useWriteFeedback,
} from '@speed/ui-kit'
import { runIdentityTransition } from './internal/identity-transition.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'

export interface PhoneSectionProps {
  /** The session whose step-up flow the dialog drives. */
  readonly session: AuthSession
  /**
   * The human-verification gate the deployment reported, when the host
   * already read the discovery document. Omitted, the section reads the
   * methods document itself.
   */
  readonly humanGate?: AuthnHumanGate
}

/** Which step the request came from, so a won step-up re-runs exactly it. */
type PhoneAction =
  | { readonly kind: 'start'; readonly phone: string }
  | { readonly kind: 'verify'; readonly code: string }

export function PhoneSection({ session, humanGate }: PhoneSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const startPhone = useAuthnLoginStartPhone()
  const verifyPhone = useAuthnLoginVerifyPhone()
  // The host's preferred starting country when one is provided, otherwise
  // the browser-locale answer (ui-kit's context does the fallback).
  const defaultCountry = usePhoneCountryDefault()
  // The deployment's human-verification gate and this section's proof. The
  // send presents it; the verify step sends nothing, so it does not.
  const human = useHumanVerification(
    session,
    humanGate === undefined ? {} : { gate: humanGate },
  )
  const needsProof = humanGateNeedsProof(human.gate)
  // Only a known gate that requires a proof blocks the send. A gate still
  // being read does not: the form has long been filled by then, and the
  // server judges the send if the document arrives late.
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

  const gate = useStepUpGate<PhoneAction>((action) => {
    if (action.kind === 'start') {
      void sendCode(action.phone)
    } else {
      void verifyCode(action.code)
    }
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
    // A required proof that is not ready keeps the send from leaving. A gate
    // still being read does not: the server judges a send that races the
    // read.
    if (proofBlocked) {
      return Promise.resolve()
    }
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
        if (gate.intercept(error, { kind: 'start', phone: nextPhone })) {
          // A step-up refusal is decided before the proof is checked, so
          // the proof is still valid for the re-run.
          return null
        }
        // Any other refusal reached the send's own checks, and the
        // single-use proof was spent on the way: obtain a fresh one.
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

  function verifyCode(nextCode: string): Promise<void> {
    // The step shows the verify controls only after a send, so the number
    // the code was sent to is the one this attempt names on success.
    const number = sentTo
    return feedback.run(async () => {
      try {
        // The verify replaces the account's phone and rotates this
        // session's credential, so the window is open before the write and
        // closes once the refetch that follows it has settled: a read still
        // carrying the outgoing credential is discarded instead of ending
        // the session.
        await runIdentityTransition(
          session,
          () => verifyPhone.mutateAsync({ data: { code: nextCode } }),
          async () => {
            setSentTo(null)
            setNationalNumber('')
            setPhoneAttempted(false)
            setCode('')
            // The verified number joins the profile's identifiers, and the
            // route revokes the account's other sessions.
            await Promise.all([
              queryClient.invalidateQueries({
                queryKey: getAuthnProfileGetQueryKey(),
              }),
              queryClient.invalidateQueries({
                queryKey: getAuthnSessionListQueryKey(),
              }),
            ])
          },
        )
      } catch (error) {
        if (gate.intercept(error, { kind: 'verify', code: nextCode })) {
          return null
        }
        throw error
      }
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
              void sendCode(phone)
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
              onCountryChange={setCountry}
              onNationalNumberChange={setNationalNumber}
              countryLabel={t('phone.country.label')}
              numberLabel={t('phone.phone.label')}
              numberHelperText={t('phone.phone.helper')}
              showValidationError={phoneAttempted}
              disabled={busy}
              autoComplete="tel"
            />
            <HumanVerification
              gate={human.gate ?? undefined}
              proof={human.proof}
              status={human.status}
              onToken={human.adoptToken}
              onRetry={human.retry}
              disabled={busy}
            />
            <Button
              type="submit"
              variant="contained"
              disabled={busy || proofBlocked || nationalNumber === ''}
            >
              {t('phone.send')}
            </Button>
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
            <Stack direction="row" spacing={1}>
              <Button
                type="submit"
                variant="contained"
                disabled={busy || code.trim() === ''}
              >
                {t('phone.verify')}
              </Button>
              <Button
                type="button"
                disabled={busy}
                onClick={() => {
                  setSentTo(null)
                  setCode('')
                  feedback.reset()
                }}
              >
                {t('phone.cancel')}
              </Button>
            </Stack>
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
