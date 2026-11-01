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
 * (session.startAuthenticatedFlow) whose steps the wizard answers until the
 * flow settles `enrolled`. Which kinds the deployment carries is its own
 * answer (GET /api/v1/auth/methods' `mfa.factors`, read once through
 * session.listMethods), never a hard-coded set: a deployment whose assembly
 * has no passkey relying party does not offer the passkey entry, so the
 * surface never offers a factor it cannot complete. The read is a session
 * operation on the session the section already drives -- the same seam the
 * enroll flow uses -- and an unresolved read withholds the entries (fail
 * closed) rather than guessing.
 *
 *  - Enroll. The entry opens a name form first: the enrollment step carries
 *    the label with its `kind` (`POST B/flows/authenticated
 *    {purpose:"enroll", input:{kind, label}}`), and later steps do not read
 *    it. The field is prefilled with the browser/OS summary the session rows
 *    show (summarizeUserAgent) so a factor registered from this device is
 *    recognisable, is editable, and may be submitted empty -- the server
 *    stores no name then and the row falls back to its kind. The form has no
 *    required rule. Continue starts the flow; the wizard opens inside the
 *    method's own group and replaces that group's rows, its heading naming
 *    the method being added, and each step states what confirming will do
 *    before its controls. The step's cancel sits on the left of its confirm
 *    with the ordinary action-row spacing. Each reply's next step is
 *    rendered (FlowStep) and answered. A `webauthn` step hands the factor its
 *    own JSON -- TOTP's otpauth_uri, or a passkey's creation options -- of
 *    which the wizard renders TOTP's URI as a QR symbol with a manual-entry
 *    disclosure for its base32 secret, and drives a passkey's options through
 *    the browser's WebAuthn API. The flow settles `enrolled` without changing
 *    this session's level; the factors list is then refetched.
 *  - Abandon. A wizard the reader cancels, or the section unmounting while a
 *    wizard is open, releases the flow (DELETE /flows/current) so the server
 *    drops the pending row the registration wrote instead of leaving it to the
 *    pending sweep. Each wizard instance releases at most once, a settled flow
 *    releases nothing, and a failed release is ignored rather than shown: the
 *    reader has already given the flow up.
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
 * carries one add action on that heading. A passkey group with an enabled
 * row words the action "add another device"; every other group words it
 * "add". The rows a group lists are the enabled and the disabled ones --
 * a pending row is the enrollment's own intermediate state and is withheld
 * -- and a disabled row carries a muted "disabled" marker. Every row shows
 * its name (the label, else the kind) and the instant it last passed a
 * verification, which says so when it has never passed one; the instant is
 * formatted with the account's timezone chain -- the stored preference, the
 * host's display-timezone context, the device zone, then UTC -- the same
 * chain every other surface uses. A row ends in an overflow menu holding
 * rename and removal, the same menu the identifiers surface uses; a disabled
 * row offers removal alone.
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
import Menu from '@mui/material/Menu'
import MenuItem from '@mui/material/MenuItem'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnMfaFactorsQueryKey,
  useAuthnMfaRemoveFactor,
  useAuthnMfaUpdateFactor,
} from '@speed/api-sdk'
import type { AuthSession, AuthnStep, StepUpReason } from '@speed/auth-core'
import { stepUpReasonOf } from '@speed/auth-core'
import { formatInstant } from '@speed/i18n'
import {
  ConfirmDialog,
  errorCodeOf,
  FORM_MAX_WIDTH,
  ListSkeleton,
  SectionCard,
  useWriteFeedback,
} from '@speed/ui-kit'
import { AccountAsyncSection } from './internal/async-section.js'
import { FlowStep } from './internal/flow-step.js'
import type { FlowStepAnswer } from './internal/flow-step.js'
import { runIdentityTransition } from './internal/identity-transition.js'
import { useFactorsQuery } from './internal/queries.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountTimeZone } from './internal/time-zone.js'
import { useAccountUiTranslation } from './internal/translation.js'
import { summarizeUserAgent } from './internal/ua-summary.js'
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
      readonly label: string
      readonly reason: StepUpReason
    }
  | {
      readonly kind: 'remove'
      readonly factorId: string
      readonly reason: StepUpReason
    }

