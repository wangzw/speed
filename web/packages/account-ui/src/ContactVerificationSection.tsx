/**
 * ContactVerificationSection: the account's email-verification surface.
 *
 * An account with an email address but no proved email address reads as
 * `pending` on GET /api/v1/auth/me's `verification` until an address is
 * proved, and the server keeps the account's restricted capabilities closed
 * until then -- however the address arrived (self-registration, administrator
 * provisioning or an import). This section is that state's face: it reads the
 * same profile query the other account surfaces read and renders one of four
 * states.
 *
 *   - `none` (an account with no unproved email address) renders nothing.
 *   - `verified` renders the completion state, naming the proved address.
 *   - `pending` with an address renders the target address, a send/resend
 *     action, the code field and the change-address action.
 *   - `pending` with no address renders the add-email form. An account can
 *     reach `pending` with no target after its last proved address is
 *     removed; the section then offers the only path that lifts the
 *     restriction again, so the add cannot live only on the identifiers
 *     section.
 *
 * Sending a code (and changing the target) is POST /api/v1/auth/identifiers
 * with `{kind: 'email', value}`, which answers `pending` and the resend
 * interval; verification is POST /api/v1/auth/identifiers/verify with the code
 * alone (the address is the server's session-keyed record). A landed verify
 * refetches the profile so `verification` flips to `verified`. Registering a
 * contact address sends a code without rotating this session's credential.
 * Proving it neither rotates the credential nor revokes the account's other
 * sessions and runs outside the identity-transition window; but when the
 * proof makes the address the account's default contact or its only address,
 * the server may answer a step-up refusal, and the verify step then arms the
 * dialog with the same code and replays it once a challenge is won.
 *
 * The section never distinguishes a free address from one another account
 * holds while sending: the answer is the same shape, and no "already
 * registered" copy exists. A conflict is rendered only after a code proves
 * control of the address, and its text names the address as another
 * account's and points at the change action.
 *
 * A host that lands this section from a verification link sets `autoSend`:
 * once per mount, when the account is `pending` with a target address and the
 * change-address form is closed, the section sends the code itself so the
 * visitor arrives with one already on its way. The demo host mounts the
 * section on the account page and sets the flag only for a `?verify=1`
 * landing, so an ordinary account visit sends nothing. A mount that
 * lands with the form already open (`initialChangeOpen`, the
 * `?change=email` entry) never sends automatically, even after the form is
 * closed: that visitor arrived to replace the address, and a code for the
 * old one is not what they asked for. A 401 of the automatic send leaves the
 * manual send action in place with an explanation; the visitor's click then
 * retries the send like any other. A 429 still holds the resend control for
 * the stated wait, and its banner names that wait, and any other failure
 * renders the failure banner.
 *
 * Registering a contact address asks for no fresh authentication, so a 401
 * authn.insufficient_user_authentication of the send renders through the
 * family's one role="alert" banner like any other failure instead of opening
 * a step-up dialog. The verify may be refused when its proof changes how the
 * account signs in, and then opens the dialog and replays the same code. A
 * resend control is disabled
 * for the interval the server asked for (or the Retry-After a 429 carried),
 * and the interval's timer stops once it expires. A hold a successful send's
 * `resend_after` started states the wait in the section's own note, because
 * that send leaves no banner; a hold a throttled failure started gets no
 * note, because the failure's banner names that wait and counts it down. The
 * "code sent" notice is announced once through a role="status" region that
 * is mounted empty and filled on landing, and focus moves to the code field.
 *
 * A resend addresses the same target the section displays (the address a send
 * in this mount landed on, else the profile's pending contact), so a resend
 * never goes to the address a change just replaced.
 *
 * The host can land the section with the change-address entry already open
 * (`initialChangeOpen`), which is what the banner's change action asks for;
 * its verify action lands with the entry closed and the code step showing.
 *
 * The section takes the session for the human-verification block and the
 * step-up dialog the verify step can open, while the reads and writes travel
 * on the shared access token through the generated calls.
 *
 * A send also presents the deployment's human-verification proof when its
 * gate requires one. The gate comes from the methods document (or from a
 * `humanGate` a host already read). The block appears only once a send has
 * been asked for: a step showing its send controls shows no challenge, and
 * the send the viewer asked for leaves by itself when the proof arrives.
 * The control that asked stays mounted and busy while the proof is
 * outstanding, and the block appears beside it. A resend obtains a fresh
 * proof because the previous one was spent. The automatic landing send has
 * no visitor's click of its own, so it takes the same awaited path as one
 * and leaves as soon as the block holds a proof; a refused automatic send
 * stays a manual action.
 */

