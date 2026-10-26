/**
 * IdentifiersSection: the sign-in-identifiers surface of the account page.
 *
 * The identifiers are the account's own, read off the profile
 * (GET /api/v1/auth/me, authn.profile.get): the spec ships no dedicated
 * identifier-list route, and the profile answer carries each identifier's
 * own id, kind, value and proven flag, so the section reads the same
 * profile query the other account surfaces read. Each row names the kind
 * (email, phone or username, mapped through a guarded vocabulary, a value
 * outside it rendering the generic identifier label) and the value, with
 * the server's `proven` answer (the ways of establishing ownership that
 * actually prove it -- email-code, sms-code, invite, idp:<provider> --
 * versus the ones that only record an assertion: admin, import-unverified,
 * self). `verified_at` cannot answer that question: every stored identifier
 * carries one, including a username written at registration as `self`.
 * A username is a login name with no ownership to prove, so its row carries
 * no ownership chip. An email or phone row carries a two-value ownership
 * chip: `proven` true reads `verified`, false reads `unverified`, whatever
 * channel established the identifier. An address an administrator asserted or
 * an import recorded is not a third state -- it is an address its holder has
 * not proved, and the account's pending verification blocks it like any other.
 *
 * A row whose identifier carries an id has a row-end remove action that
 * sits behind the ui-kit danger ConfirmDialog -- removing an identifier is
 * irreversible (it is re-established only by walking the route that
 * proves it again). The removal is DELETE /api/v1/auth/identifiers/{id}
 * (authn.login.removeIdentifier). The account module refuses to remove the
 * account's last way to log in with a 409, whose code text renders above
 * the list; a refused removal re-reads the profile, so a guard that read a
 * stale list converges on the server's answer. The removal does not replace
 * the caller's credential, so it runs outside the identity-transition
 * window that the credential-rotating writes below use. An account with a
 * password
 * signs in with a username or an email
 * address, so while it holds exactly one such identifier the section
 * renders a note in place of that row's remove action: removing it would
 * leave the password without a login name. Another username/email, or an
 * account with no password at all, keeps the action. Phone numbers are not
 * counted -- they sign in through their own code route. The server remains
 * the authority on the last way in; the note states the one case the
 * profile can reveal.
 *
 * An unverified email row carries a verify action beside its remove action
 * (the remove action is withheld while the row is the password account's only
 * login name; the verify action is not). The action proves the address the row
 * already shows: POST /api/v1/auth/identifiers re-sends a code to that same
 * address -- the route accepts the account's own address as a re-add of its
 * proof -- and POST /api/v1/auth/identifiers/verify submits the code. The write
 * is idempotent, so the refetched row flips to `verified`. The address stays
 * fixed in this flow: a row's value is not editable here, and choosing another
 * address belongs to the add face or the verification section. The action is
 * withheld while the profile's `verification.state` is `pending`, because that
 * section and the host's banner already offer the same code and two concurrent
 * requests would overwrite the one pending record. It is also withheld on a
 * deployment with no email channel, which could not send the code.
 *
 * Below the list is the add face: POST /api/v1/auth/identifiers
 * (authn.login.addIdentifier) with a kind and a value. An account holds at
 * most one username, and the server answers a second one with the same 409
 * authn.conflict as a name someone else took -- copy cannot distinguish
 * them -- so the add face does not offer the username kind once the
 * profile lists one. A username is a login name with no ownership to
 * prove, so it is written in one step; a username already in use is
 * answered 409 authn.conflict, and because that is the expected outcome of
 * the one write this face exists for, the section names the username
 * rather than rendering the code's account-state sentence. An
 * email is a contact address, so the add sends a code to it (the answer is
 * state "pending") and the code is submitted through POST
 * /api/v1/auth/identifiers/verify (authn.login.verifyIdentifier); only the
 * matching code writes the address, with the proof. The email branch is
 * unavailable on a deployment with no email channel (a 503), and both
 * writes revoke the account's other sessions, so a success refetches the
 * profile and the session list. An address another account has reserved or
 * proved is answered 409 authn.email_taken -- the claim is atomic, so the
 * refusal is the address's, and the value field carries it. The code's
 * verification answers the same code when the address was taken after the
 * code was requested; the face returns to the value field rather than
 * asking for a code nothing can accept.
 *
 * An email address is checked against the server's own rule before it is
 * sent -- trimmed, printable ASCII, exactly one @ with a part on each
 * side, at most 254 bytes -- so a malformed address is refused in the
 * field instead of making a round trip that can only answer the generic
 * bad-request. The value submitted is the trimmed one. A 400 the server
 * still answers for the email kind renders the same field message rather
 * than the account-state banner; a username's 400 keeps the banner,
 * because the server's username rule also refuses shapes this face does
 * not restate (the reserved static-credential prefix, a UUID-shaped
 * name). Closing the add face clears the whole attempt: the failure
 * banner, the field error, the success notice and the sent-code step.
 *
 * Every write asks for a fresh authentication: a 401
 * authn.insufficient_user_authentication opens the step-up dialog with the
 * write's inputs remembered, and a won challenge re-runs that one write
 * exactly once. A successful write refetches the profile so the rows
 * converge on the server's answer. The one-step username add and both
 * verification writes replace this session's credential as they commit, so
 * each runs inside an identity-transition window
 * (session.beginIdentityTransition) that closes once the refetch that
 * follows the write has settled; a read still carrying the outgoing
 * credential is discarded instead of ending the session. The email send
 * only issues a code -- it does not rotate the credential -- and runs
 * outside the window.
 *
 * The section has one failure slot: removal, add, row verification and the
 * code's verification share it, so a failure renders exactly one role="alert"
 * banner. The slot is one control group -- the row actions and the add face
 * are one section -- so its region sits below the add face, after the whole
 * group.
 *
 * The section takes the session (the step-up dialog drives it) and the two
 * optional add-face flags, while the profile read and the writes travel on
 * the shared access token through the generated calls.
 *
 * An email code send also presents the deployment's human-verification
 * proof when its gate requires one. The gate comes from the methods
 * document (or from a `humanGate` a host already read); the add face and
 * a row's resend show the block and wait for a proof. A username write
 * and every verification send no message, so they present none and are
 * not gated by the block. Each landing or refused send obtains a fresh
 * proof, because the single-use one was spent.
 */