/** The open enrollment wizard: the step it is parked on, its sequence, and
 * the factor kind it is enrolling -- its heading names that kind on every
 * step. */
interface EnrollWizard {
  readonly step: AuthnStep
  readonly seq: number
  readonly kind: string
}

/** The enrollment's name form: the kind the entry was clicked for and the
 * label typed so far. It is shown before the flow starts because the label
 * travels with the flow's first (`enroll`) input. */
interface EnrollDraft {
  readonly kind: string
  readonly label: string
}

/** The row whose name is being edited: its identifier and the input value. */
interface RenameDraft {
  readonly id: string
  readonly label: string
}

/** The name suggested for a factor registered from this browser: the same
 * short browser/OS summary the session rows show. An unrecognised user
 * agent suggests no name; the reader can type one or leave it empty. */
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
 * per factor kind. The sentence renders on every step of that kind's
 * wizard, including a passkey's registration step, whose prompt alone does
 * not say what the confirmation does. */
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

/** One factor row's overflow menu: rename it and remove it. A disabled
 * factor offers removal alone. The trigger carries the row's own accessible
 * name, and the menu is the identifiers surface's overflow menu shape. */
function FactorActionsMenu({
  factorName,
  canRename,
  disabled,
  onRename,
  onRemove,
}: {
  readonly factorName: string
  readonly canRename: boolean
  readonly disabled: boolean
  readonly onRename: () => void
  readonly onRemove: () => void
}) {
  const { t } = useAccountUiTranslation()
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  return (
    <>
      <Button
        variant="outlined"
        size="small"
        disabled={disabled}
        aria-label={t('mfa.actionsAria', {
          factor: factorName,
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
        {canRename ? (
          <MenuItem
            onClick={() => {
              setAnchor(null)
              onRename()
            }}
          >
            {t('mfa.rename')}
          </MenuItem>
        ) : null}
        <MenuItem
          sx={{ color: 'error.main' }}
          onClick={() => {
            setAnchor(null)
            onRemove()
          }}
        >
          {t('mfa.remove')}
        </MenuItem>
      </Menu>
    </>
  )
}

export function MfaSection({ session, returnPath }: MfaSectionProps) {
  const { t, i18n } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, isFetching, error, refetch } = useFactorsQuery()
  const removeMutation = useAuthnMfaRemoveFactor()
  const updateMutation = useAuthnMfaUpdateFactor()

  // The action a won step-up re-runs, if any.
  const [gate, setGate] = useState<GatedAction | null>(null)
  // The open enrollment's name form, if any.
  const [enrollDraft, setEnrollDraft] = useState<EnrollDraft | null>(null)
  // The open enrollment, if any.
  const [wizard, setWizard] = useState<EnrollWizard | null>(null)
  // The enrollment's in-flight state.
  const [enrolling, setEnrolling] = useState(false)
  // The row whose removal is being confirmed.
  const [removeTarget, setRemoveTarget] = useState<string | null>(null)
  // The row whose name is being edited, if any.
  const [renameDraft, setRenameDraft] = useState<RenameDraft | null>(null)
  // The kind whose enrollment owns the group-level feedback slot, if any.
  // It outlives the wizard and the name form, so a landed enrollment's
  // confirmation still renders in the group that produced it.
  const [enrollKind, setEnrollKind] = useState<string | null>(null)
  // Enrollment, rename and removal are separate control groups with their
  // own scopes: the enrollment result sits below its entries or form, and
  // the row-action results sit below the list, so one group's outcome never
  // clears another's.
  const enrollFeedback = useWriteFeedback()
  const removeFeedback = useWriteFeedback()
  const renameFeedback = useWriteFeedback()
  // The deployment's enrollable factor kinds (GET /api/v1/auth/methods'
  // mfa.factors). null is unresolved -- still reading, or the read failed:
  // the enroll entries are then withheld rather than guessed.
  const [enrollableKinds, setEnrollableKinds] = useState<
    readonly string[] | null
  >(null)

  // Whether this wizard holds an open flow of its own to release: set when a
  // reply parks a step, cleared when the flow settles or is abandoned. Each
  // wizard instance releases at most once.
  const flowOpenRef = useRef(false)
  // The unmount release is deferred by one microtask, so a remount in the same
  // commit (StrictMode's double effect invocation) cancels it: the remounted
  // section goes on holding the same flow.
  const releasePendingRef = useRef(false)
  // The session the release targets, read through a ref so the unmount cleanup
  // needs no dependency and never re-registers.
  const sessionRef = useRef(session)
  sessionRef.current = session

  /** Release the open enrollment flow, at most once per wizard instance. A
   * failure is ignored: giving up a flow must not put an error in front of
   * the reader. */
  function releaseEnrollFlow(): void {
    if (!flowOpenRef.current) {
      return
    }
    flowOpenRef.current = false
    void session.abandonFlow().catch(() => undefined)
  }

  // Unmounting is a departure like the wizard's cancel: a flow this section
  // opened and has not seen settle is released, so no pending factor row and
  // no stale binding linger on the server. The release waits one microtask so
  // StrictMode's double mount cancels it in the remount's setup.
  useEffect(() => {
    releasePendingRef.current = false
    return () => {
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

  const factors = data?.factors
  const pending = isPending && data === undefined
  // Disabled through the whole attempt, including the refetches that follow
  // the mutation, so no control changes context under a pending scope.
  const busy =
    enrolling ||
    removeMutation.isPending ||
    updateMutation.isPending ||
    enrollFeedback.pending ||
    removeFeedback.pending ||
    renameFeedback.pending

  /** The translated name of a factor kind, the guarded vocabulary's generic
   * label for a kind this package has no wording for. */
  function factorKindLabel(kind: string): string {
    return KNOWN_FACTOR_KINDS.has(kind)
      ? t(`mfa.factor.kind.${kind}`)
      : t('mfa.factor.kind.other')
  }
  /** The enrollment wizard's heading for one factor kind: the one
   * parameterized title key, with the kind's own name in its method slot. */
  function enrollTitle(kind: string): string {
    return t('mfa.enroll.title', {
      method: factorKindLabel(kind),
      interpolation: { escapeValue: false },
    })
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

  /** Open the enrollment's name form for one factor kind. The suggested
   * name is filled in but editable, and submitting it empty is allowed: the
   * server then stores no name. */
  function openEnroll(factorKind: string): void {
    enrollFeedback.reset()
    setEnrollKind(factorKind)
    setEnrollDraft({ kind: factorKind, label: suggestedFactorName() })
  }

  /** Start (or restart) an enrollment for one factor kind and label. The
   * label travels with the flow's first input, so a retry after a won
   * step-up sends the same one. */
  function startEnroll(factorKind: string, label: string): Promise<void> {
    return enrollFeedback.run(async () => {
      setEnrollKind(factorKind)
      setEnrolling(true)
      try {
        const state = await session.startAuthenticatedFlow({
          purpose: 'enroll',
          input: { kind: factorKind, label },
          ...(returnPath === undefined ? {} : { return_to: returnPath }),
        })
        setEnrollDraft(null)
        if ('next' in state) {
          flowOpenRef.current = true
          setWizard({ step: state.next, seq: state.seq, kind: factorKind })
          return null
        }
        // A settled flow has spent its state, so there is nothing to release.
        flowOpenRef.current = false
        if (state.state === 'enrolled') {
          setWizard(null)
          await invalidateFactors()
          return t('mfa.enroll.enrolled', {
            factor: factorKindLabel(factorKind),
            interpolation: { escapeValue: false },
          })
        }
        // A settled state other than 'enrolled' is not an enroll outcome.
        enrollFeedback.fail({ code: 'client.unknown' })
        return null
      } catch (error) {
        const code = errorCodeOf(error)
        const reason = stepUpReasonOf(code)
        if (reason !== null) {
          setEnrollDraft(null)
          setGate({ kind: 'enroll', factorKind, label, reason })
          return null
        }
        throw error
      } finally {
        setEnrolling(false)
      }
    })
  }

  /** Answer the open enrollment's current step. */
  function answerEnroll(answer: FlowStepAnswer): Promise<void> {
    if (wizard === null) {
      return Promise.resolve()
    }
    const kind = wizard.kind
    return enrollFeedback.run(async () => {
      setEnrolling(true)
      try {
        const state = await session.answerFlow({
          seq: wizard.seq,
          step: answer.step,
          input: answer.input,
        })
        if ('next' in state) {
          flowOpenRef.current = true
          setWizard({ step: state.next, seq: state.seq, kind })
          return null
        }
        // A settled flow has spent its state, so there is nothing to release.
        flowOpenRef.current = false
        if (state.state === 'enrolled') {
          setWizard(null)
          await invalidateFactors()
          return t('mfa.enroll.enrolled', {
            factor: factorKindLabel(kind),
            interpolation: { escapeValue: false },
          })
        }
        // A settled state other than 'enrolled' is not an enroll outcome.
        setWizard(null)
        enrollFeedback.fail({ code: 'client.unknown' })
        return null
      } finally {
        setEnrolling(false)
      }
    })
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
    return removeFeedback.run(async () => {
      try {
        const write = () => removeMutation.mutateAsync({ id: factorId })
        const settle = async (): Promise<string> => {
          setRemoveTarget(null)
          await invalidateFactors()
          // The confirmation names the factor row removed.
          return t('mfa.removed', {
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
    return renameFeedback.run(async () => {
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
      void startEnroll(action.factorKind, action.label)
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
          // the wizard holds; only enabled and disabled rows are listed.
          const visibleFactors = settledFactors.filter(
            (factor) => factor.status !== 'pending',
          )
          const factorKinds = Array.from(
            new Set(visibleFactors.map((factor) => factor.kind ?? '')),
          )
          const allKinds = Array.from(
            new Set([...enrollEntries, ...factorKinds]),
          ).filter((kind) => kind !== '')
          const isActive = (kind: string): boolean =>
            visibleFactors.some(
              (factor) =>
                (factor.kind ?? '') === kind && factor.status === 'active',
            )
          const STRENGTH_ORDER = ['webauthn', 'totp', 'sms']
          const rank = (kind: string): number => {
            const position = STRENGTH_ORDER.indexOf(kind)
            return position === -1 ? STRENGTH_ORDER.length : position
          }
          const orderedKinds = [...allKinds].sort((a, b) => {
            const activeDifference = Number(isActive(b)) - Number(isActive(a))
            return activeDifference !== 0
              ? activeDifference
              : rank(a) - rank(b)
          })
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
                  action on the heading. Methods with an enabled factor come
                  first, then the ones still to set up. */}
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
                    const activeCount = kindFactors.filter(
                      (factor) => factor.status === 'active',
                    ).length
                    const enrollable = enrollEntries.includes(kind)
                    const kindLabel = KNOWN_FACTOR_KINDS.has(kind)
                      ? t(`mfa.factor.kind.${kind}`)
                      : t('mfa.factor.kind.other')
                    const help = METHOD_HELP_KEYS[kind]
                    const wizardHere =
                      wizard !== null && wizard.kind === kind
                    const draftHere =
                      enrollDraft !== null && enrollDraft.kind === kind
                    const inEnrollment = wizardHere || draftHere
                    const headingText = inEnrollment
                      ? enrollTitle(kind)
                      : kindLabel
                    // A passkey can be added once per device; every other
                    // kind keeps one action word whether or not it already
                    // has an enabled row.
                    const addAnother =
                      kind === 'webauthn' && activeCount > 0
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
                          {inEnrollment || !enrollable ? null : (
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
                                addAnother
                                  ? t('mfa.manageAria', {
                                      method: kindLabel,
                                      interpolation: { escapeValue: false },
                                    })
                                  : t('mfa.addAria', {
                                      method: kindLabel,
                                      interpolation: { escapeValue: false },
                                    })
                              }
                              onClick={() => openEnroll(kind)}
                            >
                              {addAnother
                                ? t('mfa.action.manage')
                                : t('mfa.action.add')}
                            </Button>
                          )}
                        </Box>
                        {wizardHere ? (
                          <Box sx={{ maxWidth: FORM_MAX_WIDTH }}>
                            {/* Every step states what confirming does; a
                                passkey's registration prompt alone does not
                                carry that sentence. */}
                            <Typography
                              variant="body2"
                              color="text.secondary"
                              sx={{ mt: 0.5 }}
                            >
                              {t(enrollNoteKey(kind))}
                            </Typography>
                            <FlowStep
                              key={`${wizard.seq}-${wizard.step.kind}`}
                              step={wizard.step}
                              sequence={wizard.seq}
                              busy={enrolling}
                              answerStep="enroll"
                              submitLabel={t('mfa.enroll.confirmLabel')}
                              onSubmit={(answer) => void answerEnroll(answer)}
                              secondaryAction={
                                <Button
                                  type="button"
                                  disabled={enrolling}
                                  onClick={() => {
                                    releaseEnrollFlow()
                                    setWizard(null)
                                    enrollFeedback.reset()
                                  }}
                                >
                                  {t('mfa.enroll.cancelLabel')}
                                </Button>
                              }
                            />
                            <WriteFeedbackRegion
                              outcome={enrollFeedback.outcome}
                            />
                          </Box>
                        ) : draftHere ? (
                          <Box sx={{ maxWidth: FORM_MAX_WIDTH }}>
                            {/* The name travels with the flow's first input,
                                so it is collected before the flow starts.
                                The field is prefilled, editable and may be
                                submitted empty. Cancel and confirm share one
                                action row, cancel on the left. */}
                            <Box
                              component="form"
                              onSubmit={(event) => {
                                event.preventDefault()
                                void startEnroll(
                                  enrollDraft.kind,
                                  enrollDraft.label,
                                )
                              }}
                              sx={{ mt: 2 }}
                            >
                              <TextField
                                label={t('mfa.enroll.labelField')}
                                value={enrollDraft.label}
                                onChange={(event) =>
                                  setEnrollDraft({
                                    kind: enrollDraft.kind,
                                    label: event.target.value,
                                  })
                                }
                                helperText={t('mfa.enroll.labelHelp')}
                                disabled={enrolling}
                                fullWidth
                              />
                              <Box
                                sx={{
                                  display: 'flex',
                                  alignItems: 'center',
                                  gap: 1.5,
                                  mt: 2,
                                }}
                              >
                                <Button
                                  type="button"
                                  disabled={enrolling}
                                  onClick={() => {
                                    setEnrollDraft(null)
                                    enrollFeedback.reset()
                                  }}
                                >
                                  {t('mfa.enroll.cancelLabel')}
                                </Button>
                                <Button
                                  type="submit"
                                  variant="contained"
                                  disabled={enrolling}
                                >
                                  {t('mfa.enroll.confirmLabel')}
                                </Button>
                              </Box>
                            </Box>
                            <WriteFeedbackRegion
                              outcome={enrollFeedback.outcome}
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
                                  // The row reads as its name; an unnamed
                                  // factor reads as its kind.
                                  const displayName =
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
                                              sx={{
                                                mt: 1,
                                                display: 'flex',
                                                alignItems: 'flex-start',
                                                gap: 1,
                                                flexWrap: 'wrap',
                                              }}
                                            >
                                              <TextField
                                                size="small"
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
                                                sx={{
                                                  flex: '1 1 220px',
                                                  minWidth: 0,
                                                }}
                                              />
                                              <Button
                                                type="submit"
                                                variant="contained"
                                                size="small"
                                                disabled={busy}
                                              >
                                                {t('mfa.renameSave')}
                                              </Button>
                                              <Button
                                                type="button"
                                                size="small"
                                                disabled={
                                                  updateMutation.isPending
                                                }
                                                onClick={() =>
                                                  setRenameDraft(null)
                                                }
                                              >
                                                {t('mfa.renameCancel')}
                                              </Button>
                                            </Box>
                                          )}
                                      </Box>
                                      {id !== null ? (
                                        <FactorActionsMenu
                                          factorName={displayName}
                                          canRename={!disabled}
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
                            {enrollKind === kind ? (
                              <Box sx={{ mt: 2 }}>
                                <WriteFeedbackRegion
                                  outcome={enrollFeedback.outcome}
                                />
                              </Box>
                            ) : null}
                          </>
                        )}
                      </Box>
                    )
                  })}
                </Box>
              )}

              {/* The row actions' results, below the groups they belong to.
                  Rename and removal each own a scope so one action's
                  outcome never clears the other's. */}
              <WriteFeedbackRegion outcome={removeFeedback.outcome} />
              <WriteFeedbackRegion outcome={renameFeedback.outcome} />
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
      />
    </>
  )
}