import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type FormEvent,
} from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnProfileGetQueryKey,
  useAuthnLoginAddIdentifier,
  useAuthnLoginVerifyIdentifier,
} from '@speed/api-sdk'
import type { AuthSession, AuthnHumanGate } from '@speed/auth-core'
import { humanGateNeedsProof, stepUpReasonOf, useHumanVerification, useStepUpGate } from '@speed/auth-core'
import {
  ActionButton,
  ActionRow,
  errorCodeOf,
  FORM_MAX_WIDTH,
  HumanVerification,
  SectionCard,
  useActivationGuard,
  useWriteFeedback,
  visuallyHiddenSx,
  type ActionButtonUnavailableExplanation,
} from '@speed/ui-kit'
import { AccountAsyncSection } from './internal/async-section.js'
import { useProfileQuery } from './internal/queries.js'
import { retryAfterMsOf } from './internal/retry-after.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'

/** The resend note's id, referenced by the resend control while it counts
 * down. */
const RESEND_NOTE_ID = 'contact-verification-resend-note'

/** The control that started the write now in flight. The scope's `pending`
 * says a write runs; this says whose, so only that control reports busy and
 * every other initiating control in the step reports the shared scope as
 * unavailable. */
type ContactWriteAction = 'add' | 'change' | 'resend' | 'verify'

/** One asked-for send: which control asked, and whether it is the landing's
 * automatic attempt (which no control started). */
interface ContactSendRequest {
  readonly action: ContactWriteAction | null
  readonly automatic?: boolean
}

/**
 * Whether focus was lost when the initiating control's container left the
 * document. A write that removes its own control hands focus to a neighbour,
 * but only if the operator had not moved focus elsewhere while the write was
 * in flight: the browser drops focus to the body (or leaves it on a detached
 * node) in that case, while a connected control outside the container means
 * the operator chose that place.
 */
function focusFellWith(removed: HTMLElement | null): boolean {
  const active = document.activeElement
  if (active === null || active === document.body) {
    return true
  }
  if (!(active instanceof HTMLElement) || !active.isConnected) {
    return true
  }
  return removed !== null && removed.contains(active)
}

export interface ContactVerificationSectionProps {
  /** The session the human-verification proof is obtained for. */
  readonly session: AuthSession
  /** Whether the change-address entry starts open, so a host landing here
   * from a "change address" action reaches the form directly. Defaults to
   * closed (the code step). A mount that starts open never auto-sends,
   * even after the form is closed: the visitor arrived to change the
   * address, so the old one gets no code from this mount. */
  readonly initialChangeOpen?: boolean
  /** Whether a pending account with a target address sends a code once when
   * the section mounts, so a verification landing opens with one already on
   * its way. Defaults to false; the account page turns it on only for a
   * `?verify=1` landing and leaves an ordinary visit quiet. A refused
   * automatic send leaves the manual action and its explanation, and opens
   * no dialog. */
  readonly autoSend?: boolean
  /**
   * The human-verification gate the deployment reported, when the host
   * already read the discovery document. Omitted, the section reads the
   * methods document itself.
   */
  readonly humanGate?: AuthnHumanGate
}

