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
 * ownership that actually prove it -- email-code, sms-code,
 * idp:<provider> -- versus the ones that only record an assertion: invite,
 * admin, import-unverified, self). `verified_at` cannot answer that question: every
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
 * view's contact list keeps the default-contact radio on the row and renders
 * verification and removal as the same row actions the table uses. The
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
 * A row whose identifier carries an id has a remove action beside its other
 * row actions, sitting behind the ui-kit danger ConfirmDialog -- removing an
 * identifier is irreversible (it is re-established only by walking the route
 * that proves it again). The removal is DELETE
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
 * An unverified email row carries a verify action beside its remove action
 * (the removal is withheld while the row is the password account's only login
 * name; the verification is not). The action proves the
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
 * deployment with no email channel, which could not send the code. After a
 * send the row's resend is held for the server's interval; when the send
 * succeeded, the row's own note states that wait. A throttled attempt's
 * hold gets no note: its failure banner names the wait and counts it down.
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
 * exactly once. When the deployment cannot raise the session at all, the
 * dialog's re-login exit records the write in the shared step-up resume
 * store -- the page and the operation, for the removals and the sign-in
 * switches that carry no one-time code -- and the surface replays it once
 * when it mounts again after the new sign-in; a code step and the add
 * face's typed address are left for the person to redo. The email send and
 * the row resend carry no fresh
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
 * document (or from a `humanGate` a host already read). The block appears
 * only once an email send has been asked for: the add face and a row show
 * no challenge until then, and the send the viewer asked for leaves by
 * itself when the proof arrives. The control that asked gives way to the
 * block while the proof is outstanding. A username write and every
 * verification send no message, so they present none and are not gated by
 * the block. Each landing or refused send obtains a fresh proof, because
 * the single-use one was spent.
 */

import { Fragment } from 'react'
import type { FormEvent, ReactElement } from 'react'
import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Chip from '@mui/material/Chip'
import FormControlLabel from '@mui/material/FormControlLabel'
import MenuItem from '@mui/material/MenuItem'
import Radio from '@mui/material/Radio'
import Table from '@mui/material/Table'
import TableBody from '@mui/material/TableBody'
import TableCell from '@mui/material/TableCell'
import TableRow from '@mui/material/TableRow'
import Select from '@mui/material/Select'
import Skeleton from '@mui/material/Skeleton'
import Stack from '@mui/material/Stack'
import Switch from '@mui/material/Switch'
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
  ActionButton,
  ActionRow,
  AnnouncementRegion,
  ConfirmDialog,
  errorCodeOf,
  FORM_MAX_WIDTH,
  HumanVerification,
  ListSkeleton,
  parseEmailAddress,
  SectionCard,
  useActivationGuard,
  useWriteFeedback,
  visuallyHiddenSx,
} from '@speed/ui-kit'
import { AccountAsyncSection } from './internal/async-section.js'
import { runIdentityTransition } from './internal/identity-transition.js'
import { useProfileQuery } from './internal/queries.js'
import { retryAfterMsOf } from './internal/retry-after.js'
import {
  rememberStepUpResume,
  takeStepUpResume,
} from './internal/step-up-resume.js'
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
   * that page reads the refusal. It is also the page a re-login returns to
   * and replays the recorded write on. Omitted, the deployment's default
   * return target applies.
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

type RowActionKey = 'verify' | 'defaultContact' | 'remove'

interface ContactActionsProps {
  readonly canVerify: boolean
  /** Set while this row's verification flow is already open, so its action
   * cannot start the same flow again. */
  readonly verifyOpen?: boolean
  /** Why the verify action cannot start now, when its own flow is open. */
  readonly verifyOpenReason?: string
  readonly canDefault?: boolean
  readonly isDefault?: boolean
  readonly canRemove?: boolean
  /**
   * The action of this row whose own write is in flight. Only that action
   * reports busy; the row's other actions and its siblings are unavailable
   * while the scope's write runs.
   */
  readonly busyAction?: RowActionKey
  /** A write in this scope is in flight, so the actions cannot run now. */
  readonly scopeUnavailable?: boolean
  readonly onVerify: () => void
  readonly onSetDefault?: () => void
  readonly onRemove: () => void
  /** Each action's own control, so a landed write can find the neighbour
   * that replaces a control leaving the document. */
  readonly onVerifyRef?: (node: HTMLButtonElement | null) => void
  readonly onDefaultRef?: (node: HTMLButtonElement | null) => void
  readonly onRemoveRef?: (node: HTMLButtonElement | null) => void
}

/** One action of a row, with the state its own control carries. */
interface RowAction {
  readonly key: RowActionKey
  readonly label: string
  /** This action's own write is in flight. */
  readonly busy: boolean
  /** The action cannot run now: its own precondition is unmet, or a write in
   * the scope is in flight. */
  readonly unavailable: boolean
  /** Given only when the action's own precondition, not the scope's write,
   * stops it. */
  readonly unavailableReason?: string
  readonly tone?: 'danger'
  readonly onClick: () => void
  readonly refCallback?: (node: HTMLButtonElement | null) => void
}

/** One contact row's actions in their fixed order: verify it, make it the
 * default, remove it. A contacts-table row offers the default action only
 * while it is not already the default -- the row's chip already states that
 * position -- and a merged-view row does not, because its radio already
 * carries that choice. A row with one action renders that action alone; a row
 * with several renders one small action row; a row with none renders
 * nothing. */
function ContactActions({
  canVerify,
  verifyOpen = false,
  verifyOpenReason,
  canDefault = false,
  isDefault = false,
  canRemove = true,
  busyAction,
  scopeUnavailable = false,
  onVerify,
  onSetDefault,
  onRemove,
  onVerifyRef,
  onDefaultRef,
  onRemoveRef,
}: ContactActionsProps): ReactElement | null {
  const { t } = useAccountUiTranslation()
  const actions: RowAction[] = []
  if (canVerify) {
    actions.push({
      key: 'verify',
      label: t('identifiers.verify'),
      busy: false,
      unavailable: scopeUnavailable || verifyOpen,
      ...(verifyOpen && verifyOpenReason !== undefined
        ? { unavailableReason: verifyOpenReason }
        : {}),
      onClick: onVerify,
      ...(onVerifyRef === undefined ? {} : { refCallback: onVerifyRef }),
    })
  }
  if (canDefault && onSetDefault !== undefined && !isDefault) {
    // The row already carries the "Default contact" chip when it is the
    // default, so the action would repeat that position without offering
    // anything: it is absent rather than rendered unavailable.
    actions.push({
      key: 'defaultContact',
      label: t('identifiers.axes.defaultContact'),
      busy: busyAction === 'defaultContact',
      unavailable: scopeUnavailable && busyAction !== 'defaultContact',
      onClick: onSetDefault,
      ...(onDefaultRef === undefined ? {} : { refCallback: onDefaultRef }),
    })
  }
  if (canRemove) {
    actions.push({
      key: 'remove',
      label: t('identifiers.remove'),
      busy: busyAction === 'remove',
      unavailable: scopeUnavailable && busyAction !== 'remove',
      tone: 'danger',
      onClick: onRemove,
      ...(onRemoveRef === undefined ? {} : { refCallback: onRemoveRef }),
    })
  }
  if (actions.length === 0) {
    return null
  }
  const button = (action: RowAction) => (
    <ActionButton
      ref={action.refCallback}
      size="small"
      tone={action.tone}
      busy={action.busy}
      unavailable={action.unavailable}
      unavailableReason={action.unavailableReason}
      onClick={action.onClick}
    >
      {action.label}
    </ActionButton>
  )
  if (actions.length === 1) {
    const action = actions[0]
    if (action === undefined) {
      return null
    }
    return button(action)
  }
  return (
    <ActionRow size="small">
      {actions.map((action) => (
        <Fragment key={action.key}>{button(action)}</Fragment>
      ))}
    </ActionRow>
  )
}