import { useEffect, useRef, useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Chip from '@mui/material/Chip'
import MenuItem from '@mui/material/MenuItem'
import Skeleton from '@mui/material/Skeleton'
import Stack from '@mui/material/Stack'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnProfileGetQueryKey,
  getAuthnSessionListQueryKey,
  useAuthnLoginAddIdentifier,
  useAuthnLoginRemoveIdentifier,
  useAuthnLoginVerifyIdentifier,
} from '@speed/api-sdk'
import type { AuthSession, AuthnHumanGate } from '@speed/auth-core'
import { humanGateNeedsProof, useStepUpGate, useHumanVerification } from '@speed/auth-core'
import {
  ConfirmDialog,
  errorCodeOf,
  FORM_MAX_WIDTH,
  HumanVerification,
  ListSkeleton,
  parseEmailAddress,
  SectionCard,
  useWriteFeedback,
} from '@speed/ui-kit'
import { AccountAsyncSection } from './internal/async-section.js'
import { runIdentityTransition } from './internal/identity-transition.js'
import { useProfileQuery } from './internal/queries.js'
import { retryAfterMsOf } from './internal/retry-after.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'

const STEP_UP_REQUIRED = 'authn.insufficient_user_authentication'

export interface IdentifiersSectionProps {
  /** The session whose step-up flow the dialog drives. */
  readonly session: AuthSession
  /**
   * Whether the add surface offers the email kind. The host reads the
   * deployment's `authn.identifiers.email` feature and passes the
   * answer, optionally narrowing it by its own product choice; false
   * removes the email option from the add face. Defaults to true, so a
   * host that does not gate identifiers keeps the full face.
   */
  readonly emailAvailable?: boolean
  /**
   * Whether the add surface offers the username kind. False removes it
   * (an email-only product), leaving the email kind alone. Defaults to
   * true. An account that already lists a username never offers the kind
   * regardless of this prop.
   */
  readonly offerUsername?: boolean
  /**
   * The human-verification gate the deployment reported, when the host
   * already read the discovery document. Omitted, the section reads the
   * methods document itself.
   */
  readonly humanGate?: AuthnHumanGate
}

/** The identifier kinds the authn account module records
 * (pkg/authn's IdentifierKind), one bundle key each under
 * identifiers.kind. A value outside the list renders the generic
 * "other" label, never a raw value. */
const KNOWN_IDENTIFIER_KINDS = new Set<string>([
  'email',
  'phone',
  'username',
])

/** The kinds the add face offers here: a username and an email. A phone is
 * added through its own two-step surface (the SMS routes), not this one. */
type AddKind = 'email' | 'username'

/** The write a step-up dialog is armed for, so a won challenge re-runs
 * exactly it: a removal, the add that sends the code, the code's
 * verification, or the same pair on an existing row's address. */
type SectionAction =
  | { readonly step: 'remove'; readonly id: string }
  | { readonly step: 'add'; readonly kind: AddKind; readonly value: string }
  | { readonly step: 'verify'; readonly code: string }
  | { readonly step: 'rowSend'; readonly value: string }
  | { readonly step: 'rowVerify'; readonly code: string }

/** The resend note's id, referenced by the row flow's resend control while it
 * counts down. */
const ROW_RESEND_NOTE_ID = 'identifiers-row-resend-note'

/** The pending-state placeholder, read aloud as one loading announcement. */
function IdentifierListSkeleton({ label }: { readonly label: string }) {
  const widths = [
    { primary: '40%', secondary: '56%' },
    { primary: '30%', secondary: '48%' },
  ]
  return (
    <ListSkeleton
      label={label}
      rows={widths.map((width) => (
        <>
          <Skeleton variant="text" width={width.primary} />
          <Skeleton variant="text" width={width.secondary} />
        </>
      ))}
    />
  )
}