export function ContactVerificationSection({
  session,
  initialChangeOpen = false,
  autoSend = false,
  humanGate,
}: ContactVerificationSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, isFetching, error, refetch } = useProfileQuery()
  const addIdentifier = useAuthnLoginAddIdentifier()
  const verifyIdentifier = useAuthnLoginVerifyIdentifier()
  // The address a send the viewer (or the landing's automatic send) asked
  // for is waiting on, because the gate wants a proof this client does not
  // hold yet. The gate is read and the proof obtained only while this is
  // set, so no challenge shows until a send is asked for.
  const [pending, setPending] = useState<{
    readonly address: string
    readonly automatic: boolean
  } | null>(null)
  // Which initiating control owns the write in flight (or the send waiting
  // on its proof), so only it reports busy. Null while idle, and for the
  // landing's automatic send, which no control started.
  const [activeWrite, setActiveWrite] = useState<ContactWriteAction | null>(null)
  // The deployment's human-verification gate and this section's proof. The
  // send (a resend included) presents it; the code's verification sends
  // nothing, so it does not.
  // A verified-email send does not address a phone number: it runs the
  // other surface's mechanism.
  const human = useHumanVerification(session, {
    scope: 'other',
    enabled: pending !== null,
    ...(humanGate === undefined ? {} : { gate: humanGate }),
  })
  // A host that already read the discovery document hands the gate in; a
  // standalone section learns it through the hook once a send asks for one.
  const gateDocument = humanGate ?? human.gate
  const needsProof = humanGateNeedsProof(gateDocument, 'other')
  // The last-line guard of the send itself: a required proof that is not
  // in hand keeps a request from leaving.
  const proofBlocked = needsProof && human.proof === null

  // The address a code went to during this mount. Null until a send lands;
  // the target the section displays otherwise comes from the profile's
  // verification.email, so a change that replaced the pending address
  // converges on the server's answer.
  const [sentTo, setSentTo] = useState<string | null>(null)
  // The sent notice's live region must never mount together with its text:
  // the region is mounted from the first render, and its text fills one
  // commit after a send lands, so the announcement is a content change the
  // screen reader already knows.
  const [sentCommitted, setSentCommitted] = useState(false)
  const [code, setCode] = useState('')
  const [codeInvalid, setCodeInvalid] = useState(false)
  // The code step is one control group: the resend and change-address sends
  // and the code's verification all report through one slot, so their
  // results never stand side by side and the attempt that settles last is
  // the one shown.
  const feedback = useWriteFeedback()
  // The verify step's non-code failure source: the wrong-code and
  // taken-address answers render the section's own wording on the verify
  // step. It occupies the group's result position and is cleared at the
  // start of every attempt, so it never stands beside another result.
  const [verifyFailureText, setVerifyFailureText] = useState<string | null>(
    null,
  )
  // The address being entered in the change / first-add form.
  const [emailEntry, setEmailEntry] = useState('')
  const [entryOpen, setEntryOpen] = useState(initialChangeOpen)
  // The instant the resend control becomes usable again, and the ticking
  // clock its disabled state and its note read.
  const [resendUntil, setResendUntil] = useState(0)
  // Whether that hold came from a landed send's `resend_after`. Only that
  // hold gets the section's note: a hold a throttled failure started is
  // explained by the failure's banner, which counts the same wait down.
  const [resendFromSuccess, setResendFromSuccess] = useState(false)
  const [now, setNow] = useState(() => Date.now())
  // Whether the mount's automatic send was refused for step-up. The
  // automatic send never opens the dialog the visitor did not ask for, so it
  // states why it could not send and leaves the manual action in place.
  const [autoStepUp, setAutoStepUp] = useState(false)
  // The mount-local guards for the automatic send: one attempt per mount,
  // however many times a refetch re-renders the section, and no attempt at
  // all when the host landed with the change form open (the `?change=email`
  // entry). That second guard is fixed at mount: closing the form later
  // does not turn the automatic send back on, because the visitor arrived
  // to change the address, not to receive a code for the old one.
  const autoSent = useRef(false)
  const autoSendSuppressed = useRef(initialChangeOpen)
  const codeRef = useRef<HTMLInputElement>(null)
  // Where focus lands when a landed verification removes the code step that
  // held the verify control, whether the settling write owns that move, and
  // the code step that write removes, captured before it unmounts.
  const verifiedLanding = useRef<HTMLDivElement>(null)
  const verifyLandingPending = useRef(false)
  const codeStep = useRef<HTMLElement>(null)
  const removedOnLanding = useRef<HTMLElement | null>(null)
  // The verify step's step-up gate: a proof that makes the address the
  // account's default contact or its only address is refused until the
  // session proves a fresh authentication, and a won challenge replays the
  // same code.
  const gate = useStepUpGate<string>((nextCode) => {
    void verify(nextCode)
  })

  // A write in flight in the shared scope, including the refetches that
  // follow the mutation.
  const writeInFlight =
    addIdentifier.isPending ||
    verifyIdentifier.isPending ||
    feedback.pending
  // A send waiting on its human-verification proof is the same attempt, but
  // it is not a write in flight: only the control that asked waits, and the
  // code already in hand may still be verified (which calls the wait off).
  const sendWaiting = pending !== null
  // The control that asked owns the busy state of both the proof wait and the
  // write it leads to.
  const attemptInFlight = writeInFlight || sendWaiting
  // Once the attempt settles no control owns an in-flight action any more.
  useEffect(() => {
    if (!attemptInFlight) {
      setActiveWrite(null)
    }
  }, [attemptInFlight])
  const verification = data?.verification
  const state = verification?.state ?? ''
  const targetEmail = verification?.email ?? ''

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

  // Arm the sent notice's live region one commit later so it fills an
  // already-mounted region, and request the focus hand-off when the send lands.
  // The field exists before the send, so the hand-off is a real move rather
  // than a timing race -- but the send scope keeps the controls disabled until
  // it settles (including the refetches that follow the mutation), and a
  // disabled input cannot take focus. The request is therefore discharged by
  // the first commit in which the scope is no longer busy, not here.
  const focusCodeWhenSettled = useRef(false)
  useEffect(() => {
    setSentCommitted(sentTo !== null)
    if (sentTo !== null) {
      focusCodeWhenSettled.current = true
    }
  }, [sentTo])

  useEffect(() => {
    if (!focusCodeWhenSettled.current || writeInFlight) {
      return
    }
    focusCodeWhenSettled.current = false
    codeRef.current?.focus()
  }, [writeInFlight, sentTo])

  // A landed verification replaces the code step, taking the focused verify
  // control with it: focus lands on the section that stays instead of the
  // document body or a node the landing detached. A resting place the
  // operator chose during the write is left alone, so the move happens only
  // when the removal took focus (see `focusFellWith`).
  useLayoutEffect(() => {
    if (state !== 'verified' || !verifyLandingPending.current) {
      return
    }
    verifyLandingPending.current = false
    const removed = removedOnLanding.current
    removedOnLanding.current = null
    if (!focusFellWith(removed)) {
      return
    }
    verifiedLanding.current?.focus()
  }, [state])

  const remainingSeconds = Math.max(
    0,
    Math.ceil((resendUntil - now) / 1000),
  )
  const emailEntryEmpty = emailEntry.trim() === ''
  const codeEmpty = code.trim() === ''
  // A precondition that is unmet explains itself on the control; a control
  // held by another write in the same scope carries no reason.
  const emailExplanation: ActionButtonUnavailableExplanation = emailEntryEmpty
    ? { unavailableReason: t('verification.emailEmpty') }
    : {}
  const codeExplanation: ActionButtonUnavailableExplanation = codeEmpty
    ? { unavailableReason: t('verification.codeEmpty') }
    : {}
  // A successful send's hold states its wait in the section's own note, and
  // the control points at that note. A hold a throttled failure started has
  // the failure's banner instead, so the control carries the wait itself.
  const resendExplanation: ActionButtonUnavailableExplanation =
    remainingSeconds > 0
      ? resendFromSuccess
        ? { unavailableDescribedBy: RESEND_NOTE_ID }
        : {
            unavailableReason: t('verification.resendIn', {
              seconds: remainingSeconds,
            }),
          }
      : {}
  // The three step forms are guarded rather than prevented by disabled
  // controls: each state matches its own submit control, so an Enter press
  // during another control's write reaches no request.
  const addSubmit = useActivationGuard(
    (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault()
      if (emailEntry.trim() !== '') {
        askToSend(emailEntry, { action: 'add' })
      }
    },
    {
      busy: attemptInFlight && activeWrite === 'add',
      unavailable: (writeInFlight && activeWrite !== 'add') || emailEntryEmpty,
    },
  )
  const changeSubmit = useActivationGuard(
    (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault()
      if (emailEntry.trim() !== '') {
        askToSend(emailEntry, { action: 'change' })
      }
    },
    {
      busy: attemptInFlight && activeWrite === 'change',
      unavailable:
        (writeInFlight && activeWrite !== 'change') || emailEntryEmpty,
    },
  )
  const verifySubmit = useActivationGuard(
    (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault()
      if (code.trim() !== '') {
        void verify(code)
      }
    },
    {
      busy: writeInFlight && activeWrite === 'verify',
      unavailable: (writeInFlight && activeWrite !== 'verify') || codeEmpty,
    },
  )

  /** Refetch what a landed write changes: the profile's verification state
   * and identifiers. Proving the address neither rotates this session's
   * credential nor revokes the account's other sessions. */
  async function invalidateAfterWrite(): Promise<void> {
    await queryClient.invalidateQueries({ queryKey: getAuthnProfileGetQueryKey() })
  }

  function send(nextEmail: string, automatic = false): Promise<void> {
    // The last-line guard: a required proof that is not ready keeps the
    // send from leaving. The caller has already waited for it.
    if (proofBlocked) {
      return Promise.resolve()
    }
    const address = nextEmail.trim()
    // The target the code step shows before this send: a send to a different
    // address replaces it, which is a change of context for the shared slot.
    const previousTarget = sentTo ?? targetEmail
    // Every send is a new attempt in the shared slot, so the code step's
    // previous answer -- the wrong-code or taken-address text included --
    // does not stand beside it.
    setVerifyFailureText(null)
    setCodeInvalid(false)
    setAutoStepUp(false)
    return feedback.run(async () => {
      const presented = needsProof ? human.proof : null
      let result
      try {
        result = await addIdentifier.mutateAsync({
          data:
            presented === null
              ? { kind: 'email', value: address }
              : { kind: 'email', value: address, human_proof: presented },
        })
      } catch (error) {
        const failureCode = errorCodeOf(error)
        // The send registers a contact and is not a gated write: a step-up
        // refusal renders its own code text rather than a dialog. The
        // automatic attempt states what happened instead of opening one; the
        // visitor's next click retries it like any other send.
        if (automatic && stepUpReasonOf(failureCode) !== null) {
          setAutoStepUp(true)
          return null
        }
        // Any other refusal reached the send's own checks, which spend the
        // single-use proof on the way: obtain a fresh one.
        if (presented !== null) {
          human.consume()
        }
        const retryAfter = retryAfterMsOf(error)
        if (retryAfter !== undefined) {
          setResendUntil(Date.now() + retryAfter)
          // The failure's banner carries this hold's countdown; the note
          // would repeat it.
          setResendFromSuccess(false)
          setNow(Date.now())
        }
        throw error
      }
      if (presented !== null) {
        human.consume()
      }
      const interval =
        typeof result.resend_after === 'number' ? result.resend_after : 0
      const held = interval > 0
      setResendUntil(held ? Date.now() + interval * 1000 : 0)
      // A successful send leaves no banner, so its own note is the wait's
      // only visible explanation.
      setResendFromSuccess(held)
      setNow(Date.now())
      // A send to another address is a change of context: the code step now
      // addresses a different target, so the old one's result is dropped.
      if (address !== previousTarget) {
        feedback.reset()
      }
      setSentTo(address)
      setCode('')
      setEmailEntry('')
      setEntryOpen(false)
      // The pending contact may have been replaced: re-read so the target
      // label and the verification state converge on the server's answer.
      await invalidateAfterWrite()
      // The "code sent" destination text is the code step's own state, not a
      // settled action: it is announced once through its own live region and
      // leaves the send scope's slot empty.
      return null
    })
  }

  const sendRef = useRef(send)
  sendRef.current = send

  /**
   * Ask for a code: send now when the gate is known to want no proof,
   * otherwise hold the send and let the block obtain one; the effect below
   * sends when it arrives. The automatic landing send takes this path too,
   * so a disabled hook cannot let it leave without a proof.
   */
  function askToSend(
    address: string,
    { action, automatic = false }: ContactSendRequest,
  ): void {
    if (pending !== null) {
      // A send is already waiting on its proof; asking again changes nothing.
      return
    }
    // The attempt begins here, whichever path it takes: the previous
    // answer must not stand beside the verification the new one renders,
    // and the control that asked owns the busy state of both the proof
    // wait and the write it leads to.
    setActiveWrite(action)
    setVerifyFailureText(null)
    setCodeInvalid(false)
    setAutoStepUp(false)
    feedback.reset()
    if (
      !human.pending &&
      gateDocument !== null &&
      (!needsProof || human.proof !== null)
    ) {
      void send(address, automatic)
      return
    }
    setPending({ address, automatic })
  }

  const askToSendRef = useRef(askToSend)
  askToSendRef.current = askToSend

  // A send asked for while the gate held it leaves as soon as the gate is
  // known and the proof it wants is in hand: the viewer's one request (or
  // the landing's one automatic attempt) is not turned into a second click.
  // A gate still being read keeps the send waiting; a gate that wants
  // nothing sends at once.
  useEffect(() => {
    if (pending === null || human.pending || gateDocument === null) {
      return
    }
    if (needsProof && human.proof === null) {
      return
    }
    const { address, automatic } = pending
    setPending(null)
    void sendRef.current(address, automatic)
  }, [pending, human.pending, gateDocument, needsProof, human.proof])

  function verify(nextCode: string): Promise<void> {
    // Verifying the code already in hand calls off a send waiting on its
    // proof: the late token must not send a second code that invalidates the
    // one being proved, and the wait it left behind must not block later
    // sends.
    setPending(null)
    setActiveWrite('verify')
    setCodeInvalid(false)
    // Only the verify that actually lands moves focus off the step it
    // replaces; a refused attempt leaves focus where the operator has it.
    verifyLandingPending.current = false
    // A verification is a new attempt in the shared result position, so the
    // automatic send's step-up explanation does not stand beside its answer.
    setAutoStepUp(false)
    return feedback.run(async () => {
      setVerifyFailureText(null)
      try {
        // Proving the address rotates nothing and revokes no other session,
        // so it opens no window; the refetch alone flips `verification` to
        // verified. A proof that changes how the account signs in may be
        // refused for a step-up, which arms the gate with the same code.
        await verifyIdentifier.mutateAsync({ data: { code: nextCode } })
        verifyLandingPending.current = true
        removedOnLanding.current = codeStep.current
        setCode('')
        setSentTo(null)
        setResendUntil(0)
        setResendFromSuccess(false)
        await invalidateAfterWrite()
      } catch (error) {
        const failureCode = errorCodeOf(error)
        if (failureCode === 'authn.login_failed') {
          setCodeInvalid(true)
          setCode('')
          setVerifyFailureText('verification.codeInvalid')
          return null
        }
        if (
          failureCode === 'authn.conflict' ||
          failureCode === 'authn.email_taken'
        ) {
          setVerifyFailureText('verification.addressTaken')
          return null
        }
        const reason = stepUpReasonOf(failureCode)
        if (reason !== null) {
          gate.arm(nextCode, reason)
          return null
        }
        throw error
      }
      // The section re-reads into `verified`, whose completion state names
      // the proved address: that is the verification's success.
      return null
    })
  }

  // The verification landing's single automatic send: it fires when the
  // profile first settles on a pending target with the change form closed,
  // and the mount-local refs keep every later render, refetch and
  // form close from sending. It waits on the same pending path as a
  // visitor's click, because it has no click of its own to obtain a proof
  // with.
  useEffect(() => {
    if (
      !autoSend ||
      autoSent.current ||
      autoSendSuppressed.current ||
      entryOpen
    ) {
      return
    }
    if (state !== 'pending' || targetEmail === '') {
      return
    }
    autoSent.current = true
    askToSendRef.current(targetEmail, { action: null, automatic: true })
  }, [autoSend, entryOpen, state, targetEmail])

  // A settled answer with no body is the error variant, never "nothing to
  // verify" (the async section below renders the retry). A settled state
  // carrying no verification renders nothing at all.
  if (data !== undefined && (state === '' || state === 'none')) {
    return null
  }

  // The verify group's non-code failure, beside its region and mutually
  // exclusive with it: the wrong-code and taken-address answers render this
  // section's own wording, every other code through the whitelist.
  const verifyFailureBanner =
    verifyFailureText === null ? null : (
      <Alert severity="error" role="alert" sx={{ width: '100%' }}>
        {t(verifyFailureText)}
      </Alert>
    )

  const sentNotice = sentTo ?? targetEmail
  const hasTarget = targetEmail !== ''

  // The deployment's human-verification block. A send renders it only while
  // that send waits on a proof; it renders nothing when no proof is
  // required.
  const humanBlock = (
    <HumanVerification
      scope="other"
      gate={human.gate ?? humanGate}
      proof={human.proof}
      status={human.status}
      captcha={human.captcha}
      onToken={human.adoptToken}
      onRetry={human.retry}
      onRefreshCaptcha={human.refreshCaptcha}
      onRequestAudio={human.requestAudio}
      audioRetryAfterSeconds={human.audioRetryAfterSeconds}
      disabled={writeInFlight}
    />
  )

  // The settled verification states. The read's pending and failure
  // branches belong to the async section below, so a failed refresh over
  // already rendered content keeps that content under its stale notice.
  const verifiedCard = (
    // A landed verification takes the code step -- and the focused verify
    // control -- off the page: this group is the named landing that stays,
    // programmatically focusable without entering the tab order.
    <Box
      ref={verifiedLanding}
      tabIndex={-1}
      role="group"
      aria-label={t('verification.verifiedTitle')}
    >
      <SectionCard title={t('verification.verifiedTitle')}>
        <Alert severity="success" role="status">
          {targetEmail === ''
            ? t('verification.verifiedDescriptionNoEmail')
            : t('verification.verifiedDescription', {
                email: targetEmail,
                interpolation: { escapeValue: false },
              })}
        </Alert>
      </SectionCard>
    </Box>
  )

  // The send and verify control group's one result region. The add form and
  // the code step are mutually exclusive branches of that group, so they
  // place the same element under their action row rather than splitting the
  // group into two regions.
  const feedbackRegion = (
    <WriteFeedbackRegion
      outcome={feedback.outcome}
      sx={{ alignSelf: 'stretch', width: '100%' }}
    />
  )

  // A pending account with no target: the add-email form is the only way back
  // to a verifiable address.
  const addForm = (
    <SectionCard
      title={t('verification.addTitle')}
      description={t('verification.addDescription')}
    >
      <Box
        component="form"
        noValidate
        onSubmit={addSubmit}
        sx={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'flex-start',
          gap: 2,
          maxWidth: FORM_MAX_WIDTH,
        }}
      >
        <TextField
          fullWidth
          type="email"
          autoComplete="email"
          label={t('verification.addLabel')}
          value={emailEntry}
          slotProps={{ htmlInput: { readOnly: writeInFlight } }}
          onChange={(event) => {
            // Editing the address abandons a send waiting on its proof.
            setPending(null)
            setEmailEntry(event.target.value)
          }}
        />
        {pending !== null ? humanBlock : null}
        {/* The step's only action is its primary; it still closes its
            action row at the field's right edge. */}
        <ActionRow sx={{ width: '100%' }}>
          <ActionButton
            type="submit"
            emphasis="primary"
            busy={attemptInFlight && activeWrite === 'add'}
            unavailable={(writeInFlight && activeWrite !== 'add') || emailEntryEmpty}
            {...emailExplanation}
          >
            {t('verification.addSubmit')}
          </ActionButton>
        </ActionRow>
        {/* The add form is the send group's control here: its result
            sits below the action that made it. */}
        {feedbackRegion}
      </Box>
    </SectionCard>
  )

  const verificationForm = (
    <>
      <SectionCard
        ref={codeStep}
        title={t('verification.title')}
        description={t('verification.description')}
      >
        <Box
          component="form"
          noValidate
          onSubmit={verifySubmit}
          sx={{
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'flex-start',
            gap: 2,
            maxWidth: FORM_MAX_WIDTH,
          }}
        >
          {autoStepUp ? (
            <Alert severity="info" sx={{ width: '100%' }}>
              {t('verification.autoStepUp')}
            </Alert>
          ) : null}
          <Typography variant="body2" color="text.secondary">
            {sentNotice === ''
              ? t('verification.description')
              : t('verification.target', {
                  email: sentNotice,
                  interpolation: { escapeValue: false },
                })}
          </Typography>
          {/* The sent notice's live region: mounted from the first render,
              filled one commit after a code lands, so it announces the
              destination once without becoming a second result in the send
              scope's slot. */}
          <Box role="status" sx={visuallyHiddenSx}>
            {sentCommitted && sentTo !== null
              ? t('verification.sent', {
                  email: sentTo,
                  interpolation: { escapeValue: false },
                })
              : ''}
          </Box>
          <TextField
            fullWidth
            inputMode="numeric"
            autoComplete="one-time-code"
            label={t('verification.codeLabel')}
            helperText={t('verification.codeHelper')}
            value={code}
            error={codeInvalid}
            slotProps={{
              htmlInput: {
                'aria-invalid': codeInvalid,
                readOnly: writeInFlight,
              },
            }}
            inputRef={codeRef}
            onChange={(event) => {
              setCode(event.target.value)
              if (codeInvalid) {
                setCodeInvalid(false)
              }
            }}
          />
          {/* The action row: resend and change-address are secondary and
              stand on the left, verify is the step's primary action and
              closes the row on the right. All three share one line and one
              baseline. The block stands above the row, because a send it
              waits on is the one that asked for the proof. The group's one
              result sits directly below the row. */}
          {pending !== null ? humanBlock : null}
          <ActionRow sx={{ width: '100%' }}>
            <ActionButton
              type="button"
              busy={attemptInFlight && activeWrite === 'resend'}
              unavailable={
                (writeInFlight && activeWrite !== 'resend') ||
                remainingSeconds > 0
              }
              {...resendExplanation}
              onClick={() => askToSend(sentNotice, { action: 'resend' })}
            >
              {sentTo === null
                ? t('verification.send')
                : t('verification.resend')}
            </ActionButton>
            <ActionButton
              type="button"
              unavailable={writeInFlight}
              onClick={() => {
                // Closing the change entry abandons a send waiting on its
                // proof, so no code goes to the address it replaced.
                setPending(null)
                setEntryOpen((open) => !open)
                setEmailEntry('')
              }}
            >
              {entryOpen
                ? t('verification.changeCancel')
                : t('verification.change')}
            </ActionButton>
            <ActionButton
              type="submit"
              emphasis="primary"
              busy={writeInFlight && activeWrite === 'verify'}
              unavailable={
                (writeInFlight && activeWrite !== 'verify') || codeEmpty
              }
              {...codeExplanation}
            >
              {t('verification.verify')}
            </ActionButton>
          </ActionRow>
          {/* The group's one result: the resend, change-address send and the
              code verification all write into it, and it sits directly under
              the action row so the result stands next to the action that
              produced it. */}
          {feedbackRegion}
          {verifyFailureBanner}
          {/* A successful send's hold has no banner to explain it, so the
              note states its wait. A throttled failure's banner counts the
              same wait down, so this note stays away then. */}
          {remainingSeconds > 0 && resendFromSuccess ? (
            <Typography
              id={RESEND_NOTE_ID}
              variant="body2"
              color="text.secondary"
            >
              {t('verification.resendIn', { seconds: remainingSeconds })}
            </Typography>
          ) : null}
        </Box>
        {/* The change-address entry is its own form: inside the code form,
            pressing Enter in the address field would submit the code. */}
        {entryOpen ? (
          <Box
            component="form"
            noValidate
            onSubmit={changeSubmit}
            sx={{ mt: 2, maxWidth: FORM_MAX_WIDTH }}
          >
            {/* The change-address face is the add face's shape: the field
                owns the row and its submit sits beneath it, never beside
                it at the field's top edge, closing that row on the field's
                right edge as the add form's does. */}
            <Box
              sx={{
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'flex-start',
                gap: 2,
                width: '100%',
              }}
            >
              <TextField
                fullWidth
                type="email"
                autoComplete="email"
                label={t('verification.changeLabel')}
                value={emailEntry}
                slotProps={{ htmlInput: { readOnly: writeInFlight } }}
                onChange={(event) => {
                  // Editing the address abandons a send waiting on its proof.
                  setPending(null)
                  setEmailEntry(event.target.value)
                }}
              />
              <ActionRow sx={{ width: '100%' }}>
                <ActionButton
                  type="submit"
                  emphasis="primary"
                  busy={attemptInFlight && activeWrite === 'change'}
                  unavailable={
                    (writeInFlight && activeWrite !== 'change') ||
                    emailEntryEmpty
                  }
                  {...emailExplanation}
                >
                  {t('verification.changeSubmit')}
                </ActionButton>
              </ActionRow>
            </Box>
          </Box>
        ) : null}
      </SectionCard>
    </>
  )

  return (
    <>
      <AccountAsyncSection
        pending={isPending && data === undefined}
        payload={data}
        empty={false}
        error={error}
        refreshing={isFetching}
        loading={
          <SectionCard title={t('verification.title')}>
            <Box
              aria-busy="true"
              sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}
            >
              <Skeleton variant="text" width="40%" />
              <Skeleton variant="text" width="70%" />
            </Box>
          </SectionCard>
        }
        errorState={{
          title: t('verification.error.title'),
          description: t('verification.error.description'),
          retryLabel: t('verification.retry'),
          onRetry: () => void refetch(),
        }}
      >
        {() =>
          state === 'verified' ? (
            verifiedCard
          ) : !hasTarget && sentTo === null ? (
            addForm
          ) : (
            verificationForm
          )
        }
      </AccountAsyncSection>

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