interface SignInSwitchProps {
  readonly checked: boolean
  readonly label: string
  /** Existing page text that explains an unmet precondition. */
  readonly describedBy?: string
  /** This switch's own write is in flight. */
  readonly busy: boolean
  /** The switch cannot run now (its own precondition, or a scope write). */
  readonly unavailable: boolean
  readonly onChange: () => void
}

/**
 * One sign-in switch. It saves on change: while its own write is in flight,
 * or while another write in the scope runs, the change is swallowed and the
 * input reports `aria-disabled` instead of turning natively disabled, so a
 * keyboard user keeps their place.
 */
function SignInSwitch({
  checked,
  label,
  describedBy,
  busy,
  unavailable,
  onChange,
}: SignInSwitchProps): ReactElement {
  const guardedChange = useActivationGuard(onChange, { busy, unavailable })
  return (
    <Switch
      checked={checked}
      slotProps={{
        input: {
          'aria-label': label,
          'aria-describedby': describedBy,
          'aria-disabled': busy || unavailable ? 'true' : undefined,
        },
      }}
      onChange={guardedChange}
    />
  )
}

interface DefaultContactChoiceProps {
  readonly checked: boolean
  readonly label: string
  readonly ariaLabel: string
  /** The radio cannot run now: its own write is in flight, a scope write is
   * in flight, or the row carries no identifier id. */
  readonly unavailable: boolean
  /** Translated reason for an unmet precondition; omitted for a scope
   * write, whose unavailability has no reason to give. */
  readonly unavailableReason?: string
  readonly onChange: () => void
}

/**
 * The default-contact radio on one email row. It saves on change, so the
 * input reports `aria-disabled` and the change is guarded instead of the
 * input turning natively disabled; an unmet precondition is explained by a
 * visually hidden sentence the input points at.
 */
