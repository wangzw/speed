/**
 * IdentifiersSection: the identifier surface of the account page, grouped by
 * how the account uses an email address or phone number.
 *
 * The identifiers are the account's own, read off the profile
 * (GET /api/v1/auth/me, authn.profile.get): the spec ships no dedicated
 * identifier-list route, and the profile answer carries each identifier's
 * own id, kind, value, proven flag, whether it is a sign-in way and whether
 * it is the default contact. Each row
 * names the kind (email, phone or username, mapped through a guarded
 * vocabulary, a value outside it rendering the generic identifier label) and
 * the value, with the server's `proven` answer (the ways of establishing
 * ownership that actually prove it -- email-code, sms-code, invite,
 * idp:<provider> -- versus the ones that only record an assertion: admin,
 * import-unverified, self). `verified_at` cannot answer that question: every
 * stored identifier carries one, including a username written at
 * registration as `self`. A username is a login name with no ownership to
 * prove, so its row carries no ownership chip. An email or phone row carries
 * a two-value ownership chip: `proven` true reads `verified`, false reads
 * `unverified`, whatever channel established the identifier.
 *
 * The section splits the account's identifiers into two lists. The sign-in
 * ways list holds every username and every email address or phone number the
 * profile marks `sign_in`; the contacts list holds every registered email
 * address and phone number, because registering an address is what makes it
 * a contact -- the position is not a separate choice. The same record can
 * appear in both lists while it is also a sign-in way; whether an email or
 * phone signs in is a group decision, never a per-row switch, so the
 * contacts list carries no sign-in control. A username is always a sign-in
 * way, has no contact position, and is never in the contacts list.
 *
 * The sign-in ways list is grouped by kind, and each group renders its
 * controls and one sentence, never the addresses or numbers as rows: the
 * address a group chooses reads in the control itself. A username is a login
 * name by definition, so its group states that with the sentence naming the
 * username and offers no sign-in switch; the name is fixed, so the group also
 * states it cannot be changed or removed and renders no removal action. The
 * email group's switch
 * states whether the account uses email to sign in; while it is on, a picker
 * chooses one of the proved addresses, written as one step-up action so a won
 * challenge lands the same end state (the pre-chosen address turned off and
 * the new one turned on) rather than replaying only one of the row writes.
 * While it is off, the picker gives way to a note, and turning it back on
 * restores the address this visit switched off, then the default contact (the
 * address the account falls back to), then the earliest proved address,
 * through that same protected write. The phone group toggles its representative row, and
 * while that number does not sign in the group says so rather than naming it
 * as a sign-in way, so the sentence and the enable action agree.
 *
 * A host that splits the surface with `axis` gets the same blocks in the view
 * it shows: the contact axis's own table carries the default-contact choice
 * and the row actions on each address, and the sign-in axis renders the kind
 * groups alone, without the merged view's sign-in ways heading. The merged
 * view's contact list keeps the default-contact radio on the row and moves
 * verification and removal into the same trailing menu the table uses. The
 * control reads every row of its kind in both views; the login-ways page
 * keeps no per-address rows of its own.
 *
 * A group's sign-in write is PUT
 * /api/v1/auth/identifiers/{id} (authn.login.updateIdentifier) with
 * `sign_in`: turning it on moves the value into the sign-in ways list, and
 * turning it off only leaves that list (the value stays a contact). Only a
 * proved value can be put into sign-in (REQ-account-065); an unproved address
 * is still a contact, because registering it establishes that position. The
 * sign-in group write and the default contact need a fresh authentication, so
 * a refusal opens the step-up dialog like the section's other gated writes.
 * Putting a proved value into sign-in rotates this session's credential, so
 * that write runs inside an identity-transition window; taking it out does
 * not.
 *
 * A proved email address also offers the default-contact choice, a radio
 * that sets `default_contact: true` on the chosen row: the server clears the
 * previous default in the same write. The choice renders once per email
 * record, in the contacts list (the list that record's state makes
 * canonical), so one address never offers two controls for one position.
 *
 * A row whose identifier carries an id has a remove item in the row's
 * trailing overflow menu, sitting behind the ui-kit danger ConfirmDialog --
 * removing an identifier is irreversible (it is re-established only by
 * walking the route that proves it again). The removal is DELETE
 * /api/v1/auth/identifiers/{id}
 * (authn.login.removeIdentifier). The account module refuses to remove the
 * account's last way to log in with a 409, whose code text renders above
 * the list; a refused removal re-reads the profile, so a guard that read a
 * stale list converges on the server's answer. The removal does not replace
 * the caller's credential, so it runs outside the identity-transition
 * window that the credential-rotating writes below use. A username is
 * permanent and carries no removal action: a removal requested outside this
 * surface is refused with authn.username_immutable. An account with a password
 * signs in with a username or an email address chosen as a sign-in way, so
 * while exactly one such identifier is on the profile and it is an email, that
 * row's remove action gives way to a note, because removing it would leave the
 * password without a login name. Another such identifier, or an account with
 * no password at all, keeps the action enabled. Phone numbers are not counted
 * -- they sign in through their own code route. The server remains the
 * authority on the last way in; the note states the one case the profile can
 * reveal.
 *
 * An unverified email row carries a verify item in the same trailing menu as
 * its remove item (the remove item is withheld while the row is the password
 * account's only login name; the verify item is not). The action proves the
 * address the row already shows: POST /api/v1/auth/identifiers re-sends a code
 * to that same
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
 * Below the lists is the add face: POST /api/v1/auth/identifiers
 * (authn.login.addIdentifier) with a kind and a value. An account holds at
 * most one username, and the server answers a second one with the same
 * refusal as a name someone else took -- copy cannot distinguish them -- so
 * the add face does not offer the username kind once the profile lists one.
 * A username is a login name with no ownership to prove, so it is written
 * in one step; a username already in use is answered 409 (authn.username_taken,
 * or the identifier route's own authn.conflict for the same collision), and
 * because that is the expected outcome of the one write this face exists
 * for, the section names the username on its value field rather than
 * rendering the code's account-state sentence. An
 * email sends a code to it (the answer is state "pending") and the code is
 * submitted through POST /api/v1/auth/identifiers/verify
 * (authn.login.verifyIdentifier); only the matching code writes the address,
 * with the proof. A new address joins the contacts list at once and is not a
 * sign-in way. The email branch is
 * unavailable on a deployment with no email channel (a 503). The one-step
 * username write replaces this session's credential and revokes the
 * account's other sessions, so its success refetches the profile and the
 * session list; the email send and the code's verification do neither, so
 * they refetch only the profile. An address another account has reserved or
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
 * than the account-state banner; a username's 400 (a reserved name, or a
 * shape it cannot normalize) renders the value field's own message. The
 * three refusals are distinguishable: taken, reserved, and shape. A
 * bad-request that is not the username's own shape answer stays the
 * banner's: on this route it may describe another value. Closing
 * the add face clears the whole attempt: the failure banner, the field
 * error, the success notice and the sent-code step.
 *
 * Only the writes that change how the account signs in ask for a fresh
 * authentication: the one-step username add, putting a proved value into
 * sign-in, setting the default contact, removing an identifier and a
 * verification whose proof changes how the account signs in. A 401
 * authn.insufficient_user_authentication opens the step-up dialog with the
 * write's inputs remembered, and a won challenge re-runs that one write
 * exactly once. The email send and the row resend carry no fresh
 * authentication of their own, so a step-up refusal that reached them would
 * render the code's own failure text instead of opening a dialog. Proving an
 * address normally rotates nothing and asks for nothing, but the consequence
 * the server attaches to the proof -- an address that would become the
 * default contact or the account's only address -- can still answer the
 * step-up refusal; the add face's verify and the row's verify then arm the
 * dialog with the same code and replay it once a challenge is won, without
 * opening an identity-transition window (the verification still rotates no
 * credential). A successful write refetches the profile so the rows converge
 * on the server's answer. The one-step username add and putting a value into
 * sign-in replace this session's credential as they commit, so each runs
 * inside an identity-transition window (session.beginIdentityTransition)
 * that closes once the refetch that follows the write has settled; a read
 * still carrying the outgoing credential is discarded instead of ending the
 * session. The email send and the code's verification rotate nothing and run
 * outside the window. Only the username add and putting a value into sign-in
 * refetch the session list, because those are the writes that revoke the
 * account's other sessions.
 *
 * The section has one failure slot: removal, add, row verification, the
 * sign-in switch and the default-contact choice share it, so a failure renders
 * exactly one role="alert" banner. The slot is one control group -- the row
 * actions and the add face are one section -- so its region sits below the
 * add face, after the whole group.
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

import type { ReactElement } from 'react'
import { useEffect, useId, useRef, useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Chip from '@mui/material/Chip'
import FormControlLabel from '@mui/material/FormControlLabel'
import Menu from '@mui/material/Menu'
import MenuItem from '@mui/material/MenuItem'
import Radio from '@mui/material/Radio'
import Table from '@mui/material/Table'
import TableBody from '@mui/material/TableBody'
import TableCell from '@mui/material/TableCell'
import TableRow from '@mui/material/TableRow'
import Select from '@mui/material/Select'
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
  useAuthnLoginUpdateIdentifier,
  useAuthnLoginVerifyIdentifier,
} from '@speed/api-sdk'
import type { AuthnProfileIdentifier } from '@speed/api-sdk'
import type { AuthSession, AuthnHumanGate } from '@speed/auth-core'
import { humanGateNeedsProof, stepUpReasonOf, useStepUpGate, useHumanVerification } from '@speed/auth-core'
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
  /**
   * The host's current path, sent as the step-up flow's `return_to`: a
   * redirect method's round trip lands back on the page that started it, so
   * that page reads the refusal. Omitted, the deployment's default return
   * target applies.
   */
  readonly returnPath?: string
  /**
   * Which axis this occurrence manages. 'both' (the default) offers the
   * sign-in switch, the default-contact choice and both lists; 'signIn'
   * removes the contact controls and the contacts list, for a host that
   * shows the contact surface elsewhere; 'contact' removes the sign-in
   * switch and the sign-in list, for the same reason in the other direction.
   */
  readonly axis?: 'both' | 'signIn' | 'contact'
  /**
   * Whether the section renders its own add entry. False leaves the action to
   * the host, which opens the form through `openAddSignal`.
   */
  readonly showAddButton?: boolean
  /**
   * A counter the host increments to open the add form; every change after
   * the first opens it.
   */
  readonly openAddSignal?: number
}