export function IdentifiersSection({
  session,
  emailAvailable = true,
  offerUsername = true,
  humanGate,
}: IdentifiersSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, isFetching, error, refetch } = useProfileQuery()
  const removeIdentifier = useAuthnLoginRemoveIdentifier()
  const addIdentifier = useAuthnLoginAddIdentifier()
  const verifyIdentifier = useAuthnLoginVerifyIdentifier()
  // The deployment's human-verification gate and this section's proof.
  // Only an email code send presents it: a username write and every
  // verification send no message.
  const human = useHumanVerification(
    session,
    humanGate === undefined ? {} : { gate: humanGate },
  )
  const needsProof = humanGateNeedsProof(human.gate)
  // Only a known gate that requires a proof blocks an email send. A gate
  // still being read does not: the server judges the send if the document
  // arrives late.
  const proofBlocked = needsProof && human.proof === null

  const [removeTarget, setRemoveTarget] = useState<string | null>(null)
  // The section's one feedback scope: removal, add and both verification
  // flows share this slot, so the section shows at most one result at a time
  // and one failure never stands beside another.
  const feedback = useWriteFeedback()
  // The scope's non-code failure source: a failure the server's code text
  // describes too broadly (a taken username, an address another account
  // proved in between). It occupies the same result position as the region's
  // failure, and the two are mutually exclusive.
  const [failureText, setFailureText] = useState<string | null>(null)

  // The add face's own state. addOpen shows the form; addKind picks the
  // kind; addValue holds the entry; sentTo is the address a code went to,
  // or null while the value is being entered; code is the answer.
  // fieldErrorKey is the value field's own error (an ill-formed email, the
  // server's bad-request answer to one, or an address another account
  // already holds), rendered under that field rather than in the banner.
  const [addOpen, setAddOpen] = useState(false)
  const [addKind, setAddKind] = useState<AddKind>(
    emailAvailable ? 'email' : 'username',
  )
  const [addValue, setAddValue] = useState('')
  const [sentTo, setSentTo] = useState<string | null>(null)
  // The sent notice's live region must never mount together with its text:
  // it fills one commit after the code step mounts, so the announcement is a
  // content change the screen reader already knows.
  const [sentCommitted, setSentCommitted] = useState(false)
  const [code, setCode] = useState('')
  const [fieldErrorKey, setFieldErrorKey] = useState<string | null>(null)

  // The row flow's own state. rowVerify is the address whose row verification
  // is open (null when none is); rowSentTo is that address once its code has
  // been requested; rowCode is the answer; rowCodeInvalid marks a refused
  // code in the field. rowResendUntil / rowNow drive the resend countdown.
  const [rowVerify, setRowVerify] = useState<string | null>(null)
  const [rowSentTo, setRowSentTo] = useState<string | null>(null)
  const [rowCode, setRowCode] = useState('')
  const [rowCodeInvalid, setRowCodeInvalid] = useState(false)
  const [rowResendUntil, setRowResendUntil] = useState(0)
  const [rowNow, setRowNow] = useState(() => Date.now())

  // Arm the sent notice's live region one commit after the code step mounts,
  // so the notice fills an already-mounted region rather than arriving with
  // it, and hand focus to the code field. The region is empty again when the
  // flow returns to the value field.
  const addCodeRef = useRef<HTMLInputElement>(null)
  useEffect(() => {
    setSentCommitted(sentTo !== null)
    if (sentTo !== null) {
      addCodeRef.current?.focus()
    }
  }, [sentTo])

  // Disabled through the whole attempt, including the refetches that follow
  // the mutation, so no control changes context under a pending scope.
  const addBusy =
    addIdentifier.isPending || verifyIdentifier.isPending || feedback.pending
  const writeBusy = removeIdentifier.isPending || feedback.pending

  // Tick the row flow's countdown only while one is running, and stop the
  // timer at its own expiry rather than leaving it ticking for the rest of
  // the mount.
  useEffect(() => {
    if (rowResendUntil <= Date.now()) {
      return undefined
    }
    const timer = setInterval(() => {
      const current = Date.now()
      setRowNow(current)
      if (current >= rowResendUntil) {
        clearInterval(timer)
      }
    }, 1000)
    return () => {
      clearInterval(timer)
    }
  }, [rowResendUntil])

  const gate = useStepUpGate<SectionAction>((action) => {
    switch (action.step) {
      case 'remove':
        void remove(action.id)
        return
      case 'add':
        void add(action.kind, action.value)
        return
      case 'verify':
        void verify(action.code)
        return
      case 'rowSend':
        void rowSend(action.value)
        return
      case 'rowVerify':
        void rowVerifyCode(action.code)
        return
    }
  })

  const pending = isPending && data === undefined
  const rows = !pending && data !== undefined ? data.identifiers : undefined
  // A password signs an account in through a username or an email address.
  // With exactly one of those on the profile, that row's removal
  // is withheld and explained; the server still judges the last way in on
  // the write itself, so this is the visible half of the same rule.
  const passwordSet = data?.password_set === true
  const loginNameCount =
    rows === undefined
      ? 0
      : rows.filter((row) => row.kind === 'username' || row.kind === 'email')
          .length
  // An account holds at most one username. The server answers a second
  // username with the same 409 authn.conflict as a name someone else took,
  // so once the profile lists one the add face does not offer that kind:
  // the client never sends a write whose refusal the copy cannot describe.
  const usernamePresent =
    rows !== undefined && rows.some((row) => row.kind === 'username')
  // A pending account carries one contact awaiting proof, managed by
  // ContactVerificationSection. Offering an email add here as well would put a
  // second pending address on the account, so the kind leaves this face until
  // the account is verified.
  const verificationPending = data?.verification?.state === 'pending'
  // A row's verify action is offered only where it can work: an unverified
  // email row, on a deployment with an email channel, while the account as a
  // whole is not pending. A pending account's code flow belongs to the
  // verification section and the host's banner; a second concurrent request
  // would overwrite the one pending record.
  const rowVerifyAvailable = emailAvailable && !verificationPending
  const rowRemainingSeconds = Math.max(
    0,
    Math.ceil((rowResendUntil - rowNow) / 1000),
  )
  /** The kinds the add face offers in this deployment and profile. */
  const addKinds: AddKind[] = []
  if (emailAvailable && !verificationPending) {
    addKinds.push('email')
  }
  if (offerUsername && !usernamePresent) {
    addKinds.push('username')
  }
  // The selector's value: the chosen kind while it is still offered, and
  // otherwise the first offered one (the profile can change under an open
  // form). With no offered kind the face renders nothing, so the fallback
  // is never shown.
  const formKind: AddKind = addKinds.includes(addKind)
    ? addKind
    : (addKinds[0] ?? 'username')
  // A settled read that lists none, or one whose body omitted the key,
  // hands the section to the EmptyState, whose title stands in for the
  // hidden h2 header at its own level (headingLevel="h2" below) -- the
  // heading order never skips a level, and "no identifiers" is only ever
  // claimed for an answer that genuinely listed none.
  const showHeader = pending || (rows !== undefined && rows.length > 0)

  /** Refetch what a write changes: the profile's identifiers, and the
   * session list, because these routes revoke the account's other
   * sessions. */
  async function invalidateAfterWrite(): Promise<void> {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: getAuthnProfileGetQueryKey() }),
      queryClient.invalidateQueries({
        queryKey: getAuthnSessionListQueryKey(),
      }),
    ])
  }

  /** Clear the section's shared result position: the slot and the
   * non-code failure source alike. */
  function clearFailure(): void {
    feedback.reset()
    setFailureText(null)
  }

  /** Settle the add face back to closed with no trace of the attempt: the
   * failure banner, the field error, the success notice and the sent-code
   * step all belong to the form that is going away. */
  function closeAdd(): void {
    setAddOpen(false)
    setSentTo(null)
    setAddValue('')
    setCode('')
    setFieldErrorKey(null)
    clearFailure()
  }

  async function remove(id: string): Promise<void> {
    // Captured before the write: the refetched profile drops the row, so the
    // confirmation's name must be read from the list this attempt acted on.
    const identifierValue = rows?.find((row) => row.id === id)?.value ?? ''
    return feedback.run(async () => {
      setFailureText(null)
      try {
        await removeIdentifier.mutateAsync({ id })
      } catch (error) {
        setRemoveTarget(null)
        const failure = errorCodeOf(error)
        if (failure === STEP_UP_REQUIRED) {
          gate.arm({ step: 'remove', id })
          return null
        }
        // The refusal is the server's judgement on the current state, which
        // this mount's guard may have read stale (another session removed a
        // login name first): re-read so the list and the withheld-removal
        // note converge on what the server sees.
        await invalidateAfterWrite()
        throw error
      }
      setRemoveTarget(null)
      await invalidateAfterWrite()
      // Translated when the attempt settles; it names the identifier removed.
      return t('identifiers.removed', {
        value: identifierValue,
        interpolation: { escapeValue: false },
      })
    })
  }

  function add(kind: AddKind, value: string): Promise<void> {
    // Only the email branch sends a code, so only it needs the proof.
    if (kind === 'email' && proofBlocked) {
      return Promise.resolve()
    }
    setFieldErrorKey(null)
    return feedback.run(async () => {
      setFailureText(null)
      const presented = kind === 'email' && needsProof ? human.proof : null
      /** Apply the write's answer: an address's code step, or the settled
       * add that refetches the profile. */
      async function settleAdded(result: {
        readonly state?: string
      }): Promise<string | null> {
        if (result.state === 'pending') {
          // An address the deployment can reach: the code is on its way and
          // the answer is the next step, not a settled action.
          setSentTo(value)
          setCode('')
          return null
        }
        closeAdd()
        await invalidateAfterWrite()
        // The confirmation names the changed kind (the sign-in identifier).
        return t('identifiers.add.added')
      }
      try {
        const data =
          presented === null
            ? { kind, value }
            : { kind, value, human_proof: presented }
        // A username is written in one step, and that write rotates this
        // session's credential with it, so it runs inside an
        // identity-transition window whose release waits for the refetch
        // that follows. The email add only sends a code: it does not rotate
        // the credential, so it runs outside the window.
        if (kind === 'username') {
          return await runIdentityTransition(
            session,
            () => addIdentifier.mutateAsync({ data }),
            settleAdded,
          )
        }
        const result = await addIdentifier.mutateAsync({ data })
        if (presented !== null) {
          human.consume()
        }
        return await settleAdded(result)
      } catch (error) {
        const failure = errorCodeOf(error)
        if (failure === STEP_UP_REQUIRED) {
          // A step-up refusal precedes the proof check, so the proof is
          // still valid for the re-run.
          gate.arm({ step: 'add', kind, value })
          return null
        }
        // The send reached its own checks, which spend the single-use
        // proof: obtain a fresh one.
        if (presented !== null) {
          human.consume()
        }
        // A bad request for an email is the server refusing the address's
        // shape -- the same rule the client checked -- so the field carries
        // the message instead of the generic account-state banner. A taken
        // address is the same field's failure: the claim is atomic, and the
        // one value the viewer can change is the address. A username's bad
        // request keeps the banner: the server's username
        // rule also refuses shapes the client does not restate (the
        // reserved static-credential prefix, a UUID-shaped name), which the
        // kind's helper text describes rather than one blanket sentence.
        if (
          kind === 'email' &&
          (failure === 'authn.bad_request' || failure === 'authn.email_taken')
        ) {
          setFieldErrorKey(
            failure === 'authn.email_taken'
              ? 'identifiers.add.emailTaken'
              : 'identifiers.add.invalidEmail',
          )
          return null
        }
        if (kind === 'username' && failure === 'authn.conflict') {
          // An existing username is the expected 409 of the one write this
          // face exists for, and the account-state sentence sends the viewer
          // to refresh, which can never free the name.
          setFailureText('identifiers.add.usernameTaken')
          return null
        }
        throw error
      }
    })
  }

  function verify(nextCode: string): Promise<void> {
    return feedback.run(async () => {
      setFailureText(null)
      try {
        // The verification writes the address with its proof and rotates
        // this session's credential, so the window is open before the write
        // and closes once the refetch that follows it has settled.
        await runIdentityTransition(
          session,
          () => verifyIdentifier.mutateAsync({ data: { code: nextCode } }),
          async () => {
            closeAdd()
            await invalidateAfterWrite()
          },
        )
      } catch (error) {
        const failure = errorCodeOf(error)
        if (failure === STEP_UP_REQUIRED) {
          gate.arm({ step: 'verify', code: nextCode })
          return null
        }
        if (failure === 'authn.email_taken') {
          // The address was taken between the code request and the write
          // (the write is the authoritative refusal): the value is what must
          // change, so the face returns to it with the field's message
          // instead of leaving the viewer on a code with nothing to answer.
          setSentTo(null)
          setCode('')
          setFieldErrorKey('identifiers.add.emailTaken')
          return null
        }
        throw error
      }
      return t('identifiers.add.added')
    })
  }

  /** Open a row's verification flow on the address the row already shows.
   * The add face closes, because one code flow runs at a time on this
   * section. */
  function openRowVerify(value: string): void {
    closeAdd()
    setRowCodeInvalid(false)
    setRowCode('')
    setRowSentTo(null)
    setRowResendUntil(0)
    setRowVerify(value)
  }

  /** Settle the row flow back to closed with no trace of the attempt. */
  function closeRowVerify(): void {
    setRowVerify(null)
    setRowSentTo(null)
    setRowCode('')
    setRowCodeInvalid(false)
    setRowResendUntil(0)
    clearFailure()
  }

  /** Re-send a code to the row's fixed address. The route accepts the
   * account's own address as a re-add of its proof, so this is the same
   * write the add face makes, with the value already known. */
  function rowSend(address: string): Promise<void> {
    if (proofBlocked) {
      return Promise.resolve()
    }
    setRowCodeInvalid(false)
    return feedback.run(async () => {
      setFailureText(null)
      const presented = needsProof ? human.proof : null
      try {
        const result = await addIdentifier.mutateAsync({
          data:
            presented === null
              ? { kind: 'email', value: address }
              : { kind: 'email', value: address, human_proof: presented },
        })
        if (presented !== null) {
          human.consume()
        }
        const interval =
          typeof result.resend_after === 'number' ? result.resend_after : 0
        setRowResendUntil(interval > 0 ? Date.now() + interval * 1000 : 0)
        setRowNow(Date.now())
        setRowSentTo(address)
        setRowCode('')
        // The code is on its way; the sent-code step is the flow's own
        // state, not a settled action.
        return null
      } catch (error) {
        const failure = errorCodeOf(error)
        if (failure === STEP_UP_REQUIRED) {
          // The proof is not spent by a step-up refusal.
          gate.arm({ step: 'rowSend', value: address })
          return null
        }
        if (presented !== null) {
          human.consume()
        }
        const retryAfter = retryAfterMsOf(error)
        if (retryAfter !== undefined) {
          setRowResendUntil(Date.now() + retryAfter)
          setRowNow(Date.now())
        }
        throw error
      }
    })
  }

  /** Submit the row flow's code. The write proves the address the send just
   * named, so the refetched row loses its unverified chip. */
  function rowVerifyCode(nextCode: string): Promise<void> {
    const address = rowSentTo ?? rowVerify ?? ''
    setRowCodeInvalid(false)
    return feedback.run(async () => {
      setFailureText(null)
      try {
        // The row's verification proves the address and rotates this
        // session's credential, so the window is open before the write and
        // closes once the refetch that follows it has settled.
        await runIdentityTransition(
          session,
          () => verifyIdentifier.mutateAsync({ data: { code: nextCode } }),
          async () => {
            setRowVerify(null)
            setRowSentTo(null)
            setRowCode('')
            setRowResendUntil(0)
            await invalidateAfterWrite()
          },
        )
      } catch (error) {
        const failure = errorCodeOf(error)
        if (failure === STEP_UP_REQUIRED) {
          gate.arm({ step: 'rowVerify', code: nextCode })
          return null
        }
        if (failure === 'authn.login_failed') {
          // The code is the one value this flow can change: state the refusal
          // in its field instead of the section's banner.
          setRowCodeInvalid(true)
          setRowCode('')
          return null
        }
        if (failure === 'authn.email_taken') {
          // The address cannot be edited in this flow; the address-taken
          // answer means another account proved it in between, and naming
          // that is clearer than the code's account-state sentence.
          setFailureText('verification.addressTaken')
          return null
        }
        throw error
      }
      // The confirmation names the address it proved.
      return t('verification.verifiedDescription', {
        email: address,
        interpolation: { escapeValue: false },
      })
    })
  }

  const valueLabel =
    formKind === 'email'
      ? t('identifiers.add.value.labelEmail')
      : t('identifiers.add.value.labelUsername')
  const valueHelper =
    formKind === 'email'
      ? t('identifiers.add.value.helperEmail')
      : t('identifiers.add.value.helperUsername')

  // The deployment's human-verification block, above the send control of
  // whichever email flow is open. It renders nothing when no proof is
  // required.
  const humanBlock = (
    <HumanVerification
      gate={human.gate ?? undefined}
      proof={human.proof}
      status={human.status}
      onToken={human.adoptToken}
      onRetry={human.retry}
      disabled={addBusy}
    />
  )

  return (
    <>
      <SectionCard title={showHeader ? t('identifiers.title') : undefined}>
        <AccountAsyncSection
        pending={pending}
        payload={rows}
        empty={rows !== undefined && rows.length === 0}
        error={error}
        refreshing={isFetching}
        loading={<IdentifierListSkeleton label={t('identifiers.loading')} />}
        errorState={{
          title: t('identifiers.error.title'),
          description: t('identifiers.error.description'),
          retryLabel: t('identifiers.retry'),
          onRetry: () => void refetch(),
        }}
        emptyState={{
          title: t('identifiers.empty.title'),
          description: t('identifiers.empty.description'),
        }}
      >
        {(settledRows) => (
          <Box>
            {/* The rows are one real list, as the sessions and bindings
                rows are: a screen-reader user hears each identifier as one
                item of a numbered set. role="list" keeps the list
                semantics under WebKit, which strips them from a
                list-style-none ul. */}
            <Box component="ul" role="list" sx={{ m: 0, p: 0, listStyle: 'none' }}>
              {settledRows.map((identifier, index) => {
                const id = identifier.id ?? null
                const kind = identifier.kind ?? ''
                const kindLabel = KNOWN_IDENTIFIER_KINDS.has(kind)
                  ? t(`identifiers.kind.${kind}`)
                  : t('identifiers.kind.other')
                const value = identifier.value ?? ''
                // `proven` is the server's derivation of whether the way the
                // identifier was established proves ownership. `verified_at`
                // is present on every stored identifier (including a username
                // written at registration as `self`), so it cannot answer that
                // question.
                const verified = identifier.proven === true
                // The chip answers one question: has ownership of this
                // identifier been proved? Every unproven channel reads the
                // same way -- an administrator-asserted or imported address
                // is an address its holder has not proved, not a third state.
                const stateLabel = verified
                  ? t('identifiers.verified')
                  : t('identifiers.unverified')
                // A username proves no ownership, so it has no verification
                // state to show.
                const showChip = kind !== 'username'
                // An unverified email is provable from its own row, unless
                // the account-wide flow already owns that code (pending) or
                // the deployment has no channel to send it.
                const canVerifyRow =
                  rowVerifyAvailable && kind === 'email' && !verified
                const rowFlowOpen = canVerifyRow && rowVerify === value
                const lastLoginName =
                  passwordSet &&
                  loginNameCount === 1 &&
                  (kind === 'username' || kind === 'email')
                return (
                  <Box
                    component="li"
                    key={id ?? String(index)}
                    sx={{
                      py: 1.5,
                      minWidth: 0,
                      ...(index > 0
                        ? { borderTop: '1px solid', borderColor: 'divider' }
                        : {}),
                    }}
                  >
                    <Box
                      sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}
                    >
                      <Box sx={{ minWidth: 0 }}>
                        <Typography variant="body1" noWrap sx={{ fontWeight: 500 }}>
                          {value}
                        </Typography>
                        <Typography variant="body2" color="text.secondary">
                          {kindLabel}
                        </Typography>
                        {lastLoginName ? (
                          <Typography variant="body2" color="text.secondary">
                            {t('identifiers.lastLoginName')}
                          </Typography>
                        ) : null}
                      </Box>
                      <Box
                        sx={{
                          marginLeft: 'auto',
                          flexShrink: 0,
                          display: 'flex',
                          alignItems: 'center',
                          gap: 1,
                        }}
                      >
                        {showChip ? (
                          <Chip
                            size="small"
                            variant="outlined"
                            color={verified ? 'primary' : 'default'}
                            label={stateLabel}
                          />
                        ) : null}
                        {canVerifyRow ? (
                          <Button
                            variant="outlined"
                            size="small"
                            disabled={writeBusy || addBusy || rowFlowOpen}
                            aria-label={t('identifiers.verifyAria', {
                              value,
                              interpolation: { escapeValue: false },
                            })}
                            onClick={() => openRowVerify(value)}
                          >
                            {t('identifiers.verify')}
                          </Button>
                        ) : null}
                        {id !== null && !lastLoginName && (
                          <Button
                            variant="outlined"
                            color="error"
                            size="small"
                            disabled={writeBusy}
                            aria-label={t('identifiers.removeAria', {
                              value,
                              // The label is an aria-label, not HTML: the
                              // identifier's value (an email or a phone
                              // number with a '+') must reach the
                              // accessibility tree verbatim (i18next's
                              // default value escaping would embed an
                              // entity for it).
                              interpolation: { escapeValue: false },
                            })}
                            onClick={() => setRemoveTarget(id)}
                          >
                            {t('identifiers.remove')}
                          </Button>
                        )}
                      </Box>
                    </Box>
                    {rowFlowOpen ? (
                      <Box
                        component="form"
                        noValidate
                        onSubmit={(event) => {
                          event.preventDefault()
                          if (rowSentTo === null) {
                            void rowSend(value)
                            return
                          }
                          if (rowCode.trim() !== '') {
                            void rowVerifyCode(rowCode)
                          }
                        }}
                        sx={{ mt: 1.5, maxWidth: FORM_MAX_WIDTH }}
                      >
                        <Stack spacing={1} sx={{ alignItems: 'flex-start' }}>
                          <TextField
                            fullWidth
                            type="email"
                            label={t('identifiers.add.value.labelEmail')}
                            value={value}
                            helperText={t('identifiers.add.value.helperEmail')}
                            slotProps={{ htmlInput: { readOnly: true } }}
                            disabled={addBusy}
                          />
                          {humanBlock}
                          {rowSentTo === null ? null : (
                            <TextField
                              fullWidth
                              autoFocus
                              inputMode="numeric"
                              autoComplete="one-time-code"
                              label={t('identifiers.add.code.label')}
                              value={rowCode}
                              error={rowCodeInvalid}
                              helperText={
                                rowCodeInvalid
                                  ? t('identifiers.verifyCodeInvalid')
                                  : undefined
                              }
                              slotProps={{
                                htmlInput: {
                                  'aria-invalid': rowCodeInvalid,
                                },
                              }}
                              disabled={addBusy}
                              onChange={(event) => {
                                setRowCode(event.target.value)
                                if (rowCodeInvalid) {
                                  setRowCodeInvalid(false)
                                }
                              }}
                            />
                          )}
                          <Stack
                            direction="row"
                            spacing={1}
                            sx={{ flexWrap: 'wrap' }}
                          >
                            {rowSentTo === null ? (
                              <Button
                                type="submit"
                                variant="contained"
                                size="small"
                                disabled={addBusy || proofBlocked}
                                loading={addIdentifier.isPending}
                              >
                                {t('identifiers.add.submitEmail')}
                              </Button>
                            ) : (
                              <>
                                <Button
                                  type="submit"
                                  variant="contained"
                                  size="small"
                                  disabled={addBusy || rowCode.trim() === ''}
                                  loading={verifyIdentifier.isPending}
                                >
                                  {t('identifiers.add.verify')}
                                </Button>
                                <Button
                                  type="button"
                                  variant="outlined"
                                  size="small"
                                  disabled={
                                    addBusy ||
                                    proofBlocked ||
                                    rowRemainingSeconds > 0
                                  }
                                  loading={addIdentifier.isPending}
                                  aria-describedby={
                                    rowRemainingSeconds > 0
                                      ? ROW_RESEND_NOTE_ID
                                      : undefined
                                  }
                                  onClick={() => void rowSend(value)}
                                >
                                  {t('verification.resend')}
                                </Button>
                              </>
                            )}
                            <Button
                              type="button"
                              size="small"
                              disabled={addBusy}
                              onClick={closeRowVerify}
                            >
                              {t('identifiers.add.cancel')}
                            </Button>
                          </Stack>
                          {rowSentTo !== null && rowRemainingSeconds > 0 ? (
                            <Typography
                              id={ROW_RESEND_NOTE_ID}
                              variant="body2"
                              color="text.secondary"
                            >
                              {t('verification.resendIn', {
                                seconds: rowRemainingSeconds,
                              })}
                            </Typography>
                          ) : null}
                        </Stack>
                      </Box>
                    ) : null}
                  </Box>
                )
              })}
            </Box>
          </Box>
        )}
      </AccountAsyncSection>

      <Box sx={{ mt: 2 }}>
        {addKinds.length === 0 ? null : !addOpen ? (
          <Button
            variant="outlined"
            size="small"
            disabled={writeBusy || addBusy}
            onClick={() => {
              closeRowVerify()
              setFieldErrorKey(null)
              setAddOpen(true)
            }}
          >
            {t('identifiers.add.open')}
          </Button>
        ) : (
          <>
            <Typography variant="subtitle1" component="h3" sx={{ mb: 1 }}>
              {t('identifiers.add.title')}
            </Typography>
            <Box
              component="form"
              noValidate
              onSubmit={(event) => {
                event.preventDefault()
                if (sentTo !== null) {
                  void verify(code)
                  return
                }
                if (formKind === 'email') {
                  const address = parseEmailAddress(addValue)
                  if (address === null) {
                    // The client guard owns this attempt's result; a settled
                    // result from the previous attempt must not stand beside
                    // the field error.
                    clearFailure()
                    setFieldErrorKey('identifiers.add.invalidEmail')
                    return
                  }
                  void add('email', address)
                  return
                }
                const username = addValue.trim()
                if (username !== '') {
                  void add('username', username)
                }
              }}
              sx={{
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'flex-start',
                gap: 2,
                maxWidth: FORM_MAX_WIDTH,
                mt: 1,
              }}
            >
              {sentTo === null ? (
                <>
                  <TextField
                    select
                    fullWidth
                    label={t('identifiers.add.kind.label')}
                    value={formKind}
                    disabled={addBusy}
                    onChange={(event) => {
                      setAddKind(event.target.value as AddKind)
                      setFieldErrorKey(null)
                    }}
                  >
                    {addKinds.map((option) => (
                      <MenuItem key={option} value={option}>
                        {t(`identifiers.add.kind.${option}`)}
                      </MenuItem>
                    ))}
                  </TextField>
                  <TextField
                    fullWidth
                    type={formKind === 'email' ? 'email' : 'text'}
                    autoComplete={formKind === 'email' ? 'email' : 'username'}
                    label={valueLabel}
                    error={fieldErrorKey !== null}
                    helperText={
                      fieldErrorKey === null
                        ? valueHelper
                        : t(fieldErrorKey)
                    }
                    value={addValue}
                    disabled={addBusy}
                    onChange={(event) => {
                      setAddValue(event.target.value)
                      if (fieldErrorKey !== null) {
                        setFieldErrorKey(null)
                      }
                    }}
                  />
                  {formKind === 'email' ? humanBlock : null}
                  <Stack direction="row" spacing={1}>
                    <Button
                      type="submit"
                      variant="contained"
                      disabled={
                        addBusy ||
                        (formKind === 'email' && proofBlocked) ||
                        addValue.trim() === ''
                      }
                    >
                      {formKind === 'email'
                        ? t('identifiers.add.submitEmail')
                        : t('identifiers.add.submitUsername')}
                    </Button>
                    <Button type="button" disabled={addBusy} onClick={closeAdd}>
                      {t('identifiers.add.cancel')}
                    </Button>
                  </Stack>
                </>
              ) : (
                <>
                  {/* The destination text stays through the step; being the
                      live region itself, it announces the code's landing
                      once (empty, then filled one commit later). */}
                  <Typography
                    component="div"
                    role="status"
                    variant="body2"
                    color="text.secondary"
                  >
                    {sentCommitted
                      ? t('identifiers.add.sent', {
                          value: sentTo,
                          interpolation: { escapeValue: false },
                        })
                      : ''}
                  </Typography>
                  <TextField
                    fullWidth
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    label={t('identifiers.add.code.label')}
                    value={code}
                    inputRef={addCodeRef}
                    disabled={addBusy}
                    onChange={(event) => setCode(event.target.value)}
                  />
                  <Stack direction="row" spacing={1}>
                    <Button
                      type="submit"
                      variant="contained"
                      disabled={addBusy || code.trim() === ''}
                    >
                      {t('identifiers.add.verify')}
                    </Button>
                    <Button
                      type="button"
                      disabled={addBusy}
                      onClick={() => {
                        setSentTo(null)
                        setCode('')
                        setFieldErrorKey(null)
                        clearFailure()
                      }}
                    >
                      {t('identifiers.add.change')}
                    </Button>
                  </Stack>
                </>
              )}
            </Box>
          </>
        )}
      </Box>

      {/* The section's one result position, below the whole control group --
          the row actions and the add face whose writes share the slot -- so a
          success and a failure can never stand together. It stays outside
          AsyncSection so an empty list still shows it. The non-code failure
          sentences occupy the same position and are mutually exclusive with
          it. */}
      <WriteFeedbackRegion outcome={feedback.outcome} sx={{ mt: 1 }} />
      {failureText !== null && (
        <Alert severity="error" role="alert" sx={{ width: '100%', mt: 1 }}>
          {t(failureText)}
        </Alert>
      )}
      </SectionCard>

      <ConfirmDialog
        open={removeTarget !== null}
        title={t('identifiers.confirmTitle')}
        message={t('identifiers.confirmMessage')}
        variant="danger"
        confirmLabel={t('identifiers.confirmLabel')}
        confirmLoading={removeIdentifier.isPending}
        onCancel={() => setRemoveTarget(null)}
        onConfirm={() => {
          if (removeTarget !== null) {
            void remove(removeTarget)
          }
        }}
      />

      <StepUpChallenge
        open={gate.open}
        session={session}
        onSuccess={gate.succeed}
        onCancel={gate.cancel}
      />
    </>
  )
}