function DefaultContactChoice({
  checked,
  label,
  ariaLabel,
  unavailable,
  unavailableReason,
  onChange,
}: DefaultContactChoiceProps): ReactElement {
  const reasonId = useId()
  const describedBy =
    unavailable && unavailableReason !== undefined ? reasonId : undefined
  const guardedChange = useActivationGuard(onChange, {
    busy: false,
    unavailable,
  })
  return (
    <>
      <FormControlLabel
        control={
          <Radio
            size="small"
            checked={checked}
            slotProps={{
              input: {
                name: 'authn-default-contact',
                'aria-label': ariaLabel,
                'aria-disabled': unavailable ? 'true' : undefined,
                'aria-describedby': describedBy,
              },
            }}
            onChange={guardedChange}
          />
        }
        label={label}
      />
      {describedBy === undefined ? null : (
        <Box component="span" id={reasonId} sx={visuallyHiddenSx}>
          {unavailableReason}
        </Box>
      )}
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

/** The email send a pending proof is waiting on: the add face's address, or
 * a row's resend to the address it already shows. */
type PendingSend =
  | { readonly step: 'add'; readonly value: string }
  | { readonly step: 'row'; readonly address: string }

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

/** The update write in flight, and which control started it, so only that
 * control reports busy and its siblings report unavailable. */
type PendingUpdate =
  | {
      readonly kind: 'flags'
      readonly id: string
      readonly via: 'emailSwitch' | 'phoneSwitch'
    }
  | { readonly kind: 'select' }
  | { readonly kind: 'default'; readonly id: string }

/** Where focus belongs when a write takes its initiating control out of the
 * document: the recorded order names the neighbour, the control is what
 * tells a focus the write took from a focus the operator moved away. */
interface LandingRecord {
  readonly id: string
  readonly order: readonly string[]
  readonly control: HTMLElement | null
}

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

/** The row flow's resend note id, referenced by the resend control while it
 * counts down. */
const ROW_RESEND_NOTE_ID = 'identifiers-row-resend-note'

/** The scope this surface's re-login resume record carries. */
const RESUME_SCOPE = 'identifiers'

/**
 * The recorded operation as this section's own action, or null when the step
 * carries a value that must not be persisted. The two verification steps
 * carry a one-time code and the add face carries the address the person
 * typed; neither is recorded, so after a new sign-in the person redoes them.
 * The remaining writes (a removal, the sign-in switches, the default contact
 * and a whole sign-in-email selection) are pure requests over identifiers the
 * list already names.
 */
function resumableAction(value: unknown): SectionAction | null {
  if (typeof value !== 'object' || value === null) {
    return null
  }
  const action = value as {
    readonly step?: unknown
    readonly id?: unknown
    readonly value?: unknown
    readonly change?: unknown
    readonly changes?: unknown
  }
  switch (action.step) {
    case 'remove':
      return typeof action.id === 'string'
        ? { step: 'remove', id: action.id }
        : null
    case 'defaultContact':
      return typeof action.id === 'string' && typeof action.value === 'string'
        ? { step: 'defaultContact', id: action.id, value: action.value }
        : null
    case 'flags': {
      const change = action.change
      if (
        typeof action.id !== 'string' ||
        typeof action.value !== 'string' ||
        typeof change !== 'object' ||
        change === null ||
        typeof (change as { readonly signIn?: unknown }).signIn !== 'boolean'
      ) {
        return null
      }
      return {
        step: 'flags',
        id: action.id,
        value: action.value,
        change: { signIn: (change as { readonly signIn: boolean }).signIn },
      }
    }
    case 'selectSignInEmail': {
      if (!Array.isArray(action.changes)) {
        return null
      }
      const changes: SignInSelectionChange[] = []
      for (const entry of action.changes) {
        if (typeof entry !== 'object' || entry === null) {
          return null
        }
        const row = entry as {
          readonly id?: unknown
          readonly value?: unknown
          readonly signIn?: unknown
        }
        if (
          typeof row.id !== 'string' ||
          typeof row.value !== 'string' ||
          typeof row.signIn !== 'boolean'
        ) {
          return null
        }
        changes.push({ id: row.id, value: row.value, signIn: row.signIn })
      }
      return { step: 'selectSignInEmail', changes }
    }
    default:
      return null
  }
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
      openAdd()
    }
  }, [openAddSignal])
  const addIdentifier = useAuthnLoginAddIdentifier()
  const verifyIdentifier = useAuthnLoginVerifyIdentifier()
  const updateIdentifier = useAuthnLoginUpdateIdentifier()
  // The email send waiting on a proof the gate requires: the add face's
  // address, or a row's resend. The gate is read and the proof obtained
  // only while this is set, so neither face shows a challenge until its
  // send is asked for. Once the proof arrives the send leaves by itself.
  const [pendingSend, setPendingSend] = useState<PendingSend | null>(null)
  // The deployment's human-verification gate and this section's proof.
  // Only an email code send presents it: a username write and every
  // verification send no message.
  // Only an email code send presents a proof here, and an address is not a
  // phone number: the section runs the other surface's mechanism.
  const human = useHumanVerification(session, {
    scope: 'other',
    enabled: pendingSend !== null,
    ...(humanGate === undefined ? {} : { gate: humanGate }),
  })
  // A host that already read the discovery document hands the gate in; a
  // standalone section learns it through the hook once a send asks for one.
  const gateDocument = humanGate ?? human.gate
  const needsProof = humanGateNeedsProof(gateDocument, 'other')
  // The last-line guard of the send itself: a required proof that is not
  // in hand keeps an email send from leaving.
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

  // The row whose removal is its own write in flight, and the update write's
  // initiating control (the sign-in switch, the picker, or a default-contact
  // choice). Only the initiating control reports busy; every sibling reports
  // unavailable without a reason while the scope's write runs.
  const [pendingRemoveId, setPendingRemoveId] = useState<string | null>(null)
  const [pendingUpdate, setPendingUpdate] = useState<PendingUpdate | null>(null)
  const removeAttempt = useRef(0)
  const updateAttempt = useRef(0)
  /** Marks this attempt as the one in flight, and returns the release that
   * clears it only when no later attempt has taken over. */
  function beginRemove(id: string): () => void {
    const token = removeAttempt.current + 1
    removeAttempt.current = token
    setPendingRemoveId(id)
    return () => {
      if (removeAttempt.current === token) {
        setPendingRemoveId(null)
      }
    }
  }
  function beginUpdate(value: PendingUpdate): () => void {
    const token = updateAttempt.current + 1
    updateAttempt.current = token
    setPendingUpdate(value)
    return () => {
      if (updateAttempt.current === token) {
        setPendingUpdate(null)
      }
    }
  }

  // The rendered row actions, by identifier id or row key, so a landed write
  // can hand focus to the neighbour that replaces a control leaving the
  // document. The add region is the group container a write with no
  // neighbour falls back to.
  const removeButtons = useRef(new Map<string, HTMLButtonElement>())
  const defaultButtons = useRef(new Map<string, HTMLButtonElement>())
  const verifyButtons = useRef(new Map<string, HTMLButtonElement>())
  const addEntry = useRef<HTMLButtonElement | null>(null)
  const addRegion = useRef<HTMLDivElement | null>(null)

  // The writes that take their initiating control out of the document. Each
  // record is made before the write and consumed by the layout effect that
  // runs once the control leaves.
  const removalLanding = useRef<LandingRecord | null>(null)
  const defaultLanding = useRef<LandingRecord | null>(null)
  const verifyLanding = useRef<LandingRecord | null>(null)
  const addLanding = useRef<LandingRecord | null>(null)
  const addForm = useRef<HTMLFormElement | null>(null)
  const rowVerifyForm = useRef<HTMLFormElement | null>(null)
  // Set on the success that closes a form, so the layout effect can tell
  // that close from the operator's own cancel.
  const addLanded = useRef(false)
  // Whether the add face was open on the previous commit, so the opening
  // commit -- not every render while it stays open -- moves focus in.
  const addWasOpen = useRef(false)
  const verifyLanded = useRef(false)
  // The code step's own field: focus returns to the value field when the
  // operator goes back to it.
  const sentToBefore = useRef<string | null>(null)

  // The add face's own state. addOpen shows the form; addKind picks the
  // kind; addValue holds the entry; sentTo is the address a code went to,
  // or null while the value is being entered; code is the answer.
  // fieldErrorKey is the value field's own error (an ill-formed email, the
  // server's bad-request answer to one, or an address another account
  // already holds), rendered under that field rather than in the banner.
  const [addOpen, setAddOpen] = useState(false)
  // Whether the re-read that follows a landed add has settled. The form
  // closes as soon as the write lands; the landing waits for this, because
  // the entry that reopens the form is decided by the re-read's answer.
  const [addReadSettled, setAddReadSettled] = useState(true)
  const [addKind, setAddKind] = useState<AddKind>(
    emailAvailable ? 'email' : 'username',
  )
  const [addValue, setAddValue] = useState('')
  const [sentTo, setSentTo] = useState<string | null>(null)
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
  // Whether that hold came from a landed send's `resend_after`. Only that
  // hold gets the row's note: a hold a throttled failure started is
  // explained by the shared banner, which counts the same wait down.
  const [rowResendFromSuccess, setRowResendFromSuccess] = useState(false)
  const [rowNow, setRowNow] = useState(() => Date.now())

  // Hand focus to the code field when the code step mounts. The sent
  // notice's live region handles its own empty birth frame; it is empty
  // again when the flow returns to the value field, because the step's
  // region unmounts with it.
  const addCodeRef = useRef<HTMLInputElement>(null)
  // The add face's value input, focused when the server refuses the value.
  const valueInput = useRef<HTMLInputElement>(null)
  // A refusal asks for focus once the attempt has settled: the value field
  // is read-only while a write is in flight, and the refusal also re-reads
  // the profile, so moving focus from the refusal itself would race the
  // refetch.
  const focusValuePending = useRef(false)
  useEffect(() => {
    if (sentTo !== null) {
      addCodeRef.current?.focus()
    }
  }, [sentTo])

  // In flight through the whole attempt, including the refetches that follow
  // the mutation, so no control changes context under a pending scope.
  const addBusy =
    addIdentifier.isPending || verifyIdentifier.isPending || feedback.pending
  const writeBusy =
    removeIdentifier.isPending || updateIdentifier.isPending || feedback.pending
  // A send waiting on its human-verification proof is not a write in flight:
  // only the send or resend control that asked waits, and the ways out of the
  // wait -- cancel, back, a switch, the code already in hand -- stay usable.
  const scopeBlocked = writeBusy || addBusy
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
      case 'flags': {
        // The replay has no click of its own: the row the action names says
        // which switch started it, so that switch reports busy while the
        // replayed write runs.
        const via =
          (rows ?? []).find((row) => row.id === action.id)?.kind === 'phone'
            ? 'phoneSwitch'
            : 'emailSwitch'
        void setFlags(action.id, action.value, action.change, via)
        return
      }
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

  // The re-login exit records the write a new sign-in must re-issue. Only the
  // operations with no one-time code are recorded; the add face's typed
  // address and the two code steps are left for the person to redo. The record
  // is bound to the account the session snapshot carries, so it never replays
  // for an account a later sign-in lands on.
  const rememberRelogin = useCallback(() => {
    if (returnPath === undefined) {
      return
    }
    const action = gate.peek()
    if (action !== null && resumableAction(action) !== null) {
      rememberStepUpResume(
        session.getSnapshot().principal?.account_id ?? '',
        RESUME_SCOPE,
        returnPath,
        action,
      )
    }
  }, [returnPath, gate.peek, session])

  // The page a re-login returns to replays the recorded write once, through
  // the gate's own replay, so a second shortfall opens the challenge again.
  // The profile must have loaded: a removal's confirmation names the row from
  // the list it acted on.
  const resumed = useRef(false)
  useEffect(() => {
    if (resumed.current || returnPath === undefined || data === undefined) {
      return
    }
    resumed.current = true
    const account = session.getSnapshot().principal?.account_id ?? ''
    const record = takeStepUpResume(account, RESUME_SCOPE)
    if (record === null) {
      return
    }
    if (record.path !== returnPath) {
      rememberStepUpResume(
        record.account,
        record.scope,
        record.path,
        record.operation,
      )
      return
    }
    const action = resumableAction(record.operation)
    if (action === null) {
      return
    }
    gate.arm(action)
    gate.succeed()
  }, [returnPath, data, gate, session])

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
  // renders the sign-in groups alone and offers no add face, so it never
  // reaches this branch.
  const addFaceReplacesList =
    axis === 'contact' && addOpen && addKinds.length > 0
  // A settled read that lists none, or one whose body omitted the key,
  // hands the section to the EmptyState, whose title stands in for the
  // hidden h2 header at its own level (headingLevel="h2" below) -- the
  // heading order never skips a level, and "no identifiers" is only ever
  // claimed for an answer that genuinely listed none.
  const showHeader = pending || (rows !== undefined && rows.length > 0)

  /** The identifier ids the current view renders as contact rows, in their
   * rendered order: the contact axis's email table, or the merged view's
   * contact list. */
  function visibleIds(all: readonly AuthnProfileIdentifier[]): string[] {
    const visible =
      axis === 'contact'
        ? all.filter((row) => (row.kind ?? '') === 'email')
        : all.filter(isContact)
    return visible
      .filter((row) => row.id != null)
      .map((row) => row.id as string)
  }

  /** The row keys the current view's verification actions carry. */
  function visibleVerifyKeys(
    all: readonly AuthnProfileIdentifier[],
  ): string[] {
    const visible =
      axis === 'contact'
        ? all.filter((row) => (row.kind ?? '') === 'email')
        : all.filter(isContact)
    return visible.map((row, index) => `contact:${row.id ?? index}`)
  }

  // A landed removal takes its row out of the document. Focus goes to the
  // next row's remove control, else the previous one, else the section's
  // container -- in the commit that removes the row, and only when the
  // removal is what took focus; an operator who moved elsewhere keeps it.
  useLayoutEffect(() => {
    const target = removalLanding.current
    if (target === null) {
      return
    }
    const remaining = new Set(visibleIds(rows ?? []))
    if (remaining.has(target.id)) {
      return
    }
    removalLanding.current = null
    if (!focusFellWith(target.control)) {
      return
    }
    const index = target.order.indexOf(target.id)
    const candidates =
      index === -1
        ? [...target.order]
        : [
            ...target.order.slice(index + 1),
            ...target.order.slice(0, index).reverse(),
          ]
    for (const id of candidates) {
      if (!remaining.has(id)) {
        continue
      }
      const button = removeButtons.current.get(id)
      if (button !== undefined) {
        button.focus()
        return
      }
    }
    addRegion.current?.focus()
  }, [rows])

  // A landed default-contact action stops existing on its row, which stays.
  // Focus goes to the same action on a neighbour, else the section's
  // container, under the same focus test as a removal.
  useLayoutEffect(() => {
    const target = defaultLanding.current
    if (target === null) {
      return
    }
    const settled = (rows ?? []).find((row) => row.id === target.id)
    if (settled === undefined) {
      defaultLanding.current = null
      return
    }
    if (settled.default_contact !== true) {
      return
    }
    defaultLanding.current = null
    if (!focusFellWith(target.control)) {
      return
    }
    const index = target.order.indexOf(target.id)
    const candidates =
      index === -1
        ? [...target.order]
        : [
            ...target.order.slice(index + 1),
            ...target.order.slice(0, index).reverse(),
          ]
    for (const id of candidates) {
      const button = defaultButtons.current.get(id)
      if (button !== undefined && button.isConnected) {
        button.focus()
        return
      }
    }
    addRegion.current?.focus()
  }, [rows])

  // A landed row verification closes its form and, once the profile
  // refreshes, drops the row's verify action. Focus goes to the next row's
  // verify action, else the previous one, else the section's container.
  useLayoutEffect(() => {
    if (!verifyLanded.current || rowVerify !== null) {
      return
    }
    verifyLanded.current = false
    const target = verifyLanding.current
    verifyLanding.current = null
    if (target === null) {
      return
    }
    if (!focusFellWith(target.control)) {
      return
    }
    const index = target.order.indexOf(target.id)
    const candidates =
      index === -1
        ? [...target.order]
        : [
            ...target.order.slice(index + 1),
            ...target.order.slice(0, index).reverse(),
          ]
    for (const key of candidates) {
      if (key === target.id) {
        continue
      }
      const button = verifyButtons.current.get(key)
      if (button !== undefined && button.isConnected) {
        button.focus()
        return
      }
    }
    addRegion.current?.focus()
  }, [rowVerify])

  // A landed write that closes the add face takes its form out of the
  // document: focus returns to the entry that reopens it. The form closes
  // before the re-read, so the landing waits for the re-read to settle: the
  // entry it decides from is the one the refreshed profile leaves behind.
  useLayoutEffect(() => {
    if (!addLanded.current || addOpen || !addReadSettled) {
      return
    }
    addLanded.current = false
    const target = addLanding.current
    addLanding.current = null
    if (target === null) {
      return
    }
    if (!focusFellWith(target.control)) {
      return
    }
    const entry = addEntry.current
    if (entry !== null && entry.isConnected) {
      entry.focus()
      return
    }
    addRegion.current?.focus()
  }, [addOpen, addReadSettled])

  // Opening the add face removes its entry, which is where the operator's
  // focus was: hand it to the form's first field. A face the host opened
  // through its signal leaves the operator's focus where it was, unless the
  // browser already dropped it to the body.
  useLayoutEffect(() => {
    if (!addOpen) {
      addWasOpen.current = false
      return
    }
    if (addWasOpen.current) {
      return
    }
    addWasOpen.current = true
    if (!focusFellWith(addEntry.current)) {
      return
    }
    const first = addForm.current?.querySelector<HTMLElement>(
      '[role="combobox"], input:not([type="hidden"]), textarea',
    )
    first?.focus()
  }, [addOpen])

  // The code step's own controls leave when the operator goes back to the
  // value step: focus returns to the value field unless the operator moved
  // it elsewhere.
  useLayoutEffect(() => {
    const previous = sentToBefore.current
    sentToBefore.current = sentTo
    if (previous === null || sentTo !== null) {
      return
    }
    const active = document.activeElement
    if (active !== null && active !== document.body) {
      return
    }
    valueInput.current?.focus()
  }, [sentTo])

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

  /** Open the add face through its one entry, so the component's own button
   * and the host's `openAddSignal` leave the section in the same state: the
   * row flow on another row closes and the shared result slot clears, because
   * one code flow runs at a time on this section. */
  function openAdd(): void {
    closeRowVerify()
    // A landing still waiting on the previous write's re-read belongs to a
    // form the operator has left; the reopened face is a fresh attempt.
    addLanded.current = false
    addLanding.current = null
    setAddReadSettled(true)
    setFieldErrorKey(null)
    setAddOpen(true)
  }

  /** Settle the add face back to closed with no trace of the attempt: the
   * failure banner, the field error, the success notice and the sent-code
   * step all belong to the form that is going away. */
  function closeAdd(): void {
    setAddOpen(false)
    setPendingSend(null)
    setSentTo(null)
    setAddValue('')
    setCode('')
    setFieldErrorKey(null)
    clearFailure()
  }

  function remove(id: string): Promise<void> {
    // Captured before the write: the refetched profile drops the row, so the
    // confirmation's name must be read from the list this attempt acted on.
    const identifierValue = rows?.find((row) => row.id === id)?.value ?? ''
    // The row's own control leaves with the row when the removal lands, so
    // the landing record is made before it unmounts.
    const control = removeButtons.current.get(id) ?? null
    if (control !== null) {
      removalLanding.current = {
        id,
        order: visibleIds(rows ?? []),
        control,
      }
    }
    const done = beginRemove(id)
    return feedback.run(async () => {
      setFailureText(null)
      try {
        await removeIdentifier.mutateAsync({ id })
      } catch (error) {
        // The row stays: there is no neighbour to land on, and a step-up
        // replay makes its own record.
        removalLanding.current = null
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
      await invalidateProfile()
      // Translated when the attempt settles; it names the identifier removed.
      return t('identifiers.removed', {
        value: identifierValue,
        interpolation: { escapeValue: false },
      })
    }).finally(done)
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
    via: 'emailSwitch' | 'phoneSwitch',
  ): Promise<void> {
    const done = beginUpdate({ kind: 'flags', id, via })
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
    }).finally(done)
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
    const done = beginUpdate({ kind: 'select' })
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
    }).finally(done)
  }

  /** Make one proved email address the account's default contact. The server
   * clears the previous choice in the same write, which is not a credential
   * rotation. The write needs a fresh authentication like every change of
   * what proves the account. */
  function setDefaultContact(id: string, value: string): Promise<void> {
    // The landed choice takes this row's action out of the document, so the
    // record is made before the write -- including the replay after a won
    // step-up challenge, which re-enters here rather than through a click.
    const control = defaultButtons.current.get(id) ?? null
    if (control !== null) {
      defaultLanding.current = {
        id,
        order: visibleIds(rows ?? []),
        control,
      }
    }
    const done = beginUpdate({ kind: 'default', id })
    return feedback.run(async () => {
      setFailureText(null)
      try {
        await updateIdentifier.mutateAsync({ id, data: { default_contact: true } })
      } catch (error) {
        // The row keeps its action on a refusal; a step-up replay makes its
        // own landing record.
        defaultLanding.current = null
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
    }).finally(done)
  }

  function add(kind: AddKind, value: string): Promise<void> {
    // Only the email branch sends a code, so only it needs the proof.
    if (kind === 'email' && proofBlocked) {
      return Promise.resolve()
    }
    setFieldErrorKey(null)
    if (addOpen) {
      // A landed write closes the form, taking the control that sent it out
      // of the document; the record names the form for the focus test.
      addLanding.current = { id: '', order: [], control: addForm.current }
    }
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
        addLanded.current = true
        // The write landed: close the form now. A failed re-read must not
        // leave the same add available for a second submit, and the landing
        // can only be decided from the elements the read leaves behind, so
        // it waits for the read to settle. Its rejection is contained for
        // the same reason: the write already committed.
        setAddReadSettled(false)
        closeAdd()
        try {
          await (kind === 'username' ? invalidateAfterWrite() : invalidateProfile())
        } catch {
          // The stale list stays on screen under its own notice.
        } finally {
          setAddReadSettled(true)
        }
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
    if (addOpen) {
      addLanding.current = { id: '', order: [], control: addForm.current }
    }
    return feedback.run(async () => {
      setFailureText(null)
      try {
        // Proving an address carries no fresh authentication of its own and
        // rotates nothing, so it opens no window. The consequence the server
        // attaches to the proof -- an address that becomes the default
        // contact or the account's only address -- can still answer a step-up
        // refusal, which arms the gate with the same code.
        await verifyIdentifier.mutateAsync({ data: { code: nextCode } })
        addLanded.current = true
        // The re-read lands first, so the form's landing sees the entry the
        // new profile leaves behind.
        await invalidateProfile()
        closeAdd()
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
    setRowResendFromSuccess(false)
    setRowVerify({ key, value })
  }

  /** Settle the row flow back to closed with no trace of the attempt. */
  function closeRowVerify(): void {
    setPendingSend(null)
    setRowVerify(null)
    setRowSentTo(null)
    setRowCode('')
    setRowCodeInvalid(false)
    setRowResendUntil(0)
    setRowResendFromSuccess(false)
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
        const held = interval > 0
        setRowResendUntil(held ? Date.now() + interval * 1000 : 0)
        // A successful send leaves no banner, so its own note is the wait's
        // only visible explanation.
        setRowResendFromSuccess(held)
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
          // The failure's banner carries this hold's countdown; the row's
          // note would repeat it.
          setRowResendFromSuccess(false)
          setRowNow(Date.now())
        }
        throw error
      }
    })
  }

  const addRef = useRef(add)
  addRef.current = add
  const rowSendRef = useRef(rowSend)
  rowSendRef.current = rowSend

  /**
   * Ask for an email code from the add face: send now when the gate is
   * known to want no proof, otherwise hold the send and let the block
   * obtain one; the effect below sends when it arrives.
   */
  function askAddEmail(value: string): void {
    if (pendingSend !== null) {
      // A send is already waiting on its proof; asking again changes nothing.
      return
    }
    // The attempt begins here, whichever path it takes: the previous
    // result must not stand beside the verification the new one renders.
    clearFailure()
    setFieldErrorKey(null)
    if (
      !human.pending &&
      gateDocument !== null &&
      (!needsProof || human.proof !== null)
    ) {
      void add('email', value)
      return
    }
    setPendingSend({ step: 'add', value })
  }

  /**
   * Ask a row to resend to the address it already shows: the same awaited
   * path as the add face, so one proof path serves both sends.
   */
  function askRowSend(address: string): void {
    if (pendingSend !== null) {
      return
    }
    clearFailure()
    setRowCodeInvalid(false)
    if (
      !human.pending &&
      gateDocument !== null &&
      (!needsProof || human.proof !== null)
    ) {
      void rowSend(address)
      return
    }
    setPendingSend({ step: 'row', address })
  }

  // A send asked for while the gate held it leaves as soon as the gate is
  // known and the proof it wants is in hand: the viewer's one request is
  // not turned into a click followed by a second one. A gate still being
  // read keeps the send waiting; a gate that wants nothing sends at once.
  // The scope's own write in flight keeps it waiting too: the send joins
  // the same feedback scope, so it must not start beside another attempt
  // (a row's switch, a removal, a verification). Waiting on the proof is
  // not a write, so the controls that leave the wait -- cancel, a switch,
  // the code in hand -- stay usable while it is held here.
  useEffect(() => {
    if (pendingSend === null || human.pending || gateDocument === null) {
      return
    }
    if (needsProof && human.proof === null) {
      return
    }
    if (writeBusy) {
      return
    }
    const send = pendingSend
    setPendingSend(null)
    if (send.step === 'add') {
      void addRef.current('email', send.value)
      return
    }
    void rowSendRef.current(send.address)
  }, [
    pendingSend,
    human.pending,
    gateDocument,
    needsProof,
    human.proof,
    writeBusy,
  ])

  /** Submit the row flow's code. The write proves the address the send just
   * named, so the refetched row loses its unverified chip. */
  function rowVerifyCode(nextCode: string): Promise<void> {
    // Verifying the code already in hand calls off a row resend waiting on
    // its proof: the late token must not send a second code that invalidates
    // the one being proved, and the wait it left behind must not block later
    // sends.
    setPendingSend(null)
    const address = rowSentTo ?? rowVerify?.value ?? ''
    setRowCodeInvalid(false)
    // The landed verification closes the row's form; the record names the
    // form and the rows whose verify actions can take focus after it.
    const openKey = rowVerify?.key
    if (openKey !== undefined) {
      verifyLanding.current = {
        id: openKey,
        order: visibleVerifyKeys(rows ?? []),
        control: rowVerifyForm.current,
      }
    }
    return feedback.run(async () => {
      setFailureText(null)
      try {
        // The row's verification proves the address without rotating the
        // session or revoking the others, so it runs outside the window. The
        // consequence the proof carries -- an address that becomes the only
        // address, hence the automatic login way -- can still answer a step-up
        // refusal, which arms the gate with the same code.
        await verifyIdentifier.mutateAsync({ data: { code: nextCode } })
        verifyLanded.current = true
        setRowVerify(null)
        setRowSentTo(null)
        setRowCode('')
        setRowResendUntil(0)
        setRowResendFromSuccess(false)
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

  /** The add face's submit: send or add on the value step, verify on the
   * code step. */
  function submitAdd(event: FormEvent<HTMLFormElement>): void {
    event.preventDefault()
    if (sentTo !== null) {
      void verify(code)
      return
    }
    if (axis === 'contact' || formKind === 'email') {
      const address = parseEmailAddress(addValue)
      if (address === null) {
        // The client guard owns this attempt's result; a settled result from
        // the previous attempt must not stand beside the field error.
        clearFailure()
        setFieldErrorKey('identifiers.add.invalidEmail')
        return
      }
      askAddEmail(address)
      return
    }
    const username = addValue.trim()
    if (username !== '') {
      void add('username', username)
    }
  }

  /** The row verification form's submit: ask for a code, or verify the one
   * in hand. */
  function submitRowVerify(event: FormEvent<HTMLFormElement>): void {
    event.preventDefault()
    if (rowSentTo === null) {
      askRowSend(rowVerify?.value ?? '')
      return
    }
    if (rowCode.trim() !== '') {
      void rowVerifyCode(rowCode)
    }
  }

  // Both forms are guarded: an implicit submission the submit button does
  // not carry (Enter in a field, a programmatic submit) is swallowed while
  // the write is in flight and while a required value is missing.
  const guardedAddSubmit = useActivationGuard(submitAdd, {
    busy: addBusy || pendingSend !== null,
    unavailable:
      writeBusy ||
      (sentTo === null ? addValue.trim() === '' : code.trim() === ''),
  })
  const guardedRowVerifySubmit = useActivationGuard(submitRowVerify, {
    busy: addBusy,
    unavailable:
      writeBusy || (rowSentTo !== null && rowCode.trim() === ''),
  })

  // Which action of the add face is in flight, and the reason a value step's
  // send cannot run. The two mutations are shared with the row flow, so the
  // open face is what tells this form's write apart.
  const addSendInFlight =
    (pendingSend !== null && pendingSend.step === 'add') ||
    (addIdentifier.isPending && addOpen)
  const addVerifyInFlight = verifyIdentifier.isPending && addOpen
  const addValueMissing = addValue.trim() === ''
  const addValueMissingReason =
    axis === 'contact' || formKind === 'email'
      ? t('identifiers.add.emailEmpty')
      : t('identifiers.add.usernameEmpty')

  const valueLabel =
    formKind === 'email'
      ? t('identifiers.add.value.labelEmail')
      : t('identifiers.add.value.labelUsername')
  const valueHelper =
    formKind === 'email'
      ? t('identifiers.add.value.helperEmail')
      : t('identifiers.add.value.helperUsername')

  // The deployment's human-verification block. An email face renders it
  // only while the send it asked for waits on a proof; it renders nothing
  // when no proof is required.
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
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: 1.5,
          }}
        >
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
            <SignInSwitch
              checked={emailSignInRow !== null}
              label={
                emailSignInRow === null
                  ? t('identifiers.action.enable')
                  : t('identifiers.action.disable')
              }
              describedBy={
                emailSignInRow === null ? `${headingId}-off` : undefined
              }
              busy={
                pendingUpdate?.kind === 'flags' &&
                pendingUpdate.via === 'emailSwitch'
              }
              unavailable={
                scopeBlocked ||
                (emailSignInRow === null && provenEmails.length === 0)
              }
              onChange={() => {
                if (emailSignInRow !== null) {
                  setLastDisabledSignInEmail({
                    id: emailSignInRow.id,
                    value: emailSignInRow.value ?? '',
                  })
                  void setFlags(
                    emailSignInRow.id,
                    emailSignInRow.value ?? '',
                    { signIn: false },
                    'emailSwitch',
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
                }, 'emailSwitch')
              }}
            />
          ) : signInRow?.id != null ? (
            <SignInSwitch
              checked={signInRow.sign_in === true}
              label={
                signInRow.sign_in === true
                  ? t('identifiers.action.disable')
                  : t('identifiers.action.enable')
              }
              busy={
                pendingUpdate?.kind === 'flags' &&
                pendingUpdate.via === 'phoneSwitch'
              }
              unavailable={scopeBlocked}
              onChange={() =>
                void setFlags(
                  signInRow.id as string,
                  signInRow.value ?? '',
                  { signIn: signInRow.sign_in !== true },
                  'phoneSwitch',
                )
              }
            />
          ) : null}
        </Box>
        {kind === 'email' ? (
          emailSignInRow !== null ? (
            <Select
              size="small"
              value={emailSignInRow.value ?? ''}
              // A picker that saves on change stays focusable but
              // unchangeable while a write is in flight: MUI reports it as
              // aria-readonly on the combobox and ignores activation.
              readOnly={scopeBlocked}
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
   * verify closing the row on the right. After a send the resend stays
   * unavailable for the server's interval. A hold a successful send started
   * states the wait in the row's own note; a hold a throttled failure
   * started gets no note, because the failure's banner names that wait and
   * counts it down.
   */
  function renderRowVerifyForm(address: string) {
    // Which action of this form is in flight. The row send and the code's
    // verification share the two mutations with the add face, so the open
    // row flow is what tells this form's write apart.
    const rowSendInFlight =
      (pendingSend !== null && pendingSend.step === 'row') ||
      (addIdentifier.isPending && rowVerify !== null)
    const rowVerifyInFlight = verifyIdentifier.isPending && rowVerify !== null
    const resendCountingDown = rowRemainingSeconds > 0
    const resendUnavailable =
      !rowSendInFlight && (scopeBlocked || resendCountingDown)
    // A successful send's note states the wait; a throttled failure's banner
    // counts it down but has no id to point at, so the control names the
    // waiting state itself. The two explanations are exclusive.
    const resendExplanation =
      resendCountingDown && rowResendFromSuccess
        ? { unavailableDescribedBy: ROW_RESEND_NOTE_ID }
        : resendUnavailable && !scopeBlocked && resendCountingDown
          ? { unavailableReason: t('identifiers.resendWaiting') }
          : {}
    return (
      <Box
        component="form"
        ref={rowVerifyForm}
        noValidate
        onSubmit={guardedRowVerifySubmit}
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
          />
          {/* The email face's block appears only while a send it asked for
              waits on its proof. */}
          {pendingSend !== null && pendingSend.step === 'row'
            ? humanBlock
            : null}
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
              slotProps={{
                htmlInput: {
                  'aria-invalid': rowCodeInvalid,
                  readOnly: addBusy,
                },
              }}
              onChange={(event) => {
                setRowCode(event.target.value)
                if (rowCodeInvalid) {
                  setRowCodeInvalid(false)
                }
              }}
            />
          )}
          <ActionRow size="small" sx={{ width: '100%' }}>
            <ActionButton
              type="button"
              unavailable={scopeBlocked}
              onClick={closeRowVerify}
            >
              {t('identifiers.add.cancel')}
            </ActionButton>
            {rowSentTo === null ? null : (
              <ActionButton
                type="button"
                busy={rowSendInFlight}
                unavailable={resendUnavailable}
                {...resendExplanation}
                onClick={() => askRowSend(address)}
              >
                {t('verification.resend')}
              </ActionButton>
            )}
            {rowSentTo === null ? (
              <ActionButton
                type="submit"
                emphasis="primary"
                busy={rowSendInFlight}
                unavailable={!rowSendInFlight && scopeBlocked}
              >
                {t('identifiers.add.submitEmail')}
              </ActionButton>
            ) : (
              <ActionButton
                type="submit"
                emphasis="primary"
                busy={rowVerifyInFlight}
                unavailable={
                  !rowVerifyInFlight &&
                  (scopeBlocked || rowCode.trim() === '')
                }
                unavailableReason={
                  !rowVerifyInFlight &&
                  !scopeBlocked &&
                  rowCode.trim() === ''
                    ? t('identifiers.add.codeEmpty')
                    : undefined
                }
              >
                {t('identifiers.add.verify')}
              </ActionButton>
            )}
          </ActionRow>
          {/* A successful send's hold has no banner to explain it, so the
              note states its wait. A throttled failure's banner counts the
              same wait down, so this note stays away then. */}
          {rowSentTo !== null &&
          rowRemainingSeconds > 0 &&
          rowResendFromSuccess ? (
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
   * removal sit as the row's own actions. Whether an email or phone signs in
   * is a group decision, so no row carries a switch for it.
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
    const scopeUnavailable = scopeBlocked
    // The action of this row whose own write is in flight (only a removal is
    // offered on a merged-view row besides verification, which starts a
    // form rather than a write).
    const busyAction: RowActionKey | undefined =
      id !== null && pendingRemoveId === id ? 'remove' : undefined
    return (
      <Box
        component="li"
        key={rowKey}
        sx={{ py: 1.5, minWidth: 0 }}
      >
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: 1.5,
          }}
        >
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
                color={verified ? 'success' : 'default'}
                label={stateLabel}
              />
            ) : null}
            {showActionsMenu ? (
              <ContactActions
                canVerify={canVerifyRow}
                verifyOpen={rowFlowOpen}
                verifyOpenReason={t('identifiers.verifyInProgress')}
                canRemove={canRemoveRow}
                busyAction={busyAction}
                scopeUnavailable={scopeUnavailable}
                onVerify={() => openRowVerify(rowKey, value)}
                onVerifyRef={(node) => {
                  if (node === null) {
                    verifyButtons.current.delete(rowKey)
                  } else {
                    verifyButtons.current.set(rowKey, node)
                  }
                }}
                onRemove={() => {
                  if (id !== null) {
                    setRemoveTarget(id)
                  }
                }}
                onRemoveRef={(node) => {
                  if (id === null) {
                    return
                  }
                  if (node === null) {
                    removeButtons.current.delete(id)
                  } else {
                    removeButtons.current.set(id, node)
                  }
                }}
              />
            ) : null}
          </Box>
        </Box>
        {kind === 'email' && verified ? (
          <DefaultContactChoice
            checked={axes.defaultContact}
            label={t('identifiers.axes.defaultContact')}
            ariaLabel={t('identifiers.axes.defaultAria', {
              value,
              interpolation: { escapeValue: false },
            })}
            unavailable={scopeUnavailable || id === null}
            unavailableReason={
              id === null
                ? t('identifiers.axes.defaultUnavailable')
                : undefined
            }
            onChange={() => {
              if (id !== null && !axes.defaultContact) {
                void setDefaultContact(id, value)
              }
            }}
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
                                color={verified ? 'success' : 'default'}
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
                              <ContactActions
                                canVerify={
                                  kind === 'email' &&
                                  !verified &&
                                  rowVerifyAvailable
                                }
                                canDefault={kind === 'email' && verified}
                                isDefault={axes.defaultContact}
                                busyAction={
                                  pendingRemoveId === id
                                    ? 'remove'
                                    : pendingUpdate?.kind === 'default' &&
                                        pendingUpdate.id === id
                                      ? 'defaultContact'
                                      : undefined
                                }
                                scopeUnavailable={scopeBlocked}
                                onVerify={() =>
                                  openRowVerify(
                                    `contact:${id}`,
                                    row.value ?? '',
                                  )
                                }
                                onVerifyRef={(node) => {
                                  const key = `contact:${id}`
                                  if (node === null) {
                                    verifyButtons.current.delete(key)
                                  } else {
                                    verifyButtons.current.set(key, node)
                                  }
                                }}
                                onSetDefault={() =>
                                  void setDefaultContact(id, row.value ?? '')
                                }
                                onDefaultRef={(node) => {
                                  if (node === null) {
                                    defaultButtons.current.delete(id)
                                  } else {
                                    defaultButtons.current.set(id, node)
                                  }
                                }}
                                onRemove={() => setRemoveTarget(id)}
                                onRemoveRef={(node) => {
                                  if (node === null) {
                                    removeButtons.current.delete(id)
                                  } else {
                                    removeButtons.current.set(id, node)
                                  }
                                }}
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

      <Box
        ref={addRegion}
        tabIndex={-1}
        role="group"
        aria-label={t('identifiers.title')}
        sx={{ mt: 2 }}
      >
        {axis === 'signIn' || addKinds.length === 0 ? null : !addOpen ? (
          showAddButton ? (
          <ActionButton
            ref={addEntry}
            size="small"
            unavailable={scopeBlocked}
            onClick={openAdd}
          >
            {t('identifiers.add.open')}
          </ActionButton>
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
              ref={addForm}
              noValidate
              onSubmit={guardedAddSubmit}
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
                      // A selector that only changes the form's context: it
                      // never starts this write, so it is never focused when
                      // one begins. It stays natively disabled, and is
                      // outside the lint rule's element set.
                      disabled={addBusy}
                      onChange={(event) => {
                        // Changing the kind abandons a send waiting on its
                        // proof.
                        setPendingSend(null)
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
                    slotProps={{ htmlInput: { readOnly: addBusy } }}
                    onChange={(event) => {
                      // Editing the value abandons a send waiting on its
                      // proof.
                      setPendingSend(null)
                      setAddValue(event.target.value)
                      if (fieldErrorKey !== null) {
                        setFieldErrorKey(null)
                      }
                    }}
                  />
                  {pendingSend !== null && pendingSend.step === 'add'
                    ? humanBlock
                    : null}
                  {/* Cancel leads the inline form's action row; the
                      primary submit closes it on the right. */}
                  <ActionRow sx={{ width: '100%' }}>
                    <ActionButton
                      type="button"
                      unavailable={scopeBlocked}
                      onClick={closeAdd}
                    >
                      {t('identifiers.add.cancel')}
                    </ActionButton>
                    <ActionButton
                      type="submit"
                      emphasis="primary"
                      busy={addSendInFlight}
                      unavailable={
                        !addSendInFlight && (scopeBlocked || addValueMissing)
                      }
                      unavailableReason={
                        !addSendInFlight && !scopeBlocked && addValueMissing
                          ? addValueMissingReason
                          : undefined
                      }
                    >
                      {axis === 'contact' || formKind === 'email'
                        ? t('identifiers.add.submitEmail')
                        : t('identifiers.add.submitUsername')}
                    </ActionButton>
                  </ActionRow>
                </>
              ) : (
                <>
                  {/* The destination text stays through the step; as the
                      announcement region it announces the code's landing
                      once (inserted empty, then filled a frame later). */}
                  <AnnouncementRegion
                    message={t('identifiers.add.sent', {
                      value: sentTo,
                      interpolation: { escapeValue: false },
                    })}
                    sx={{ typography: 'body2', color: 'text.secondary' }}
                  />
                  <TextField
                    fullWidth
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    label={t('identifiers.add.code.label')}
                    value={code}
                    inputRef={addCodeRef}
                    slotProps={{ htmlInput: { readOnly: addBusy } }}
                    onChange={(event) => setCode(event.target.value)}
                  />
                  <ActionRow sx={{ width: '100%' }}>
                    <ActionButton
                      type="button"
                      unavailable={scopeBlocked}
                      onClick={() => {
                        setSentTo(null)
                        setCode('')
                        setFieldErrorKey(null)
                        clearFailure()
                      }}
                    >
                      {t('identifiers.add.change')}
                    </ActionButton>
                    <ActionButton
                      type="submit"
                      emphasis="primary"
                      busy={addVerifyInFlight}
                      unavailable={
                        !addVerifyInFlight &&
                        (scopeBlocked || code.trim() === '')
                      }
                      unavailableReason={
                        !addVerifyInFlight &&
                        !scopeBlocked &&
                        code.trim() === ''
                          ? t('identifiers.add.codeEmpty')
                          : undefined
                      }
                    >
                      {t('identifiers.add.verify')}
                    </ActionButton>
                  </ActionRow>
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
        onCancel={() => setRemoveTarget(null)}
        onConfirm={() => {
          // The confirmation's job ends at the confirming click: it closes
          // before the write, so the row's own control (the write's
          // initiator) holds focus while the removal runs and the section's
          // result region reports the outcome without a modal standing over
          // the list.
          const id = removeTarget
          setRemoveTarget(null)
          if (id !== null) {
            void remove(id)
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
        onRelogin={rememberRelogin}
      />
    </>
  )
}