interface ContactActionsMenuProps {
  readonly value: string
  readonly canVerify: boolean
  /** Set while this row's verification flow is already open, so its item
   * cannot start the same flow again. */
  readonly verifyDisabled?: boolean
  readonly canDefault?: boolean
  readonly isDefault?: boolean
  readonly canRemove?: boolean
  readonly disabled: boolean
  readonly onVerify: () => void
  readonly onSetDefault?: () => void
  readonly onRemove: () => void
}

/** One contact row's overflow menu: verify it, make it the default, or
 * remove it. A contacts-table row offers the default item; a merged-view
 * row does not, because its radio already carries that choice. */
function ContactActionsMenu({
  value,
  canVerify,
  verifyDisabled = false,
  canDefault = false,
  isDefault = false,
  canRemove = true,
  disabled,
  onVerify,
  onSetDefault,
  onRemove,
}: ContactActionsMenuProps): ReactElement {
  const { t } = useAccountUiTranslation()
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  return (
    <>
      <Button
        variant="outlined"
        size="small"
        disabled={disabled}
        aria-label={t('identifiers.actionsAria', {
          value,
          interpolation: { escapeValue: false },
        })}
        onClick={(event) => setAnchor(event.currentTarget)}
      >
        <Box
          component="svg"
          aria-hidden="true"
          focusable="false"
          viewBox="0 0 24 24"
          width={18}
          height={18}
          fill="currentColor"
        >
          <circle cx="5" cy="12" r="1.8" />
          <circle cx="12" cy="12" r="1.8" />
          <circle cx="19" cy="12" r="1.8" />
        </Box>
      </Button>
      <Menu
        anchorEl={anchor}
        open={anchor !== null}
        onClose={() => setAnchor(null)}
      >
        {canVerify ? (
          <MenuItem
            disabled={verifyDisabled}
            onClick={() => {
              setAnchor(null)
              onVerify()
            }}
          >
            {t('identifiers.verify')}
          </MenuItem>
        ) : null}
        {canDefault && onSetDefault !== undefined ? (
          // The current default is marked, not greyed: `selected` says
          // "this is the one in force", where a disabled item left the
          // reader guessing why it could not be chosen.
          <MenuItem
            selected={isDefault}
            onClick={() => {
              setAnchor(null)
              if (!isDefault) {
                onSetDefault()
              }
            }}
          >
            {t('identifiers.axes.defaultContact')}
          </MenuItem>
        ) : null}
        {canRemove ? (
          <MenuItem
            sx={{ color: 'error.main' }}
            onClick={() => {
              setAnchor(null)
              onRemove()
            }}
          >
            {t('identifiers.remove')}
          </MenuItem>
        ) : null}
      </Menu>
    </>
  )
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

/** The change the sign-in switch asks for. */
type AxisChange = {
  readonly signIn: boolean
}

/** One row's end state in a sign-in-email selection. The whole selection is
 * one step-up action, so a won challenge replays every row it needs. */
type SignInSelectionChange = {
  readonly id: string
  readonly value: string
  readonly signIn: boolean
}

/** The two lists the identifiers are grouped into. A username is always a
 * sign-in way; every registered email address or phone number is a contact. */
type IdentifierGroup = 'signIn' | 'contact'

/** The write a step-up dialog is armed for, so a won challenge re-runs
 * exactly it: a removal, the username add, the sign-in switch of a row, the
 * default-contact choice, either verification step or a whole sign-in-email
 * selection. The code-send steps never arm the gate. */
type SectionAction =
  | { readonly step: 'remove'; readonly id: string }
  | { readonly step: 'add'; readonly kind: AddKind; readonly value: string }
  | {
      readonly step: 'flags'
      readonly id: string
      readonly value: string
      readonly change: AxisChange
    }
  | { readonly step: 'defaultContact'; readonly id: string; readonly value: string }
  | { readonly step: 'verifyCode'; readonly code: string }
  | { readonly step: 'rowVerifyCode'; readonly code: string }
  | {
      readonly step: 'selectSignInEmail'
      readonly changes: readonly SignInSelectionChange[]
    }

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

/** The grouping facts one profile row carries. `sign_in` can be true on an
 * unproved row written under the old behaviour, so the axis's presence and
 * the row's proved state are read separately: the list membership follows
 * the server's flag, the sign-in switch is offered only where the value is
 * proved. */
function axesOf(row: {
  readonly kind?: string
  readonly sign_in?: boolean
  readonly default_contact?: boolean
}): {
  readonly username: boolean
  readonly signIn: boolean
  readonly defaultContact: boolean
} {
  const username = row.kind === 'username'
  return {
    username,
    signIn: username || row.sign_in === true,
    defaultContact: row.default_contact === true,
  }
}

/** Whether a row belongs to the contacts list: every registered email
 * address and phone number. A username is a login name only. */
function isContact(row: { readonly kind?: string }): boolean {
  return row.kind !== 'username'
}

export function IdentifiersSection({
  session,
  emailAvailable = true,
  offerUsername = true,
  humanGate,
  returnPath,
  axis = 'both',
  showAddButton = true,
  openAddSignal,
}: IdentifiersSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, isFetching, error, refetch } = useProfileQuery()
  const removeIdentifier = useAuthnLoginRemoveIdentifier()
  const lastAddSignal = useRef(openAddSignal)
  useEffect(() => {
    if (
      openAddSignal !== undefined &&
      openAddSignal !== lastAddSignal.current
    ) {
      lastAddSignal.current = openAddSignal
      setAddOpen(true)
    }
  }, [openAddSignal])
  const addIdentifier = useAuthnLoginAddIdentifier()
  const verifyIdentifier = useAuthnLoginVerifyIdentifier()
  const updateIdentifier = useAuthnLoginUpdateIdentifier()
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
  // The address whose email sign-in this visit switched off. Turning the
  // group back on restores it before falling back to the default contact, so
  // the viewer's own last choice wins. Component-local memory: it is not
  // persisted, and it never outlives the mount.
  const [lastDisabledSignInEmail, setLastDisabledSignInEmail] = useState<{
    readonly id: string
    readonly value: string
  } | null>(null)
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

  // The row flow's own state. rowVerify is the row whose verification is open
  // (null when none is); rowSentTo is that address once its code has been
  // requested; rowCode is the answer; rowCodeInvalid marks a refused code in
  // the field. rowResendUntil / rowNow drive the resend countdown. The key
  // names the list occurrence that opened the flow, so a record in both
  // lists shows the form in the row the viewer acted on.
  const [rowVerify, setRowVerify] = useState<{
    readonly key: string
    readonly value: string
  } | null>(null)
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
  // The add face's value input, focused when the server refuses the value.
  const valueInput = useRef<HTMLInputElement>(null)
  // A refusal asks for focus once the attempt has settled: the value field
  // is disabled while a write is in flight, so focusing it from the
  // refusal itself would be dropped.
  const focusValuePending = useRef(false)
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
  const writeBusy =
    removeIdentifier.isPending || updateIdentifier.isPending || feedback.pending
  useEffect(() => {
    if (addBusy || !focusValuePending.current) {
      return
    }
    focusValuePending.current = false
    valueInput.current?.focus()
  }, [addBusy])

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
      case 'flags':
        void setFlags(action.id, action.value, action.change)
        return
      case 'defaultContact':
        void setDefaultContact(action.id, action.value)
        return
      case 'verifyCode':
        void verify(action.code)
        return
      case 'rowVerifyCode':
        void rowVerifyCode(action.code)
        return
      case 'selectSignInEmail':
        void selectSignInEmail(action.changes)
        return
    }
  })

  const pending = isPending && data === undefined
  const rows = !pending && data !== undefined ? data.identifiers : undefined
  // A password signs an account in through a username or an email address
  // chosen as a sign-in way. With exactly one of those on the profile, that
  // row's removal is withheld and explained; the server still judges the last
  // way in on the write itself, so this is the visible half of the same rule.
  const passwordSet = data?.password_set === true
  const loginNameCount =
    rows === undefined
      ? 0
      : rows.filter(
          (row) => row.kind === 'username' || (row.kind === 'email' && row.sign_in),
        ).length
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
  // On the contact axis the host opens the add face from the block header:
  // while the face is open it stands where the list stood, so the form
  // replaces the list the way the phone block's form replaces its row,
  // rather than sitting below a whole table of addresses. The sign-in axis
  // keeps its own in-section Add button and the list above it.
  const addFaceReplacesList =
    axis === 'contact' && addOpen && addKinds.length > 0
  // A settled read that lists none, or one whose body omitted the key,
  // hands the section to the EmptyState, whose title stands in for the
  // hidden h2 header at its own level (headingLevel="h2" below) -- the
  // heading order never skips a level, and "no identifiers" is only ever
  // claimed for an answer that genuinely listed none.
  const showHeader = pending || (rows !== undefined && rows.length > 0)

  /** Refetch the profile alone. Registering or proving a contact address, the
   * default-contact choice and a removal change only what the profile
   * reports: none of them replaces this session's credential or revokes the
   * account's other sessions. */
  async function invalidateProfile(): Promise<void> {
    await queryClient.invalidateQueries({ queryKey: getAuthnProfileGetQueryKey() })
  }

  /** Refetch what a credential-changing write changes: the profile's
   * identifiers, and the session list, because the one-step username add and
   * putting a value into sign-in revoke the account's other sessions. */
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
        const reason = stepUpReasonOf(failure)
        if (reason !== null) {
          gate.arm({ step: 'remove', id }, reason)
          return null
        }
        // The refusal is the server's judgement on the current state, which
        // this mount's guard may have read stale (another session removed a
        // login name first): re-read so the list and the withheld-removal
        // note converge on what the server sees.
        await invalidateProfile()
        throw error
      }
      setRemoveTarget(null)
      await invalidateProfile()
      // Translated when the attempt settles; it names the identifier removed.
      return t('identifiers.removed', {
        value: identifierValue,
        interpolation: { escapeValue: false },
      })
    })
  }

  /** Write a row's sign-in axis. Putting a proved value into sign-in rotates
   * the caller's credential, so that write runs inside the identity-transition
   * window; taking it out does not. A refusal re-reads the profile, so a
   * selection the client offered but the server rejects converges on the
   * server's answer. */
  function setFlags(
    id: string,
    value: string,
    change: AxisChange,
  ): Promise<void> {
    return feedback.run(async () => {
      setFailureText(null)
      const write = () =>
        updateIdentifier.mutateAsync({
          id,
          data: { sign_in: change.signIn },
        })
      try {
        if (change.signIn) {
          await runIdentityTransition(session, write, invalidateAfterWrite)
        } else {
          await write()
          await invalidateProfile()
        }
      } catch (error) {
        const reason = stepUpReasonOf(errorCodeOf(error))
        if (reason !== null) {
          gate.arm({ step: 'flags', id, value, change }, reason)
          return null
        }
        await invalidateProfile()
        throw error
      }
      return t('identifiers.axes.signInChanged', {
        value,
        interpolation: { escapeValue: false },
      })
    })
  }

  /** Set which proved email address is a sign-in way. The whole selection is
   * one attempt, so a step-up refusal arms the gate with every row it needs
   * and a won challenge lands the same end state -- not only the one row that
   * met the refusal. The chosen address is written first (it rotates this
   * session's credential, so the writes run inside the identity-transition
   * window whose release waits for the refetch); the addresses it replaces
   * are then turned off. A selection that only turns addresses off rotates
   * nothing and re-reads the profile alone. */
  function selectSignInEmail(
    changes: readonly SignInSelectionChange[],
  ): Promise<void> {
    const chosen = changes.find((change) => change.signIn)
    return feedback.run(async () => {
      setFailureText(null)
      const write = async (): Promise<void> => {
        if (chosen !== undefined) {
          await updateIdentifier.mutateAsync({
            id: chosen.id,
            data: { sign_in: true },
          })
        }
        for (const change of changes) {
          if (change.signIn) {
            continue
          }
          await updateIdentifier.mutateAsync({
            id: change.id,
            data: { sign_in: false },
          })
        }
      }
      try {
        if (chosen !== undefined) {
          await runIdentityTransition(session, write, invalidateAfterWrite)
        } else {
          await write()
          await invalidateProfile()
        }
      } catch (error) {
        const reason = stepUpReasonOf(errorCodeOf(error))
        if (reason !== null) {
          gate.arm({ step: 'selectSignInEmail', changes }, reason)
          return null
        }
        await invalidateProfile()
        throw error
      }
      return t('identifiers.axes.signInChanged', {
        value: chosen?.value ?? changes[0]?.value ?? '',
        interpolation: { escapeValue: false },
      })
    })
  }

  /** Make one proved email address the account's default contact. The server
   * clears the previous choice in the same write, which is not a credential
   * rotation. The write needs a fresh authentication like every change of
   * what proves the account. */
  function setDefaultContact(id: string, value: string): Promise<void> {
    return feedback.run(async () => {
      setFailureText(null)
      try {
        await updateIdentifier.mutateAsync({ id, data: { default_contact: true } })
      } catch (error) {
        const reason = stepUpReasonOf(errorCodeOf(error))
        if (reason !== null) {
          gate.arm({ step: 'defaultContact', id, value }, reason)
          return null
        }
        await invalidateProfile()
        throw error
      }
      await invalidateProfile()
      return t('identifiers.axes.defaultChanged', {
        value,
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
        await (kind === 'username' ? invalidateAfterWrite() : invalidateProfile())
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
        // Only the username write is a change of what proves the account;
        // the email add registers a contact and is never gated.
        const reason = kind === 'username' ? stepUpReasonOf(failure) : null
        if (reason !== null) {
          // A step-up refusal precedes the proof check, so the proof is
          // still valid for the re-run.
          gate.arm({ step: 'add', kind, value }, reason)
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
        if (
          kind === 'username' &&
          (failure === 'authn.username_taken' || failure === 'authn.conflict')
        ) {
          // An existing username is the expected 409 of the one write this
          // face exists for. The identifier route keeps its own conflict
          // answer for the same collision; both mean the name is not
          // available, and the name is the value the viewer changes, so the
          // field carries the message.
          setFieldErrorKey('identifiers.add.usernameTaken')
          focusValuePending.current = true
          return null
        }
        if (
          kind === 'username' &&
          (failure === 'authn.username_reserved' ||
            failure === 'authn.username_invalid')
        ) {
          // A login name the system keeps for itself, or one the server
          // cannot normalize: the name is the value this face can change,
          // so the field carries the message.
          setFieldErrorKey(
            failure === 'authn.username_reserved'
              ? 'identifiers.add.usernameReserved'
              : 'identifiers.add.usernameInvalid',
          )
          focusValuePending.current = true
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
        // Proving an address carries no fresh authentication of its own and
        // rotates nothing, so it opens no window. The consequence the server
        // attaches to the proof -- an address that becomes the default
        // contact or the account's only address -- can still answer a step-up
        // refusal, which arms the gate with the same code.
        await verifyIdentifier.mutateAsync({ data: { code: nextCode } })
        closeAdd()
        await invalidateProfile()
      } catch (error) {
        const failure = errorCodeOf(error)
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
        const reason = stepUpReasonOf(failure)
        if (reason !== null) {
          gate.arm({ step: 'verifyCode', code: nextCode }, reason)
          return null
        }
        throw error
      }
      return t('identifiers.add.added')
    })
  }

  /** Open a row's verification flow on the address the row already shows.
   * The add face closes, because one code flow runs at a time on this
   * section. `key` names the list occurrence that opened it. */
  function openRowVerify(key: string, value: string): void {
    closeAdd()
    setRowCodeInvalid(false)
    setRowCode('')
    setRowSentTo(null)
    setRowResendUntil(0)
    setRowVerify({ key, value })
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
    const address = rowSentTo ?? rowVerify?.value ?? ''
    setRowCodeInvalid(false)
    return feedback.run(async () => {
      setFailureText(null)
      try {
        // The row's verification proves the address without rotating the
        // session or revoking the others, so it runs outside the window. The
        // consequence the proof carries -- an address that becomes the only
        // address, hence the automatic login way -- can still answer a step-up
        // refusal, which arms the gate with the same code.
        await verifyIdentifier.mutateAsync({ data: { code: nextCode } })
        setRowVerify(null)
        setRowSentTo(null)
        setRowCode('')
        setRowResendUntil(0)
        await invalidateProfile()
      } catch (error) {
        const failure = errorCodeOf(error)
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
        const reason = stepUpReasonOf(failure)
        if (reason !== null) {
          gate.arm({ step: 'rowVerifyCode', code: nextCode }, reason)
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

  // The lists, in the order the section renders them, with the keys their
  // headings and empty notes read. The contact axis's own view renders its
  // table instead, so it reads no group here. The sign-in axis renders one
  // list per kind; the merged view renders the same kind lists inside its
  // sign-in ways list, so an email or phone choice reads the same in both
  // views, and keeps the contacts list beside it.
  interface GroupSpec {
    readonly key: IdentifierGroup
    /** Set when the list is one identifier kind rather than one axis. */
    readonly kind?: string
    readonly title: string
    readonly empty: string
  }
  const signInKindSpecs: readonly GroupSpec[] = [
    {
      key: 'signIn',
      kind: 'username',
      title: 'identifiers.kind.username',
      empty: 'identifiers.groups.signInEmpty',
    },
    {
      key: 'signIn',
      kind: 'email',
      title: 'identifiers.kind.email',
      empty: 'identifiers.groups.signInEmpty',
    },
    {
      key: 'signIn',
      kind: 'phone',
      title: 'identifiers.kind.phone',
      empty: 'identifiers.groups.signInEmpty',
    },
  ]
  const groups: readonly GroupSpec[] =
    axis === 'signIn'
      ? signInKindSpecs
      : axis === 'contact'
        ? [
            {
              key: 'contact',
              title: 'identifiers.groups.contact',
              empty: 'identifiers.groups.contactEmpty',
            },
          ]
        : [
            {
              key: 'signIn',
              title: 'identifiers.groups.signIn',
              empty: 'identifiers.groups.signInEmpty',
            },
            {
              key: 'contact',
              title: 'identifiers.groups.contact',
              empty: 'identifiers.groups.contactEmpty',
            },
          ]
  const groupIdBase = useId()

  /**
   * One sign-in kind group: the group-level choice for that kind and the
   * sentence stating what it chose. Neither view renders addresses or
   * numbers as rows here -- the picker displays the chosen address, and the
   * sentence names the username or phone. The sign-in axis names the group
   * with its kind heading; the merged view renders the same block inside its
   * sign-in ways list under the kind's accessible name, so an email or phone
   * choice reads the same in both views. A username has no control, the email
   * group has a toggle plus a picker, and the phone group has a toggle of its
   * representative row.
   */
  function renderSignInKind(
    spec: GroupSpec,
    allRows: readonly AuthnProfileIdentifier[],
  ) {
    const kind = spec.kind ?? ''
    const headingId = `${groupIdBase}-${kind}`
    // The control reads every row of the kind: the email picker offers all
    // proved addresses, and the phone toggle targets the representative row.
    // The group renders no address rows of its own: the picker is where the
    // chosen address reads, and the sentence states the username or phone.
    const kindRows = allRows.filter((row) => (row.kind ?? '') === kind)
    const signInRow =
      kindRows.find((row) => row.sign_in === true) ??
      kindRows.find((row) => row.proven === true) ??
      kindRows[0] ??
      null
    // A username is always a login name, so its group carries no sign-in
    // switch and no removal action: the name is permanent, and the group
    // states that below the sentence naming it. The server refuses a removal
    // requested outside this surface with authn.username_immutable.
    const usernameRow = kind === 'username' ? (kindRows[0] ?? null) : null
    const emailSignInRow =
      kind === 'email'
        ? (kindRows.find(
            (row) => row.id != null && row.sign_in === true,
          ) ?? null)
        : null
    const provenEmails =
      kind === 'email'
        ? kindRows.filter((row) => row.proven === true && row.id != null)
        : []
    const emailOptions =
      kind === 'email'
        ? kindRows.filter(
            (row) => row.proven === true || row.sign_in === true,
          )
        : []
    const named = axis === 'signIn'
    return (
      <Box
        key={kind}
        role="group"
        aria-labelledby={named ? headingId : undefined}
        aria-label={named ? undefined : t(spec.title)}
        aria-describedby={
          kind === 'email' && emailSignInRow === null
            ? `${headingId}-off`
            : undefined
        }
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}>
          {named ? (
            <Typography
              id={headingId}
              component="h3"
              variant="body1"
              sx={{ fontWeight: 600 }}
            >
              {t(spec.title)}
            </Typography>
          ) : null}
          {kind === 'username' ? null : kind === 'email' ? (
            <Button
              variant="outlined"
              size="small"
              disabled={
                writeBusy ||
                addBusy ||
                (emailSignInRow === null && provenEmails.length === 0)
              }
              aria-describedby={
                emailSignInRow === null ? `${headingId}-off` : undefined
              }
              sx={{ marginLeft: 'auto' }}
              onClick={() => {
                if (emailSignInRow !== null) {
                  setLastDisabledSignInEmail({
                    id: emailSignInRow.id,
                    value: emailSignInRow.value ?? '',
                  })
                  void setFlags(
                    emailSignInRow.id,
                    emailSignInRow.value ?? '',
                    { signIn: false },
                  )
                  return
                }
                const remembered =
                  lastDisabledSignInEmail === null
                    ? undefined
                    : provenEmails.find(
                        (row) =>
                          row.id === lastDisabledSignInEmail.id ||
                          (row.value ?? '') ===
                            lastDisabledSignInEmail.value,
                      )
                const target =
                  remembered ??
                  provenEmails.find(
                    (row) => row.default_contact === true,
                  ) ??
                  provenEmails[0]
                if (target === undefined) {
                  return
                }
                setLastDisabledSignInEmail(null)
                void setFlags(target.id, target.value ?? '', {
                  signIn: true,
                })
              }}
            >
              {emailSignInRow === null
                ? t('identifiers.action.enable')
                : t('identifiers.action.disable')}
            </Button>
          ) : signInRow?.id != null ? (
            <Button
              variant="outlined"
              size="small"
              disabled={writeBusy || addBusy}
              sx={{ marginLeft: 'auto' }}
              onClick={() =>
                void setFlags(signInRow.id as string, signInRow.value ?? '', {
                  signIn: signInRow.sign_in !== true,
                })
              }
            >
              {signInRow.sign_in === true
                ? t('identifiers.action.disable')
                : t('identifiers.action.enable')}
            </Button>
          ) : null}
        </Box>
        {kind === 'email' ? (
          emailSignInRow !== null ? (
            <Select
              size="small"
              value={emailSignInRow.value ?? ''}
              disabled={writeBusy || addBusy}
              slotProps={{
                input: {
                  'aria-label': t('identifiers.kind.email'),
                },
              }}
              onChange={(event) => {
                const chosen = event.target.value
                // The whole selection is one action: every row that reaches
                // the chosen end state is written together, so a step-up
                // replay lands that same state instead of only the first row.
                const changes = kindRows
                  .filter((row) => row.id != null)
                  .map((row) => ({
                    id: row.id as string,
                    value: row.value ?? '',
                    signIn: (row.value ?? '') === chosen,
                  }))
                  .filter((change) => {
                    const row = kindRows.find(
                      (candidate) => candidate.id === change.id,
                    )
                    return (row?.sign_in === true) !== change.signIn
                  })
                if (changes.length > 0) {
                  void selectSignInEmail(changes)
                }
              }}
              sx={{ mt: 1, minWidth: '18rem' }}
            >
              {emailOptions.map((row) => (
                <MenuItem key={row.id ?? row.value} value={row.value ?? ''}>
                  {row.value ?? ''}
                </MenuItem>
              ))}
            </Select>
          ) : (
            <Typography
              id={`${headingId}-off`}
              variant="body2"
              color="text.secondary"
              sx={{ mt: 0.5 }}
            >
              {t('identifiers.usage.emailNone')}
            </Typography>
          )
        ) : (
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: 1.5,
              mt: 0.5,
            }}
          >
            <Typography variant="body2" color="text.secondary">
              {signInRow === null || signInRow.value === undefined ? (
                t(
                  kind === 'username'
                    ? 'identifiers.usage.usernameNone'
                    : 'identifiers.usage.phoneNone',
                )
              ) : kind === 'username' || signInRow.sign_in === true ? (
                (() => {
                  const marker = '__VALUE__'
                  const sentence = t('identifiers.usage.loginAs', {
                    kind: t(spec.title),
                    value: marker,
                  })
                  const [before, after = ''] = sentence.split(marker)
                  return (
                    <>
                      {before}
                      <Box
                        component="span"
                        sx={{ color: 'text.primary', fontWeight: 600 }}
                      >
                        {signInRow.value}
                      </Box>
                      {after}
                    </>
                  )
                })()
              ) : (
                // The button offers to enable this phone, so the sentence must
                // state the same thing: the number is not used to sign in.
                t('identifiers.usage.phoneNotSignIn')
              )}
            </Typography>
          </Box>
        )}
        {usernameRow !== null ? (
          <Typography
            variant="body2"
            color="text.secondary"
            sx={{ mt: 0.5 }}
          >
            {t('identifiers.usernamePermanent')}
          </Typography>
        ) : null}
      </Box>
    )
  }

  /**
   * The row verification form both views render: the row's own address,
   * fixed; the human-verification block; the code field once a code is out;
   * and one action row whose controls keep one order -- cancel on the left,
   * the resend in the middle once a code is out, and the primary send or
   * verify closing the row on the right. After a send the resend counts down
   * and a note states the wait.
   */
  function renderRowVerifyForm(address: string) {
    return (
      <Box
        component="form"
        noValidate
        onSubmit={(event) => {
          event.preventDefault()
          if (rowSentTo === null) {
            void rowSend(address)
            return
          }
          if (rowCode.trim() !== '') {
            void rowVerifyCode(rowCode)
          }
        }}
        sx={{ mt: 1.5, maxWidth: FORM_MAX_WIDTH }}
      >
        <Stack spacing={1.5} sx={{ alignItems: 'flex-start' }}>
          <TextField
            fullWidth
            type="email"
            label={t('identifiers.add.value.labelEmail')}
            value={address}
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
                rowCodeInvalid ? t('identifiers.verifyCodeInvalid') : undefined
              }
              slotProps={{ htmlInput: { 'aria-invalid': rowCodeInvalid } }}
              disabled={addBusy}
              onChange={(event) => {
                setRowCode(event.target.value)
                if (rowCodeInvalid) {
                  setRowCodeInvalid(false)
                }
              }}
            />
          )}
          <Box
            sx={{
              display: 'flex',
              flexWrap: 'wrap',
              alignItems: 'center',
              gap: 1,
              width: '100%',
            }}
          >
            <Button
              type="button"
              size="small"
              disabled={addBusy}
              onClick={closeRowVerify}
            >
              {t('identifiers.add.cancel')}
            </Button>
            {rowSentTo === null ? null : (
              <Button
                type="button"
                variant="outlined"
                size="small"
                disabled={addBusy || proofBlocked || rowRemainingSeconds > 0}
                loading={addIdentifier.isPending}
                aria-describedby={
                  rowRemainingSeconds > 0 ? ROW_RESEND_NOTE_ID : undefined
                }
                onClick={() => void rowSend(address)}
              >
                {t('verification.resend')}
              </Button>
            )}
            {rowSentTo === null ? (
              <Button
                type="submit"
                variant="contained"
                size="small"
                sx={{ marginLeft: 'auto' }}
                disabled={addBusy || proofBlocked}
                loading={addIdentifier.isPending}
              >
                {t('identifiers.add.submitEmail')}
              </Button>
            ) : (
              <Button
                type="submit"
                variant="contained"
                size="small"
                sx={{ marginLeft: 'auto' }}
                disabled={addBusy || rowCode.trim() === ''}
                loading={verifyIdentifier.isPending}
              >
                {t('identifiers.add.verify')}
              </Button>
            )}
          </Box>
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
    )
  }

  /**
   * One row in the contacts list. The per-record actions (the
   * default-contact choice, verification and removal) render once per
   * record, so one address never offers the same action twice: the
   * default-contact radio stands below the row, and verification and
   * removal sit in the row's trailing overflow menu. Whether an email or
   * phone signs in is a group decision, so no row carries a switch for it.
   */
  function renderContactRow(
    identifier: AuthnProfileIdentifier,
    index: number,
  ) {
    const id = identifier.id ?? null
    const kind = identifier.kind ?? ''
    const kindLabel = KNOWN_IDENTIFIER_KINDS.has(kind)
      ? t(`identifiers.kind.${kind}`)
      : t('identifiers.kind.other')
    const value = identifier.value ?? ''
    // `proven` is the server's derivation of whether the way the identifier
    // was established proves ownership. `verified_at` is present on every
    // stored identifier (including a username written at registration as
    // `self`), so it cannot answer that question.
    const verified = identifier.proven === true
    // The chip answers one question: has ownership of this identifier been
    // proved? Every unproven channel reads the same way -- an
    // administrator-asserted or imported address is an address its holder
    // has not proved, not a third state.
    const stateLabel = verified
      ? t('identifiers.verified')
      : t('identifiers.unverified')
    // A username proves no ownership, so it has no verification state to
    // show.
    const showChip = kind !== 'username'
    const axes = axesOf(identifier)
    const rowKey = `contact:${id ?? index}`
    // An unverified email is provable from its own row, unless the
    // account-wide flow already owns that code (pending) or the deployment
    // has no channel to send it.
    const canVerifyRow = rowVerifyAvailable && kind === 'email' && !verified
    const rowFlowOpen = canVerifyRow && rowVerify?.key === rowKey
    const lastLoginName =
      passwordSet &&
      loginNameCount === 1 &&
      kind === 'email' &&
      axes.signIn
    const canRemoveRow = id !== null && !lastLoginName
    const showActionsMenu = canVerifyRow || canRemoveRow
    const disabled = writeBusy || addBusy || id === null
    return (
      <Box
        component="li"
        key={rowKey}
        sx={{ py: 1.5, minWidth: 0 }}
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}>
          <Box sx={{ minWidth: 0 }}>
            <Typography variant="body1" noWrap sx={{ fontWeight: 600 }}>
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
            {showActionsMenu ? (
              <ContactActionsMenu
                value={value}
                canVerify={canVerifyRow}
                verifyDisabled={addBusy || rowFlowOpen}
                canRemove={canRemoveRow}
                disabled={writeBusy}
                onVerify={() => openRowVerify(rowKey, value)}
                onRemove={() => {
                  if (id !== null) {
                    setRemoveTarget(id)
                  }
                }}
              />
            ) : null}
          </Box>
        </Box>
        {kind === 'email' && verified ? (
          <FormControlLabel
            control={
              <Radio
                size="small"
                checked={axes.defaultContact}
                disabled={disabled}
                slotProps={{
                  input: {
                    name: 'authn-default-contact',
                    'aria-label': t('identifiers.axes.defaultAria', {
                      value,
                      interpolation: { escapeValue: false },
                    }),
                  },
                }}
                onChange={() => {
                  if (id !== null && !axes.defaultContact) {
                    void setDefaultContact(id, value)
                  }
                }}
              />
            }
            label={t('identifiers.axes.defaultContact')}
          />
        ) : null}
        {rowFlowOpen ? renderRowVerifyForm(value) : null}
      </Box>
    )
  }

  return (
    <>
      <SectionCard title={showHeader ? t('identifiers.title') : undefined}>
        {addFaceReplacesList ? null : (
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
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
            {axis === 'contact' ? (
              <>
              <Table
                size="small"
                sx={{
                  '& td': { border: 0, padding: '8px 0' },
                  '& td + td': { paddingLeft: 16 },
                }}
              >
                <TableBody>
                  {settledRows
                    .filter((row) => (row.kind ?? '') === 'email')
                    .map((row, index) => {
                      const id = row.id ?? null
                      const kind = row.kind ?? ''
                      const verified = row.proven === true
                      const axes = axesOf(row)
                      return (
                        <TableRow key={id ?? index}>
                          {/* The address column carries the host's contact
                              tables' shared width (the phone table beside
                              this one writes the same value), so the badge
                              and action columns of both tables land on one
                              set of rails. */}
                          <TableCell sx={{ width: '11rem' }}>
                            {row.value ?? ''}
                          </TableCell>
                          <TableCell>
                            <Stack
                              direction="row"
                              spacing={1}
                              sx={{ alignItems: 'center' }}
                            >
                              <Chip
                                size="small"
                                variant="outlined"
                                color={verified ? 'primary' : 'default'}
                                label={
                                  verified
                                    ? t('identifiers.verified')
                                    : t('identifiers.unverified')
                                }
                              />
                              {axes.defaultContact ? (
                                <Chip
                                  size="small"
                                  variant="outlined"
                                  label={t('identifiers.axes.defaultContact')}
                                />
                              ) : null}
                            </Stack>
                          </TableCell>
                          <TableCell align="right">
                            {id !== null ? (
                              <ContactActionsMenu
                                value={row.value ?? ''}
                                canVerify={
                                  kind === 'email' &&
                                  !verified &&
                                  rowVerifyAvailable
                                }
                                canDefault={kind === 'email' && verified}
                                isDefault={axes.defaultContact}
                                disabled={writeBusy || addBusy}
                                onVerify={() =>
                                  openRowVerify(
                                    `contact:${id}`,
                                    row.value ?? '',
                                  )
                                }
                                onSetDefault={() =>
                                  void setDefaultContact(id, row.value ?? '')
                                }
                                onRemove={() => setRemoveTarget(id)}
                              />
                            ) : null}
                          </TableCell>
                        </TableRow>
                      )
                    })}
                </TableBody>
              </Table>
              {rowVerify !== null && rowVerify.key.startsWith('contact:')
                ? renderRowVerifyForm(rowVerify.value)
                : null}
              </>
            ) : (
              groups.map((group) => {
              // A sign-in list is one per kind over one axis, so the axis key
              // alone would repeat: the kind keeps the React key and the
              // heading id unique.
              const groupKey = group.kind ?? group.key
              const headingId = `${groupIdBase}-${groupKey}`
              // The sign-in axis renders its lists without headings (the
              // host's own block names them), so it must not point
              // aria-labelledby at an id no element carries.
              const showHeading =
                axis !== 'signIn' && (axis === 'both' || groups.length > 1)
              if (group.key === 'signIn') {
                // The sign-in axis has one list per kind; the merged view
                // renders the same blocks inside its sign-in ways list, one
                // per kind the profile holds a row for.
                const kindSpecs =
                  axis === 'signIn'
                    ? [group]
                    : signInKindSpecs.filter((spec) =>
                        settledRows.some(
                          (row) => (row.kind ?? '') === (spec.kind ?? ''),
                        ),
                      )
                return (
                  <Box
                    component="section"
                    key={groupKey}
                    aria-labelledby={showHeading ? headingId : undefined}
                  >
                    {axis === 'signIn' ? null : (
                      <Typography
                        id={headingId}
                        component="h3"
                        variant="subtitle1"
                        sx={{ mb: 0.5 }}
                      >
                        {t(group.title)}
                      </Typography>
                    )}
                    <Box
                      sx={{
                        display: 'flex',
                        flexDirection: 'column',
                        gap: 2,
                      }}
                    >
                      {kindSpecs.map((spec) =>
                        renderSignInKind(spec, settledRows),
                      )}
                    </Box>
                  </Box>
                )
              }
              const contactRows = settledRows.filter(isContact)
              return (
                <Box
                  component="section"
                  key={groupKey}
                  aria-labelledby={showHeading ? headingId : undefined}
                >
                  <Typography
                    id={headingId}
                    component="h3"
                    variant="subtitle1"
                    sx={{ mb: 0.5 }}
                  >
                    {t(group.title)}
                  </Typography>
                  {contactRows.length === 0 ? (
                    <Typography variant="body2" color="text.secondary">
                      {t(group.empty)}
                    </Typography>
                  ) : (
                    <Box
                      component="ul"
                      role="list"
                      sx={{ m: 0, p: 0, listStyle: 'none' }}
                    >
                      {contactRows.map((identifier, index) =>
                        renderContactRow(identifier, index),
                      )}
                    </Box>
                  )}
                </Box>
              )
              })
            )}
          </Box>
        )}
      </AccountAsyncSection>
        )}

      <Box sx={{ mt: 2 }}>
        {axis === 'signIn' || addKinds.length === 0 ? null : !addOpen ? (
          showAddButton ? (
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
          ) : null
        ) : (
          <>
            <Typography variant="subtitle1" component="h3" sx={{ mb: 1 }}>
              {axis === 'contact'
                ? t('identifiers.addEmail.title')
                : t('identifiers.add.title')}
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
                if (axis === 'contact' || formKind === 'email') {
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
                  {axis === 'contact' ? null : (
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
                  )}
                  <TextField
                    fullWidth
                    type={
                      axis === 'contact' || formKind === 'email'
                        ? 'email'
                        : 'text'
                    }
                    autoComplete={
                      axis === 'contact' || formKind === 'email'
                        ? 'email'
                        : 'username'
                    }
                    inputRef={valueInput}
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
                  {axis === 'contact' || formKind === 'email'
                    ? humanBlock
                    : null}
                  {/* Cancel on the left, the primary submit closing the
                      row on the right, as every inline form in the package
                      does. */}
                  <Box
                    sx={{
                      display: 'flex',
                      flexWrap: 'wrap',
                      alignItems: 'center',
                      gap: 1,
                      width: '100%',
                    }}
                  >
                    <Button type="button" disabled={addBusy} onClick={closeAdd}>
                      {t('identifiers.add.cancel')}
                    </Button>
                    <Button
                      type="submit"
                      variant="contained"
                      sx={{ marginLeft: 'auto' }}
                      disabled={
                        addBusy ||
                        ((axis === 'contact' || formKind === 'email') &&
                          proofBlocked) ||
                        addValue.trim() === ''
                      }
                    >
                      {axis === 'contact' || formKind === 'email'
                        ? t('identifiers.add.submitEmail')
                        : t('identifiers.add.submitUsername')}
                    </Button>
                  </Box>
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
                  <Box
                    sx={{
                      display: 'flex',
                      flexWrap: 'wrap',
                      alignItems: 'center',
                      gap: 1,
                      width: '100%',
                    }}
                  >
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
                    <Button
                      type="submit"
                      variant="contained"
                      sx={{ marginLeft: 'auto' }}
                      disabled={addBusy || code.trim() === ''}
                    >
                      {t('identifiers.add.verify')}
                    </Button>
                  </Box>
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
        reason={gate.reason}
        session={session}
        onSuccess={gate.succeed}
        onCancel={gate.cancel}
        returnPath={returnPath}
      />
    </>
  )
}
