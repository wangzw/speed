/**
 * MfaSection: the two-step-verification (TOTP authenticator + passkey)
 * surface of the account page.
 *
 * The authn factor surface lists, renames and removes factors (GET
 * /api/v1/auth/factors, PATCH /api/v1/auth/factors/{id}, DELETE
 * /api/v1/auth/factors/{id}); registration is not among its routes -- the
 * one entry for it is the login engine's `enroll` purpose. The section
 * therefore renders the factors it reads and offers the enrollment entries
 * THIS deployment carries, each of which opens a `purpose: 'enroll'` flow
 * (session.startAuthenticatedFlow) whose steps the panel answers until the
 * flow settles `enrolled`. Which kinds the deployment carries is its own
 * answer (GET /api/v1/auth/methods' `mfa.factors`, read once through
 * session.listMethods), never a hard-coded set: a deployment whose assembly
 * has no passkey relying party does not offer the passkey entry, so the
 * surface never offers a factor it cannot complete. The read is a session
 * operation on the session the section already drives -- the same seam the
 * enroll flow uses -- and an unresolved read withholds the entries (fail
 * closed) rather than guessing.
 *
 *  - Enroll. The entry opens the method's one panel and starts the flow at
 *    once: the panel holds the name field (prefilled with the browser/OS
 *    summary the session rows show, editable and clearable), the sentence
 *    stating what confirming does, and the
 *    method's body as its first step arrives -- TOTP's otpauth_uri as a QR
 *    symbol with a manual-entry disclosure for its base32 secret beside a
 *    six-digit code field, or a passkey's creation options driven through
 *    the browser's WebAuthn API. One confirm control submits the step: a
 *    passkey's registration click is also the user gesture its ceremony
 *    needs. A step the server sends as a bare `complete` carries no input,
 *    so the driver submits it itself (at most once per seq) instead of
 *    asking for a second confirmation; the same auto-submission covers the
 *    ready state a code answer reaches. The flow settles `enrolled`
 *    without changing this session's level; the factors list is then
 *    refetched, and a name typed in the panel -- the flow's first input
 *    carried an empty label -- is written to the row the enrollment just
 *    added with PATCH /api/v1/auth/factors/{id}. The panel opens inside the
 *    method's own group and replaces that group's rows, its heading naming
 *    the method being added. Its cancel sits on the left of its confirm
 *    with the ordinary action-row spacing.
 *  - SMS destination and prerequisite. The SMS factor binds the phone the
 *    account has already proved; this flow carries no phone field, and the
 *    server refuses a start without one (authn.phone_required). The panel
 *    therefore reads the proved phone off the profile (GET /api/v1/auth/me,
 *    the same read the account page uses) and names it in the note above the
 *    step. An account with no proved phone gets no panel: activating the SMS
 *    add entry shows an informational notice beneath that kind's entry
 *    instead, stating that an SMS second step uses the account's verified
 *    phone and that one must be added and verified first. The notice carries
 *    no action and no close control, no flow request goes out, and the add
 *    entry stays available. It clears when another kind's add entry is
 *    activated, when the profile gains a verified phone, or when the section
 *    unmounts. Because the destination is the account's phone and not this
 *    browser, the panel does not prefill the browser summary for this kind.
 *    A refusal that still answers phone-required -- the phone was removed in
 *    another tab -- renders that code's own sentence in the panel's failure
 *    region.
 *  - Fail inside the panel. A refused start or answer renders its code in
 *    the panel's own region, beside the controls that can be corrected; the
 *    panel stays open with the typed code and name. This is the one place a
 *    failure is not the section's result: a control group's result region is
 *    below the groups, and the panel is the group it belongs to while it is
 *    open.
 *
 *  - One result region. Enrollment, rename and removal are writes of this
 *    section, so they report through one `useWriteFeedback` slot rendered
 *    once below the grouped methods. A new attempt resets the slot first,
 *    and the result that settles last replaces the earlier one: the
 *    enrollment's confirmation and a later removal never stand side by
 *    side. The result stays until the next attempt or until the reader
 *    leaves the section; nothing clears it on a timer.
 *  - Abandon. A panel the reader cancels, or the section unmounting while a
 *    flow is open, releases the flow (DELETE /flows/current) so the server
 *    drops the pending row the registration wrote instead of leaving it to
 *    the pending sweep. Each panel instance releases at most once, a
 *    settled flow releases nothing, and a failed release is ignored rather
 *    than shown: the reader has already given the flow up. A start that
 *    answers after the panel closed or the section unmounted is released
 *    the same way.
 *  - Rename. A row's inline editor sends PATCH /api/v1/auth/factors/{id}
 *    with the new label. The label is metadata, so the route asks for no
 *    step-up and the section opens no gate for it; an empty value clears the
 *    name and the row falls back to its kind. A landed rename confirms itself
 *    below the list, naming the row by the name the server stored, and the
 *    factors list is refetched. A refused rename renders its error code in
 *    the same region (a 400 for a value outside the label's domain). A 404
 *    `authn.factor_not_found` -- the row was removed on another device, or
 *    never was the account's -- closes the editor, rereads the factors once
 *    and renders the code's sentence, which says the method is gone; the
 *    reread is not repeated by the answer it produces.
 *  - Remove. A row's row-end action sends DELETE /api/v1/auth/factors/{id}
 *    behind the ui-kit danger ConfirmDialog (removing a factor is
 *    irreversible). The route asks for a fresh multi-factor
 *    authentication; an unelevated caller is answered 401
 *    authn.insufficient_user_authentication, which opens the step-up dialog and, on
 *    success, re-runs the removal exactly once. A 409 (the factor is the
 *    account's last one) renders its code text and the section stays put.
 *    A landed removal confirms itself below the list, naming the row that
 *    went -- its own label when it has one, else its kind -- because two
 *    factors of one kind can stand side by side. Removing an active factor
 *    rotates the requesting session's cookie; removing a pending or
 *    disabled one does not (the route settles the sessions only for a
 *    factor that was active, and treats a name it no longer finds as
 *    already removed, rotating all the same). The rotating removal runs
 *    inside an identity-transition window (session.beginIdentityTransition):
 *    a read that lands with the outgoing credential and answers anonymous
 *    is discarded instead of ending the session, and the window closes once
 *    the refetch that follows the write has settled. A removal the list
 *    read as pending or disabled runs bare: no credential is replaced, so
 *    a genuine anonymous answer must still end the session.
 *
 * The factors are grouped by kind: each assembled kind is a section whose
 * heading names it, and a group that the deployment reports as enrollable
 * carries one add action on that heading, worded "add" whether or not the
 * group already holds an enabled row. The groups stand in one fixed order by
 * strength -- passkey, authenticator app, SMS, then kinds this package does
 * not know -- and enabling a method never moves its group. The rows a group
 * lists are the enabled and the disabled ones -- a pending row is the
 * enrollment's own intermediate state and is withheld -- and a disabled row
 * carries a muted "disabled" marker. Every row shows
 * its name (the label, else the kind) and the instant it last passed a
 * verification, which says so when it has never passed one; the instant is
 * formatted with the account's timezone chain -- the stored preference, the
 * host's display-timezone context, the device zone, then UTC -- the same
 * chain every other surface uses. A row ends in its own actions -- rename
 * and removal when the row can be renamed, removal alone otherwise -- the
 * same direct row actions the identifiers surface uses.
 *
 * Both gated entries share one dialog: the pending action is remembered
 * so a success re-runs exactly that action and a cancel re-runs nothing.
 *
 * The section takes the session as its only prop -- the enroll flow and
 * the step-up dialog both drive it -- while the factors read and the
 * remove write travel on the shared access token through the generated
 * calls. State is discovered by reading the factor list, never declared;
 * the section never claims a factor is enabled beyond what the server
 * answered.
 *
 * An account left with exactly one enabled factor is told so above the
 * grouped methods, which are where another one is registered. The count
 * comes from the same factors read; pending and disabled rows do not count.
 * The note is withheld when the deployment reports no enrollable kind,
 * because then there is no add action to point at.
 */

import { useEffect, useRef, useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnMfaFactorsQueryKey,
  useAuthnMfaRemoveFactor,
  useAuthnMfaUpdateFactor,
} from '@speed/api-sdk'
import type {
  AuthnFlowState,
  AuthSession,
  AuthnStep,
  StepUpReason,
} from '@speed/auth-core'
import { stepUpReasonOf } from '@speed/auth-core'
import { formatInstant } from '@speed/i18n'
import {
  ActionButton,
  ActionRow,
  ConfirmDialog,
  errorCodeOf,
  FORM_MAX_WIDTH,
  ListSkeleton,
  SectionCard,
  useWriteFeedback,
  type WriteOutcome,
} from '@speed/ui-kit'
import { AccountAsyncSection } from './internal/async-section.js'
import { FlowStep } from './internal/flow-step.js'
import type { FlowStepAnswer } from './internal/flow-step.js'
import { runIdentityTransition } from './internal/identity-transition.js'
import { asFactorList } from './internal/models.js'
import { useFactorsQuery, useProfileQuery } from './internal/queries.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountTimeZone } from './internal/time-zone.js'
import { summarizeUserAgent } from './internal/ua-summary.js'
import { useAccountUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'

export interface MfaSectionProps {
  /** The session that drives the enroll flow and the step-up dialog. */
  readonly session: AuthSession
  /**
   * The host's current path, sent as the enroll and step-up flows'
   * `return_to`: a redirect method's round trip lands back on the page that
   * started it, so that page reads the refusal. Omitted, the deployment's
   * default return target applies.
   */
  readonly returnPath?: string
}

/** The factor kinds the authn factor surface records (pkg/authn's
 * FactorKind), one bundle key each under mfa.factor.kind. A value outside
 * the list renders the generic "other" label, never a raw value. */
const KNOWN_FACTOR_KINDS = new Set<string>(['totp', 'webauthn', 'sms'])

/** The action a won step-up re-runs, with what the refusal asked for. */
type GatedAction =
  | {
      readonly kind: 'enroll'
      readonly factorKind: string
      readonly reason: StepUpReason
    }
  | {
      readonly kind: 'remove'
      readonly factorId: string
      readonly reason: StepUpReason
    }

/** The open enrollment: the kind it adds, the name typed in its own field,
 * and the step it is parked on. The step is null while the flow starts,
 * and its heading names the kind on every step. */
interface EnrollPanel {
  readonly kind: string
  readonly label: string
  readonly step: AuthnStep | null
  readonly seq: number
}

/** The row whose name is being edited: its identifier and the input value. */
interface RenameDraft {
  readonly id: string
  readonly label: string
}

/** What a reply leaves the flow doing: waiting on a step, or parked on a
 * `complete` step whose automatic submission was refused. */
type AdvanceOutcome =
  | { readonly kind: 'state'; readonly state: AuthnFlowState }
  | {
      readonly kind: 'retry'
      readonly step: AuthnStep
      readonly seq: number
      readonly failure: unknown
    }

/** The name suggested for a passkey registered from this browser: the same
 * short browser/OS summary the session rows show. An unrecognised user
 * agent suggests no name; the reader can type one or leave it empty. No
 * other kind takes this suggestion: an SMS factor sends to the account's
 * phone, so its name describes no browser. */
function suggestedFactorName(): string {
  if (typeof navigator === 'undefined') {
    return ''
  }
  return summarizeUserAgent(navigator.userAgent) ?? ''
}

/** Parses a server timestamp; answers null for an absent or malformed
 * value so a bad row can never reach Intl and throw. */
function parseDate(iso: string | undefined): Date | null {
  if (iso === undefined) {
    return null
  }
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? null : date
}

/** The pending-state placeholder, read aloud as one loading announcement. */
function FactorListSkeleton({ label }: { readonly label: string }) {
  return (
    <ListSkeleton
      label={label}
      rows={[0, 1].map((index) => (
        <>
          <Skeleton variant="text" width={index === 0 ? '44%' : '34%'} />
          <Skeleton variant="text" width={index === 0 ? '58%' : '48%'} />
        </>
      ))}
    />
  )
}

/** One sentence explaining a two-step method. */
const METHOD_HELP_KEYS: Readonly<Record<string, string>> = {
  totp: 'mfa.method.totp',
  webauthn: 'mfa.method.webauthn',
  sms: 'mfa.method.sms',
}

/** One sentence stating what confirming the enrollment step will do, one
 * per factor kind. The sentence renders above that kind's step, including a
 * passkey's registration step, whose prompt alone does not say what the
 * confirmation does. */
const ENROLL_NOTE_KEYS: Readonly<Record<string, string>> = {
  totp: 'mfa.enroll.note.totp',
  webauthn: 'mfa.enroll.note.webauthn',
  sms: 'mfa.enroll.note.sms',
}

/** The enrollment note key for one factor kind, with the generic wording
 * for a kind this package has no sentence for. */
function enrollNoteKey(kind: string): string {
  return ENROLL_NOTE_KEYS[kind] ?? 'mfa.enroll.note.other'
}

/** One factor row's actions: rename it and remove it, in that order. A
 * disabled factor offers removal alone. A row with one action renders that
 * action alone; a row with several renders one small action row. */
function FactorActions({
  canRename,
  disabled,
  onRename,
  onRemove,
}: {
  readonly canRename: boolean
  readonly disabled: boolean
  readonly onRename: () => void
  readonly onRemove: () => void
}) {
  const { t } = useAccountUiTranslation()
  if (!canRename) {
    return (
      <ActionButton
        size="small"
        tone="danger"
        disabled={disabled}
        onClick={onRemove}
      >
        {t('mfa.remove')}
      </ActionButton>
    )
  }
  return (
    <ActionRow size="small">
      <ActionButton disabled={disabled} onClick={onRename}>
        {t('mfa.rename')}
      </ActionButton>
      <ActionButton tone="danger" disabled={disabled} onClick={onRemove}>
        {t('mfa.remove')}
      </ActionButton>
    </ActionRow>
  )
}

/** The enrollment panel's own failure region. A refused start or answer is
 * correctable beside the controls that produced it, so it renders inside the
 * panel; a settled enrollment reports through the section's result region
 * instead. */
function EnrollmentFeedback({
  outcome,
}: {
  readonly outcome: WriteOutcome | null
}) {
  return <WriteFeedbackRegion outcome={outcome} />
}

export function MfaSection({ session, returnPath }: MfaSectionProps) {
  const { t, i18n } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, isFetching, error, refetch } = useFactorsQuery()
  // The same profile read the account page's identifier surface uses: the
  // SMS factor sends to the account's proved phone, and this is where that
  // phone is read. The read is not a new endpoint.
  const { data: profile } = useProfileQuery()
  const provenPhone =
    profile?.identifiers?.find(
      (identifier) =>
        identifier.kind === 'phone' && identifier.proven === true,
    )?.value ?? null
  // The profile answered with its identifier collection and that collection
  // lists no verified phone. An absent collection is not read as "no phone":
  // a partial answer keeps the normal panel and leaves the prerequisite to
  // the server.
  const phoneMissing =
    profile?.identifiers !== undefined && provenPhone === null
  const removeMutation = useAuthnMfaRemoveFactor()
  const updateMutation = useAuthnMfaUpdateFactor()

  // The action a won step-up re-runs, if any.
  const [gate, setGate] = useState<GatedAction | null>(null)
  // The open enrollment panel, if any.
  const [panel, setPanel] = useState<EnrollPanel | null>(null)
  // The kind whose add entry answered with its informational prerequisite
  // notice instead of opening a panel: an SMS factor with no proved phone.
  const [notice, setNotice] = useState<string | null>(null)
  // The enrollment's in-flight state.
  const [enrolling, setEnrolling] = useState(false)
  // The row whose removal is being confirmed.
  const [removeTarget, setRemoveTarget] = useState<string | null>(null)
  // The row whose name is being edited, if any.
  const [renameDraft, setRenameDraft] = useState<RenameDraft | null>(null)
  // The section's one result slot: enrollment, rename and removal all report
  // through it, and it renders once below the grouped methods. The slot
  // holds at most one outcome, so the result that settles last replaces the
  // earlier one instead of standing beside it.
  const feedback = useWriteFeedback()
  // The open panel's own failure slot: a refused start or answer is
  // correctable beside the controls that produced it, so it renders inside
  // the panel rather than in the section's result region.
  const panelFeedback = useWriteFeedback()
  // The deployment's enrollable factor kinds (GET /api/v1/auth/methods'
  // mfa.factors). null is unresolved -- still reading, or the read failed:
  // the enroll entries are then withheld rather than guessed.
  const [enrollableKinds, setEnrollableKinds] = useState<
    readonly string[] | null
  >(null)

  // Whether this panel holds an open flow of its own to release: set when a
  // reply parks a step, cleared when the flow settles or is abandoned. Each
  // panel instance releases at most once.
  const flowOpenRef = useRef(false)
  // The unmount release is deferred by one microtask, so a remount in the same
  // commit (StrictMode's double effect invocation) cancels it: the remounted
  // section goes on holding the same flow.
  const releasePendingRef = useRef(false)
  // The session the release targets, read through a ref so the unmount cleanup
  // needs no dependency and never re-registers.
  const sessionRef = useRef(session)
  sessionRef.current = session
  // Every open and every cancel takes a token. A start or answer that lands
  // under a stale token belongs to a panel the reader has left, so its flow
  // is released instead of reopening anything.
  const panelTokenRef = useRef(0)
  // Whether the section is still mounted; a late start under an unmounted
  // section releases its flow. StrictMode's remount sets it back to true
  // before the late answer runs.
  const mountedRef = useRef(true)
  // The factor identifiers the list held when the panel opened. The row the
  // enrollment adds is the one the refetched list carries beyond this set,
  // which is how a typed name finds the row it belongs to.
  const knownIdsRef = useRef<ReadonlySet<string>>(new Set())

  /** Release the open enrollment flow, at most once per panel instance. A
   * failure is ignored: giving up a flow must not put an error in front of
   * the reader. */
  function releaseEnrollFlow(): void {
    if (!flowOpenRef.current) {
      return
    }
    flowOpenRef.current = false
    void session.abandonFlow().catch(() => undefined)
  }

  // Unmounting is a departure like the panel's cancel: a flow this section
  // opened and has not seen settle is released, so no pending factor row and
  // no stale binding linger on the server. The release waits one microtask so
  // StrictMode's double mount cancels it in the remount's setup.
  useEffect(() => {
    mountedRef.current = true
    releasePendingRef.current = false
    return () => {
      mountedRef.current = false
      if (!flowOpenRef.current) {
        return
      }
      releasePendingRef.current = true
      void Promise.resolve().then(() => {
        if (!releasePendingRef.current) {
          return
        }
        releasePendingRef.current = false
        flowOpenRef.current = false
        void sessionRef.current.abandonFlow().catch(() => undefined)
      })
    }
  }, [])

  const timeZone = useAccountTimeZone()
  const formatTime = (value: string | number | Date): string =>
    formatInstant(value, i18n.language, { timeZone })

  // Read the deployment's factors once per session. The guard survives
  // StrictMode's double effect invocation, so a settled answer is only
  // written while this effect is still the live one.
  useEffect(() => {
    let active = true
    setEnrollableKinds(null)
    void (async () => {
      try {
        const methods = await session.listMethods()
        if (active) {
          setEnrollableKinds(methods.mfa.factors)
        }
      } catch {
        // A failed discovery offers no entry (fail closed): the surface
        // never offers a factor the deployment may not carry.
        if (active) {
          setEnrollableKinds([])
        }
      }
    })()
    return () => {
      active = false
    }
  }, [session])

  // The notice states a missing phone: once the profile read supplies one,
  // it has said its piece and clears itself, rather than hiding only for as
  // long as the phone is there and reappearing if it is removed again.
  useEffect(() => {
    if (provenPhone !== null) {
      setNotice(null)
    }
  }, [provenPhone])

  const factors = data?.factors
  const pending = isPending && data === undefined
  // Disabled through the whole attempt, including the refetches that follow
  // the mutation, so no control changes context under a pending scope.
  const busy =
    enrolling ||
    removeMutation.isPending ||
    updateMutation.isPending ||
    feedback.pending ||
    panelFeedback.pending

  /** Whether a kind's factors carry a reader-chosen name. Only a kind that
   * may be held more than once needs one: the account keeps a single
   * authenticator entry, while several passkeys or several SMS factors are
   * told apart by their names. */
  function takesName(kind: string): boolean {
    return kind !== 'totp'
  }

  /** The translated name of a factor kind, the guarded vocabulary's generic
   * label for a kind this package has no wording for. */
  function factorKindLabel(kind: string): string {
    return KNOWN_FACTOR_KINDS.has(kind)
      ? t(`mfa.factor.kind.${kind}`)
      : t('mfa.factor.kind.other')
  }
  /** The enrollment panel's heading for one factor kind: the one
   * parameterized title key, with the kind's own name in its method slot. */
  function enrollTitle(kind: string): string {
    return t('mfa.enroll.title', {
      method: factorKindLabel(kind),
      interpolation: { escapeValue: false },
    })
  }
  /** The enrollment panel's note: an SMS panel with a proved phone names
   * that destination, and every other kind reads its own sentence. */
  function enrollNote(kind: string): string {
    if (kind === 'sms' && provenPhone !== null) {
      return t('mfa.enroll.note.smsTo', {
        phone: provenPhone,
        interpolation: { escapeValue: false },
      })
    }
    return t(enrollNoteKey(kind))
  }
  // Every kind the deployment reported gets an entry, in the document's
  // order. An unknown kind still starts that kind's flow -- the generic
  // label never turns a deployable factor into a missing entry.
  const enrollEntries = enrollableKinds ?? []

  function invalidateFactors(): Promise<void> {
    return queryClient.invalidateQueries({
      queryKey: getAuthnMfaFactorsQueryKey(),
    })
  }

  /** Submit every bare `complete` step a reply leads with, so the one
   * confirm the reader gave takes the whole flow: such a step carries no
   * input and asks no question. A seq is submitted at most once, so a reply
   * that keeps naming the same one is rendered for the reader instead of
   * looping. A refused automatic submission parks the complete step with its
   * own confirm, which is the control the reader retries. */
  async function advanceFlow(state: AuthnFlowState): Promise<AdvanceOutcome> {
    let current = state
    const completed = new Set<number>()
    while (
      'next' in current &&
      current.next.kind === 'complete' &&
      !completed.has(current.seq)
    ) {
      const step = current.next
      const seq = current.seq
      completed.add(seq)
      try {
        current = await session.answerFlow({ seq, step: 'complete' })
      } catch (failure) {
        return { kind: 'retry', step, seq, failure }
      }
    }
    return { kind: 'state', state: current }
  }

  /** The identifier of the row this enrollment added: the active row of the
   * kind that the list did not carry before the panel opened. */
  function addedFactorId(kind: string): string | null {
    const list = asFactorList(
      queryClient.getQueryData(getAuthnMfaFactorsQueryKey()),
    )
    const added = (list.factors ?? []).find(
      (factor) =>
        (factor.kind ?? '') === kind &&
        factor.status === 'active' &&
        factor.id !== undefined &&
        !knownIdsRef.current.has(factor.id),
    )
    return added?.id ?? null
  }

  /** Finish a settled enrollment: reread the list, name the row with the
   * name typed in the panel, and answer the confirmation sentence. The
   * flow's first input carried no label, so the name is written to the row
   * the enrollment added. */
  async function finishEnrollment(
    kind: string,
    label: string,
  ): Promise<string> {
    await invalidateFactors()
    if (label !== '') {
      const added = addedFactorId(kind)
      if (added === null) {
        throw new Error('the enrolled factor is not in the refetched list')
      }
      await updateMutation.mutateAsync({ id: added, data: { label } })
      await invalidateFactors()
    }
    return t('mfa.enroll.enrolled', {
      factor: factorKindLabel(kind),
      interpolation: { escapeValue: false },
    })
  }

  /** Open one method's enrollment panel and start its flow at once, so the
   * method's body (a QR symbol or a registration prompt) is on the same
   * panel as the name field. The one exception is an SMS factor with no
   * proved phone: there is nothing to send to, so the add entry answers
   * with an informational notice and no panel and no flow request. An
   * unresolved profile is not read as "no phone"; the server still answers
   * that case. */
  function openEnroll(factorKind: string): void {
    releaseEnrollFlow()
    knownIdsRef.current = new Set(
      (factors ?? []).flatMap((factor) =>
        factor.id === undefined ? [] : [factor.id],
      ),
    )
    feedback.reset()
    panelFeedback.reset()
    if (factorKind === 'sms' && phoneMissing) {
      // A start still in flight belongs to the panel being replaced.
      panelTokenRef.current += 1
      setPanel(null)
      setNotice(factorKind)
      return
    }
    setNotice(null)
    const token = ++panelTokenRef.current
    // Only a passkey suggests the browser summary; an SMS factor's name
    // describes the account's phone, not this device.
    const label =
      factorKind === 'webauthn' ? suggestedFactorName() : ''
    setPanel({ kind: factorKind, label, step: null, seq: 0 })
    void startEnrollFlow(factorKind, token, label)
  }

  /** Start the enrollment flow of one method and park its first step on the
   * open panel. */
  async function startEnrollFlow(
    factorKind: string,
    token: number,
    label: string,
  ): Promise<void> {
    // A retry starts a new attempt: the slot is cleared before it goes out.
    feedback.reset()
    panelFeedback.reset()
    setEnrolling(true)
    try {
      const started = await session.startAuthenticatedFlow({
        purpose: 'enroll',
        input: { kind: factorKind, label: '' },
        ...(returnPath === undefined ? {} : { return_to: returnPath }),
      })
      // A start that answers after the reader left the panel is not carried
      // on: its flow is released and no automatic completion is sent.
      if (token !== panelTokenRef.current || !mountedRef.current) {
        if ('next' in started) {
          void sessionRef.current.abandonFlow().catch(() => undefined)
        }
        return
      }
      const outcome = await advanceFlow(started)
      const stillOpen = outcome.kind === 'retry' || 'next' in outcome.state
      if (token !== panelTokenRef.current || !mountedRef.current) {
        if (stillOpen) {
          void sessionRef.current.abandonFlow().catch(() => undefined)
        }
        return
      }
      if (outcome.kind === 'retry') {
        flowOpenRef.current = true
        setPanel((current) =>
          current !== null && current.kind === factorKind
            ? { ...current, step: outcome.step, seq: outcome.seq }
            : current,
        )
        panelFeedback.fail(outcome.failure)
        return
      }
      const state = outcome.state
      if ('next' in state) {
        flowOpenRef.current = true
        setPanel((current) =>
          current !== null && current.kind === factorKind
            ? { ...current, step: state.next, seq: state.seq }
            : current,
        )
        return
      }
      // A settled flow has spent its state, so there is nothing to release.
      flowOpenRef.current = false
      setPanel(null)
      if (state.state === 'enrolled') {
        try {
          feedback.succeed(await finishEnrollment(factorKind, label.trim()))
        } catch (failure) {
          feedback.fail(failure)
        }
        return
      }
      // A settled state other than 'enrolled' is not an enroll outcome.
      feedback.fail({ code: 'client.unknown' })
    } catch (failure) {
      if (token !== panelTokenRef.current || !mountedRef.current) {
        return
      }
      const reason = stepUpReasonOf(errorCodeOf(failure))
      if (reason !== null) {
        // The elevation gate is another door: the panel closes and the flow
        // is started again after a won challenge.
        setPanel(null)
        setGate({ kind: 'enroll', factorKind, reason })
        return
      }
      panelFeedback.fail(failure)
    } finally {
      if (token === panelTokenRef.current) {
        setEnrolling(false)
      }
    }
  }

  /** Answer the panel's current step. The name typed in the panel is read
   * here, at the one confirmation, and written once the enrollment lands. */
  async function answerEnroll(
    current: EnrollPanel,
    answer: FlowStepAnswer,
  ): Promise<void> {
    if (current.step === null) {
      return
    }
    const token = panelTokenRef.current
    const kind = current.kind
    // A new attempt clears the section's slot before it goes out.
    feedback.reset()
    panelFeedback.reset()
    setEnrolling(true)
    try {
      let answered: AuthnFlowState
      try {
        answered = await session.answerFlow({
          seq: current.seq,
          step: answer.step,
          input: answer.input,
        })
      } catch (failure) {
        if (token !== panelTokenRef.current || !mountedRef.current) {
          return
        }
        // A refused answer is correctable in the panel that asked for it.
        panelFeedback.fail(failure)
        return
      }
      // A reader who left while the answer was in flight keeps its refusal or
      // step, but no automatic completion follows it.
      if (token !== panelTokenRef.current || !mountedRef.current) {
        if ('next' in answered) {
          void sessionRef.current.abandonFlow().catch(() => undefined)
        }
        return
      }
      const outcome = await advanceFlow(answered)
      const stillOpen = outcome.kind === 'retry' || 'next' in outcome.state
      if (token !== panelTokenRef.current || !mountedRef.current) {
        if (stillOpen) {
          void sessionRef.current.abandonFlow().catch(() => undefined)
        }
        return
      }
      if (outcome.kind === 'retry') {
        flowOpenRef.current = true
        setPanel((open) =>
          open !== null && open.kind === kind
            ? { ...open, step: outcome.step, seq: outcome.seq }
            : open,
        )
        panelFeedback.fail(outcome.failure)
        return
      }
      const state = outcome.state
      if ('next' in state) {
        flowOpenRef.current = true
        setPanel((open) =>
          open !== null && open.kind === kind
            ? { ...open, step: state.next, seq: state.seq }
            : open,
        )
        return
      }
      // A settled flow has spent its state, so there is nothing to release.
      flowOpenRef.current = false
      setPanel(null)
      if (state.state !== 'enrolled') {
        // A settled state other than 'enrolled' is not an enroll outcome.
        feedback.fail({ code: 'client.unknown' })
        return
      }
      try {
        feedback.succeed(await finishEnrollment(kind, current.label.trim()))
      } catch (failure) {
        feedback.fail(failure)
      }
    } finally {
      if (token === panelTokenRef.current) {
        setEnrolling(false)
      }
    }
  }

  /** Close the panel and give its flow up. A start still in flight releases
   * the flow it opens when its answer lands under the stale token. */
  function cancelEnroll(): void {
    panelTokenRef.current += 1
    releaseEnrollFlow()
    setPanel(null)
    feedback.reset()
    panelFeedback.reset()
  }

  /** Whether removing this factor leaves the account with no other active
   * one: its deletion must not be proved by itself. */
  function isLastActiveFactor(factorId: string): boolean {
    const active = (factors ?? []).filter(
      (factor) => factor.status === 'active',
    )
    return active.length === 1 && active[0]?.id === factorId
  }

  /** Whether a rename answer says the factor is not there, or is not the
   * caller's: the two read as one code so neither says the row exists. */
  function factorIsGone(error: unknown): boolean {
    return errorCodeOf(error) === 'authn.factor_not_found'
  }

  /** Remove one factor. */
  function removeFactor(factorId: string): Promise<void> {
    // Captured before the write: the refetched list drops the row, so the
    // confirmation's name must be read from the list this attempt acted on.
    // It names that row -- its own label when it has one, else its kind --
    // because two factors of one kind can stand side by side.
    const removed = factors?.find((factor) => factor.id === factorId)
    const removedLabel =
      removed === undefined
        ? ''
        : removed.label != null && removed.label !== ''
          ? removed.label
          : factorKindLabel(removed.kind ?? '')
    // Only removing an active factor replaces this session's credential:
    // the route settles the sessions and rotates the requesting one for an
    // active factor, while a pending or disabled one leaves them alone. A
    // name the list no longer carries stays on the rotating side -- the
    // route treats it as already removed and settles the sessions all the
    // same. A non-rotating removal runs bare, because wrapping it would
    // discard a genuine anonymous answer and postpone a real sign-out.
    const rotates = removed === undefined || removed.status === 'active'
    const lastActive = rotates && isLastActiveFactor(factorId)
    return feedback.run(async () => {
      try {
        const write = () => removeMutation.mutateAsync({ id: factorId })
        const settle = async (): Promise<string> => {
          setRemoveTarget(null)
          await invalidateFactors()
          // The confirmation names the factor row removed, and says so when
          // the account is left with a single verification method.
          return t(lastActive ? 'mfa.removedLast' : 'mfa.removed', {
            factor: removedLabel,
            interpolation: { escapeValue: false },
          })
        }
        // Only a rotating removal needs the window: it is open across the
        // write and the refetch that follows it, so a read that left under
        // the replaced credential and answers anonymous is discarded while
        // the request that produced it keeps its own 401. The release is
        // idempotent, and a step-up re-run opens its own window.
        if (rotates) {
          return await runIdentityTransition(session, write, settle)
        }
        await write()
        return await settle()
      } catch (error) {
        setRemoveTarget(null)
        const code = errorCodeOf(error)
        const reason = stepUpReasonOf(code)
        if (reason !== null) {
          setGate({ kind: 'remove', factorId, reason })
          return null
        }
        throw error
      }
    })
  }

  /** Rename one factor. The label is metadata, so no step-up can gate this
   * write; a refusal lands in the row actions' feedback region. A factor
   * that is gone closes the editor and rereads the list once, so the
   * sentence and the list agree. */
  function renameFactor(factorId: string, label: string): Promise<void> {
    return feedback.run(async () => {
      try {
        const updated = await updateMutation.mutateAsync({
          id: factorId,
          data: { label },
        })
        setRenameDraft(null)
        await invalidateFactors()
        // The confirmation names the row by the name the server stored; an
        // empty name means the row reads as its kind.
        const name =
          updated.label !== ''
            ? updated.label
            : factorKindLabel(updated.kind ?? '')
        return t('mfa.renamed', {
          factor: name,
          interpolation: { escapeValue: false },
        })
      } catch (error) {
        if (factorIsGone(error)) {
          setRenameDraft(null)
          await invalidateFactors()
        }
        throw error
      }
    })
  }

  function handleStepUpSuccess(): void {
    const action = gate
    setGate(null)
    if (action?.kind === 'enroll') {
      openEnroll(action.factorKind)
    } else if (action?.kind === 'remove') {
      void removeFactor(action.factorId)
    }
  }

  return (
    <>
      <SectionCard title={t('mfa.title')}>
        <AccountAsyncSection
        pending={pending}
        payload={factors}
        empty={false}
        error={error}
        refreshing={isFetching}
        loading={<FactorListSkeleton label={t('mfa.loading')} />}
        errorState={{
          title: t('mfa.error.title'),
          description: t('mfa.error.description'),
          retryLabel: t('mfa.retry'),
          onRetry: () => void refetch(),
        }}
      >
        {(settledFactors) => {
          // The note above the groups speaks for the enabled rows alone: a
          // pending or disabled factor is not a fallback the account can use
          // to sign in.
          const enabledFactorCount = settledFactors.filter(
            (factor) => factor.status === 'active',
          ).length
          // A pending factor is the enrollment's intermediate state, which
          // the panel holds; only enabled and disabled rows are listed.
          const visibleFactors = settledFactors.filter(
            (factor) => factor.status !== 'pending',
          )
          const factorKinds = Array.from(
            new Set(visibleFactors.map((factor) => factor.kind ?? '')),
          )
          const allKinds = Array.from(
            new Set([...enrollEntries, ...factorKinds]),
          ).filter((kind) => kind !== '')
          // The groups keep one order, fixed by strength: passkey,
          // authenticator app, SMS, then kinds this package does not know. The
          // order does not follow which group is enabled. Kinds outside the
          // known set share the last rank and keep the order they entered
          // `allKinds`: the deployment's list first, then the factor read's
          // oldest-first order.
          const STRENGTH_ORDER = ['webauthn', 'totp', 'sms']
          const rank = (kind: string): number => {
            const position = STRENGTH_ORDER.indexOf(kind)
            return position === -1 ? STRENGTH_ORDER.length : position
          }
          const orderedKinds = [...allKinds].sort((a, b) => rank(a) - rank(b))
          return (
            <Box>
              {enabledFactorCount === 1 && enrollEntries.length > 0 && (
                <Alert
                  severity="info"
                  role="note"
                  sx={{ width: '100%', mb: 2 }}
                >
                  {t('mfa.singleFactor')}
                </Alert>
              )}
              {/* Each assembled kind is its own section: the heading names
                  the method -- or the enrollment in progress, inside that
                  method's group -- and an enrollable group carries one add
                  action on the heading. The groups stand in one fixed
                  strength order, the same whether a method is enabled or
                  still to set up. */}
              {orderedKinds.length === 0 ? (
                <Typography
                  variant="body2"
                  color="text.secondary"
                  sx={{ py: 1 }}
                >
                  {t('mfa.empty')}
                </Typography>
              ) : (
                <Box
                  sx={{
                    display: 'flex',
                    flexDirection: 'column',
                    gap: 3,
                  }}
                >
                  {orderedKinds.map((kind) => {
                    const kindFactors = visibleFactors.filter(
                      (factor) => (factor.kind ?? '') === kind,
                    )
                    const enrollable = enrollEntries.includes(kind)
                    const kindLabel = KNOWN_FACTOR_KINDS.has(kind)
                      ? t(`mfa.factor.kind.${kind}`)
                      : t('mfa.factor.kind.other')
                    const help = METHOD_HELP_KEYS[kind]
                    const open =
                      panel !== null && panel.kind === kind ? panel : null
                    const headingText =
                      open === null ? kindLabel : enrollTitle(kind)
                    // The single authenticator entry is replaced rather than
                    // added to: the entry already there is the one leaving.
                    const replace =
                      kind === 'totp' &&
                      kindFactors.some((factor) => factor.status === 'active')
                    return (
                      <Box
                        component="section"
                        key={kind}
                        aria-label={headingText}
                      >
                        <Box
                          sx={{
                            display: 'flex',
                            alignItems: 'center',
                            gap: 2,
                            minWidth: 0,
                          }}
                        >
                          <Typography
                            variant="subtitle1"
                            component="h3"
                            sx={{
                              flex: '1 1 auto',
                              minWidth: 0,
                              fontWeight: 600,
                            }}
                          >
                            {headingText}
                          </Typography>
                          {open !== null || !enrollable ? null : (
                            <Button
                              variant="outlined"
                              size="small"
                              disabled={busy}
                              data-testid={`mfa-add-${kind}`}
                              // The accessible name carries the visible
                              // action word and names the method, so it
                              // contains the visible label (WCAG 2.5.3)
                              // while still telling several method buttons
                              // apart.
                              aria-label={
                                replace
                                  ? t('mfa.replaceAria')
                                  : t('mfa.addAria', {
                                      method: kindLabel,
                                      interpolation: { escapeValue: false },
                                    })
                              }
                              onClick={() => openEnroll(kind)}
                            >
                              {t(
                                replace
                                  ? 'mfa.action.replace'
                                  : 'mfa.action.add',
                              )}
                            </Button>
                          )}
                        </Box>
                        {open !== null ? (
                          <Box sx={{ maxWidth: FORM_MAX_WIDTH }}>
                            {/* The name field belongs to a kind that may be
                                held more than once: several passkeys or SMS
                                factors are told apart by their names, while
                                the single authenticator entry is the kind
                                itself. */}
                            {takesName(kind) ? (
                              <TextField
                                label={t('mfa.enroll.labelField')}
                                placeholder={t('mfa.enroll.labelPlaceholder')}
                                value={open.label}
                                onChange={(event) =>
                                  setPanel((current) =>
                                    current !== null && current.kind === kind
                                      ? {
                                          ...current,
                                          label: event.target.value,
                                        }
                                      : current,
                                  )
                                }
                                helperText={t('mfa.enroll.labelHelp')}
                                disabled={enrolling}
                                fullWidth
                                sx={{ mt: 2 }}
                              />
                            ) : null}
                            {/* The step states what confirming does; a
                                passkey's registration prompt alone does not
                                carry that sentence. An SMS step also names
                                the phone the code goes to. */}
                            <Typography
                              variant="body2"
                              color="text.secondary"
                              sx={{ mt: 1.5 }}
                            >
                              {enrollNote(kind)}
                            </Typography>
                            {open.step === null ? (
                              <Box sx={{ mt: 2 }}>
                                <Typography variant="body2" color="text.secondary">
                                  {t('mfa.enroll.starting')}
                                </Typography>
                                <ActionRow sx={{ mt: 1.5 }}>
                                  <ActionButton
                                    type="button"
                                    disabled={enrolling}
                                    onClick={cancelEnroll}
                                  >
                                    {t('mfa.enroll.cancelLabel')}
                                  </ActionButton>
                                  <ActionButton
                                    type="button"
                                    emphasis="primary"
                                    disabled={enrolling}
                                    onClick={() => {
                                      const token = ++panelTokenRef.current
                                      void startEnrollFlow(kind, token, open.label)
                                    }}
                                  >
                                    {t('mfa.retry')}
                                  </ActionButton>
                                </ActionRow>
                              </Box>
                            ) : (
                              <FlowStep
                                key={`${open.seq}-${open.step.kind}`}
                                step={open.step}
                                sequence={open.seq}
                                busy={enrolling}
                                answerStep="enroll"
                                submitLabel={t('mfa.enroll.confirmLabel')}
                                onSubmit={(answer) => {
                                  void answerEnroll(open, answer)
                                }}
                                secondaryAction={
                                  <ActionButton
                                    type="button"
                                    disabled={enrolling}
                                    onClick={cancelEnroll}
                                  >
                                    {t('mfa.enroll.cancelLabel')}
                                  </ActionButton>
                                }
                              />
                            )}
                            <EnrollmentFeedback
                              outcome={panelFeedback.outcome}
                            />
                          </Box>
                        ) : (
                          <>
                            {help === undefined ? null : (
                              <Typography
                                variant="body2"
                                color="text.secondary"
                                sx={{ mt: 0.5 }}
                              >
                                {t(help)}
                              </Typography>
                            )}
                            {notice === kind && phoneMissing ? (
                              // No phone to send to: the add entry answers
                              // with a statement about the missing
                              // prerequisite, not a panel, printed under the
                              // method's own description. It carries no
                              // action and no close control; another add
                              // entry, a proved phone or leaving the section
                              // clears it.
                              <Alert
                                severity="info"
                                role="note"
                                sx={{ mt: 1.5 }}
                              >
                                {t('mfa.enroll.prerequisite.sms')}
                              </Alert>
                            ) : null}
                            {kindFactors.length > 0 ? (
                              <Box
                                component="ul"
                                role="list"
                                sx={{
                                  m: 0,
                                  mt: 1.5,
                                  p: 0,
                                  listStyle: 'none',
                                  display: 'flex',
                                  flexDirection: 'column',
                                  gap: 1,
                                }}
                              >
                                {kindFactors.map((factor, factorIndex) => {
                                  const id = factor.id ?? null
                                  const disabled =
                                    factor.status === 'disabled'
                                  // A row of a kind that may be held once
                                  // reads as its kind alone; a kind held
                                  // several times reads as the name its
                                  // holder gave, or the kind when unnamed.
                                  const displayName =
                                    takesName(kind) &&
                                    factor.label != null &&
                                    factor.label !== ''
                                      ? factor.label
                                      : kindLabel
                                  const lastUsed = parseDate(
                                    factor.last_used_at,
                                  )
                                  return (
                                    <Box
                                      component="li"
                                      key={id ?? String(factorIndex)}
                                      sx={{
                                        display: 'flex',
                                        alignItems: 'flex-start',
                                        gap: 1.5,
                                        minWidth: 0,
                                      }}
                                    >
                                      <Box
                                        sx={{
                                          minWidth: 0,
                                          flex: '1 1 auto',
                                        }}
                                      >
                                        <Box
                                          sx={{
                                            display: 'flex',
                                            alignItems: 'center',
                                            gap: 1,
                                            flexWrap: 'wrap',
                                          }}
                                        >
                                          <Typography
                                            variant="body2"
                                            sx={{
                                              fontWeight: 600,
                                              overflowWrap: 'anywhere',
                                            }}
                                          >
                                            {displayName}
                                          </Typography>
                                          {disabled ? (
                                            <Typography
                                              variant="body2"
                                              color="text.secondary"
                                            >
                                              {t(
                                                'mfa.factor.status.disabled',
                                              )}
                                            </Typography>
                                          ) : null}
                                        </Box>
                                        <Typography
                                          variant="body2"
                                          color="text.secondary"
                                          sx={{ mt: 0.5 }}
                                        >
                                          {lastUsed === null
                                            ? t('mfa.factor.neverUsed')
                                            : t('mfa.factor.lastUsedAt', {
                                                time: formatTime(lastUsed),
                                                interpolation: {
                                                  escapeValue: false,
                                                },
                                              })}
                                        </Typography>
                                        {renameDraft !== null &&
                                          renameDraft.id === id && (
                                            <Box
                                              component="form"
                                              onSubmit={(event) => {
                                                event.preventDefault()
                                                void renameFactor(
                                                  renameDraft.id,
                                                  renameDraft.label,
                                                )
                                              }}
                                              // The row's in-place edit: the
                                              // name field owns its line at
                                              // the editor's text-column
                                              // width, and the actions close
                                              // the next line on the right.
                                              sx={{ mt: 1 }}
                                            >
                                              <TextField
                                                size="small"
                                                fullWidth
                                                label={t('mfa.renameLabel')}
                                                value={renameDraft.label}
                                                onChange={(event) =>
                                                  setRenameDraft({
                                                    id: renameDraft.id,
                                                    label: event.target.value,
                                                  })
                                                }
                                                helperText={t(
                                                  'mfa.renameHint',
                                                )}
                                                disabled={
                                                  updateMutation.isPending
                                                }
                                                sx={{ mb: 1 }}
                                              />
                                              <ActionRow
                                                size="small"
                                                sx={{ width: '100%' }}
                                              >
                                                <ActionButton
                                                  type="button"
                                                  disabled={
                                                    updateMutation.isPending
                                                  }
                                                  onClick={() =>
                                                    setRenameDraft(null)
                                                  }
                                                >
                                                  {t('mfa.renameCancel')}
                                                </ActionButton>
                                                <ActionButton
                                                  type="submit"
                                                  emphasis="primary"
                                                  disabled={busy}
                                                >
                                                  {t('mfa.renameSave')}
                                                </ActionButton>
                                              </ActionRow>
                                            </Box>
                                          )}
                                      </Box>
                                      {id !== null ? (
                                        <FactorActions
                                          canRename={
                                            !disabled && takesName(kind)
                                          }
                                          disabled={busy}
                                          onRename={() =>
                                            setRenameDraft({
                                              id,
                                              label: factor.label ?? '',
                                            })
                                          }
                                          onRemove={() => setRemoveTarget(id)}
                                        />
                                      ) : null}
                                    </Box>
                                  )
                                })}
                              </Box>
                            ) : null}
                          </>
                        )}
                      </Box>
                    )
                  })}
                </Box>
              )}

              {/* The section's one result, below the groups the writes
                  belong to. Enrollment, rename and removal share this slot,
                  so the later result replaces the earlier one. */}
              <WriteFeedbackRegion outcome={feedback.outcome} />
            </Box>
          )
        }}
      </AccountAsyncSection>
      </SectionCard>

      <ConfirmDialog
        open={removeTarget !== null}
        title={t('mfa.removeConfirm.title')}
        message={t('mfa.removeConfirm.message')}
        variant="danger"
        confirmLabel={t('mfa.removeConfirm.confirmLabel')}
        confirmLoading={removeMutation.isPending}
        onCancel={() => setRemoveTarget(null)}
        onConfirm={() => {
          if (removeTarget !== null) {
            void removeFactor(removeTarget)
          }
        }}
      />

      <StepUpChallenge
        open={gate !== null}
        reason={gate?.reason}
        session={session}
        onSuccess={handleStepUpSuccess}
        onCancel={() => setGate(null)}
        returnPath={returnPath}
        excludeFactorId={
          gate?.kind === 'remove' ? gate.factorId : undefined
        }
        notice={
          gate?.kind === 'remove' && isLastActiveFactor(gate.factorId)
            ? t('mfa.stepUpDeleteLastNote')
            : undefined
        }
      />
    </>
  )
}
