/**
 * MfaSection: the two-step-verification surface, the account's factors and the
 * deployment's enrollment entries.
 */

import { useContext, useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
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
  SectionSurfaceContext,
  useActivationGuard,
  useWriteFeedback,
  type WriteOutcome,
} from '@speed/ui-kit'
import { AccountAsyncSection } from './internal/async-section.js'
import { FlowStep } from './internal/flow-step.js'
import type { FlowStepAnswer } from './internal/flow-step.js'
import { runIdentityTransition } from './internal/identity-transition.js'
import { asFactorList } from './internal/models.js'
import { useFactorsQuery, useProfileQuery } from './internal/queries.js'
import { StepUpChallengeFrame } from './internal/step-up-challenge.js'
import { useAccountTimeZone } from './internal/time-zone.js'
import { summarizeUserAgent } from './internal/ua-summary.js'
import { useAccountUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'

/** The host supplies the session and return path. */
export interface MfaSectionProps {
  /** The session that drives the enroll flow and the step-up dialog. */
  readonly session: AuthSession
  /** The host's current path, sent as the enroll and step-up flows'
   * `return_to`. */
  readonly returnPath?: string
}

const KNOWN_FACTOR_KINDS = new Set<string>(['totp', 'webauthn', 'sms'])

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

interface EnrollPanel {
  readonly kind: string
  readonly label: string
  readonly step: AuthnStep | null
  readonly seq: number
}

interface RenameDraft {
  readonly id: string
  readonly label: string
}

interface RemovedLanding {
  readonly id: string
  readonly kind: string
  readonly siblings: readonly string[]
  readonly control: HTMLElement | null
  readonly outcome: 'removed' | 'refused'
}

interface RenameLanding {
  readonly id: string
  readonly kind: string
  readonly siblings: readonly string[]
  readonly removed: boolean
}

interface EnrollLanding {
  readonly kind: string
}

// Focus inside this section's own dialogs counts as lost; any other dialog is
// the host shell's.
function focusFellWith(
  control: HTMLElement | null,
  ownDialogs: readonly (HTMLElement | null)[],
): boolean {
  const active = document.activeElement
  if (active === null || active === document.body) {
    return true
  }
  if (!(active instanceof HTMLElement) || !active.isConnected) {
    return true
  }
  if (control !== null && control.contains(active)) {
    return true
  }
  return ownDialogs.some(
    (dialog) => dialog !== null && dialog.contains(active),
  )
}

type AdvanceOutcome =
  | { readonly kind: 'state'; readonly state: AuthnFlowState }
  | {
      readonly kind: 'retry'
      readonly step: AuthnStep
      readonly seq: number
      readonly failure: unknown
    }

// Only a passkey suggests the browser summary; an SMS factor names the account's phone.
function suggestedFactorName(): string {
  if (typeof navigator === 'undefined') {
    return ''
  }
  return summarizeUserAgent(navigator.userAgent) ?? ''
}

function parseDate(iso: string | undefined): Date | null {
  if (iso === undefined) {
    return null
  }
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? null : date
}

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

const METHOD_HELP_KEYS: Readonly<Record<string, string>> = {
  totp: 'mfa.method.totp',
  webauthn: 'mfa.method.webauthn',
  sms: 'mfa.method.sms',
}

const ENROLL_NOTE_KEYS: Readonly<Record<string, string>> = {
  totp: 'mfa.enroll.note.totp',
  webauthn: 'mfa.enroll.note.webauthn',
  sms: 'mfa.enroll.note.sms',
}

function enrollNoteKey(kind: string): string {
  return ENROLL_NOTE_KEYS[kind] ?? 'mfa.enroll.note.other'
}

function FactorActions({
  canRename,
  removeBusy,
  unavailable,
  onRename,
  onRemove,
  renameRef,
  removeRef,
}: {
  readonly canRename: boolean
  readonly removeBusy: boolean
  readonly unavailable: boolean
  readonly onRename: () => void
  readonly onRemove: () => void
  readonly renameRef: (element: HTMLButtonElement | null) => void
  readonly removeRef: (element: HTMLButtonElement | null) => void
}) {
  const { t } = useAccountUiTranslation()
  // A busy button already reports unavailable; it gives no reason.
  const removeUnavailable = !removeBusy && unavailable
  if (!canRename) {
    return (
      <ActionButton
        ref={removeRef}
        size="small"
        tone="danger"
        busy={removeBusy}
        unavailable={removeUnavailable}
        onClick={onRemove}
      >
        {t('mfa.remove')}
      </ActionButton>
    )
  }
  return (
    <ActionRow size="small">
      <ActionButton ref={renameRef} unavailable={unavailable} onClick={onRename}>
        {t('mfa.rename')}
      </ActionButton>
      <ActionButton
        ref={removeRef}
        tone="danger"
        busy={removeBusy}
        unavailable={removeUnavailable}
        onClick={onRemove}
      >
        {t('mfa.remove')}
      </ActionButton>
    </ActionRow>
  )
}

// Read-only, not disabled: the field stays focusable while a write runs.
function FactorRenameForm({
  label,
  busy,
  locked,
  onLabelChange,
  onCancel,
  onSave,
}: {
  readonly label: string
  readonly busy: boolean
  readonly locked: boolean
  readonly onLabelChange: (label: string) => void
  readonly onCancel: () => void
  readonly onSave: () => void
}) {
  const { t } = useAccountUiTranslation()
  const guardedSubmit = useActivationGuard(
    (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault()
      onSave()
    },
    { busy, unavailable: locked && !busy },
  )
  return (
    <Box
      component="form"
      onSubmit={guardedSubmit}
      sx={{ mt: 1 }}
    >
      <TextField
        size="small"
        fullWidth
        label={t('mfa.renameLabel')}
        value={label}
        onChange={(event) => onLabelChange(event.target.value)}
        helperText={t('mfa.renameHint')}
        slotProps={{ htmlInput: { readOnly: locked } }}
        sx={{ mb: 1 }}
      />
      <ActionRow size="small" sx={{ width: '100%' }}>
        <ActionButton
          type="button"
          unavailable={locked}
          onClick={onCancel}
        >
          {t('mfa.renameCancel')}
        </ActionButton>
        <ActionButton
          type="submit"
          emphasis="primary"
          busy={busy}
          unavailable={!busy && locked}
        >
          {t('mfa.renameSave')}
        </ActionButton>
      </ActionRow>
    </Box>
  )
}

function EnrollmentFeedback({
  outcome,
}: {
  readonly outcome: WriteOutcome | null
}) {
  return <WriteFeedbackRegion outcome={outcome} />
}

/**
 * The two-step-verification surface: enrolls and removes factors through
 * step-up dialogs, each write reporting beside its controls.
 */
export function MfaSection({ session, returnPath }: MfaSectionProps) {
  const { t, i18n } = useAccountUiTranslation()
  // A plain surface's accessible name must differ from the host block's.
  const surface = useContext(SectionSurfaceContext)
  const queryClient = useQueryClient()
  const { data, isPending, isFetching, error, refetch } = useFactorsQuery()
  const { data: profile } = useProfileQuery()
  const provenPhone =
    profile?.identifiers?.find(
      (identifier) =>
        identifier.kind === 'phone' && identifier.proven === true,
    )?.value ?? null
  // An absent collection is not read as "no phone"; the server still judges.
  const phoneMissing =
    profile?.identifiers !== undefined && provenPhone === null
  const removeMutation = useAuthnMfaRemoveFactor()
  const updateMutation = useAuthnMfaUpdateFactor()

  const [gate, setGate] = useState<GatedAction | null>(null)
  const [panel, setPanel] = useState<EnrollPanel | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [enrolling, setEnrolling] = useState(false)
  const [removeTarget, setRemoveTarget] = useState<string | null>(null)
  const [pendingRemoveId, setPendingRemoveId] = useState<string | null>(null)
  const [pendingRenameId, setPendingRenameId] = useState<string | null>(null)
  const [startOrigin, setStartOrigin] = useState<'add' | 'retry' | null>(null)
  const [renameDraft, setRenameDraft] = useState<RenameDraft | null>(null)
  const removedLanding = useRef<RemovedLanding | null>(null)
  const renameLanding = useRef<RenameLanding | null>(null)
  const enrollLanding = useRef<EnrollLanding | null>(null)
  const removeButtons = useRef(new Map<string, HTMLButtonElement>())
  const renameButtons = useRef(new Map<string, HTMLButtonElement>())
  const addButtons = useRef(new Map<string, HTMLButtonElement>())
  const removeRefs = useRef(
    new Map<string, (element: HTMLButtonElement | null) => void>(),
  )
  const renameRefs = useRef(
    new Map<string, (element: HTMLButtonElement | null) => void>(),
  )
  const addRefs = useRef(
    new Map<string, (element: HTMLButtonElement | null) => void>(),
  )
  const groupSections = useRef(new Map<string, HTMLElement>())
  const groupRefs = useRef(
    new Map<string, (element: HTMLElement | null) => void>(),
  )
  const groupsContainer = useRef<HTMLDivElement | null>(null)
  const confirmDialog = useRef<HTMLDivElement | null>(null)
  const stepUpDialog = useRef<HTMLDivElement | null>(null)
  const feedback = useWriteFeedback()
  const panelFeedback = useWriteFeedback()
  const [enrollableKinds, setEnrollableKinds] = useState<
    readonly string[] | null
  >(null)

  const flowOpenRef = useRef(false)
  // Deferred one microtask, so a StrictMode remount cancels the release.
  const releasePendingRef = useRef(false)
  const sessionRef = useRef(session)
  sessionRef.current = session
  const panelTokenRef = useRef(0)
  const mountedRef = useRef(true)
  const knownIdsRef = useRef<ReadonlySet<string>>(new Set())

  function releaseEnrollFlow(): void {
    if (!flowOpenRef.current) {
      return
    }
    flowOpenRef.current = false
    void session.abandonFlow().catch(() => undefined)
  }

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
        // Fail closed: never offer a factor the deployment may not carry.
        if (active) {
          setEnrollableKinds([])
        }
      }
    })()
    return () => {
      active = false
    }
  }, [session])

  useEffect(() => {
    if (provenPhone !== null) {
      setNotice(null)
    }
  }, [provenPhone])

  const factors = data?.factors
  const pending = isPending && data === undefined
  const writeInFlight =
    enrolling ||
    feedback.pending ||
    pendingRemoveId !== null ||
    pendingRenameId !== null

  function actionRefFor(
    key: string,
    buttons: Map<string, HTMLButtonElement>,
    refs: Map<string, (element: HTMLButtonElement | null) => void>,
  ): (element: HTMLButtonElement | null) => void {
    let ref = refs.get(key)
    if (ref === undefined) {
      ref = (element) => {
        if (element === null) {
          buttons.delete(key)
        } else {
          buttons.set(key, element)
        }
      }
      refs.set(key, ref)
    }
    return ref
  }

  function removeRefFor(
    id: string,
  ): (element: HTMLButtonElement | null) => void {
    return actionRefFor(id, removeButtons.current, removeRefs.current)
  }

  function renameRefFor(
    id: string,
  ): (element: HTMLButtonElement | null) => void {
    return actionRefFor(id, renameButtons.current, renameRefs.current)
  }

  function addRefFor(
    kind: string,
  ): (element: HTMLButtonElement | null) => void {
    return actionRefFor(kind, addButtons.current, addRefs.current)
  }

  function groupRefFor(kind: string): (element: HTMLElement | null) => void {
    let ref = groupRefs.current.get(kind)
    if (ref === undefined) {
      ref = (element) => {
        if (element === null) {
          groupSections.current.delete(kind)
        } else {
          groupSections.current.set(kind, element)
        }
      }
      groupRefs.current.set(kind, ref)
    }
    return ref
  }

  function focusKindGroup(kind: string): void {
    const group = groupSections.current.get(kind)
    ;(group ?? groupsContainer.current)?.focus()
  }

  function siblingIdsOf(kind: string): readonly string[] {
    return (factors ?? []).flatMap((factor) =>
      factor.status !== 'pending' &&
      (factor.kind ?? '') === kind &&
      factor.id !== undefined
        ? [factor.id]
        : [],
    )
  }

  function landOnNeighbour(
    id: string,
    order: readonly string[],
    buttons: ReadonlyMap<string, HTMLButtonElement>,
    kind: string,
  ): void {
    const index = order.indexOf(id)
    const candidates =
      index === -1
        ? [...order]
        : [...order.slice(index + 1), ...order.slice(0, index).reverse()]
    for (const candidate of candidates) {
      if (candidate === id) {
        continue
      }
      const button = buttons.get(candidate)
      if (button !== undefined && button.isConnected) {
        button.focus()
        return
      }
    }
    focusKindGroup(kind)
  }

  useLayoutEffect(() => {
    let observer: MutationObserver | null = null
    const ownDialogs = [confirmDialog.current, stepUpDialog.current]
    const removal = removedLanding.current
    if (removal !== null) {
      const remaining = (factors ?? []).some(
        (factor) => factor.id === removal.id,
      )
      if (remaining) {
        if (pendingRemoveId === null && gate === null) {
          if (removal.outcome === 'refused') {
            const dialog = confirmDialog.current
            const land = (): void => {
              if (removedLanding.current !== removal) {
                observer?.disconnect()
                return
              }
              if (dialog !== null && dialog.isConnected) {
                return
              }
              removedLanding.current = null
              observer?.disconnect()
              if (focusFellWith(removal.control, ownDialogs)) {
                removeButtons.current.get(removal.id)?.focus()
              }
            }
            land()
            if (removedLanding.current === removal) {
              observer = new MutationObserver(land)
              observer.observe(document.body, { childList: true })
            }
          } else {
            removedLanding.current = null
          }
        }
      } else {
        removedLanding.current = null
        if (focusFellWith(removal.control, ownDialogs)) {
          landOnNeighbour(
            removal.id,
            removal.siblings,
            removeButtons.current,
            removal.kind,
          )
        }
      }
    }
    const rename = renameLanding.current
    if (rename !== null && renameDraft === null) {
      const remaining =
        !rename.removed &&
        (factors ?? []).some((factor) => factor.id === rename.id)
      if (remaining) {
        if (pendingRenameId === null) {
          renameLanding.current = null
        }
        if (focusFellWith(null, ownDialogs)) {
          renameButtons.current.get(rename.id)?.focus()
        }
      } else {
        renameLanding.current = null
        if (focusFellWith(null, ownDialogs)) {
          landOnNeighbour(
            rename.id,
            rename.siblings,
            renameButtons.current,
            rename.kind,
          )
        }
      }
    }
    const enroll = enrollLanding.current
    if (enroll !== null && panel === null && gate === null) {
      enrollLanding.current = null
      if (focusFellWith(null, ownDialogs)) {
        const add = addButtons.current.get(enroll.kind)
        if (add !== undefined && add.isConnected) {
          add.focus()
        } else {
          focusKindGroup(enroll.kind)
        }
      }
    }
    return () => {
      observer?.disconnect()
    }
  }, [factors, renameDraft, panel, gate, pendingRemoveId, pendingRenameId])

  function takesName(kind: string): boolean {
    return kind !== 'totp'
  }

  function factorKindLabel(kind: string): string {
    return KNOWN_FACTOR_KINDS.has(kind)
      ? t(`mfa.factor.kind.${kind}`)
      : t('mfa.factor.kind.other')
  }
  function enrollTitle(kind: string): string {
    return t('mfa.enroll.title', {
      method: factorKindLabel(kind),
      interpolation: { escapeValue: false },
    })
  }
  function enrollNote(kind: string): string {
    if (kind === 'sms' && provenPhone !== null) {
      return t('mfa.enroll.note.smsTo', {
        phone: provenPhone,
        interpolation: { escapeValue: false },
      })
    }
    return t(enrollNoteKey(kind))
  }
  const enrollEntries = enrollableKinds ?? []

  function invalidateFactors(): Promise<void> {
    return queryClient.invalidateQueries({
      queryKey: getAuthnMfaFactorsQueryKey(),
    })
  }

  // A bare `complete` step carries no input, so it is submitted on the
  // reader's one confirm. A seq is submitted at most once; a refusal parks the
  // step with its own confirm for a manual retry.
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

  // The flow's first input carried no label, so the typed name is written to
  // the row the enrollment added.
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
      panelTokenRef.current += 1
      setPanel(null)
      setNotice(factorKind)
      return
    }
    setNotice(null)
    const token = ++panelTokenRef.current
    const label =
      factorKind === 'webauthn' ? suggestedFactorName() : ''
    setPanel({ kind: factorKind, label, step: null, seq: 0 })
    void startEnrollFlow(factorKind, token, label, 'add')
  }

  async function startEnrollFlow(
    factorKind: string,
    token: number,
    label: string,
    origin: 'add' | 'retry',
  ): Promise<void> {
    feedback.reset()
    panelFeedback.reset()
    setStartOrigin(origin)
    setEnrolling(true)
    try {
      const started = await session.startAuthenticatedFlow({
        purpose: 'enroll',
        input: { kind: factorKind, label: '' },
        ...(returnPath === undefined ? {} : { return_to: returnPath }),
      })
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
      if (state.state === 'enrolled') {
        enrollLanding.current = { kind: factorKind }
      }
      setPanel(null)
      if (state.state === 'enrolled') {
        try {
          feedback.succeed(await finishEnrollment(factorKind, label.trim()))
        } catch (failure) {
          feedback.fail(failure)
        }
        return
      }
      feedback.fail({ code: 'client.unknown' })
    } catch (failure) {
      if (token !== panelTokenRef.current || !mountedRef.current) {
        return
      }
      const reason = stepUpReasonOf(errorCodeOf(failure))
      if (reason !== null) {
        setPanel(null)
        setGate({ kind: 'enroll', factorKind, reason })
        return
      }
      panelFeedback.fail(failure)
    } finally {
      if (token === panelTokenRef.current) {
        setEnrolling(false)
        setStartOrigin(null)
      }
    }
  }

  async function answerEnroll(
    current: EnrollPanel,
    answer: FlowStepAnswer,
  ): Promise<void> {
    if (current.step === null) {
      return
    }
    const token = panelTokenRef.current
    const kind = current.kind
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
        panelFeedback.fail(failure)
        return
      }
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
      if (state.state === 'enrolled') {
        enrollLanding.current = { kind }
      }
      setPanel(null)
      if (state.state !== 'enrolled') {
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
        setStartOrigin(null)
      }
    }
  }

  function cancelEnroll(): void {
    panelTokenRef.current += 1
    releaseEnrollFlow()
    if (panel !== null) {
      enrollLanding.current = { kind: panel.kind }
    }
    setPanel(null)
    feedback.reset()
    panelFeedback.reset()
  }

  // Its deletion must not be proved by itself.
  function isLastActiveFactor(factorId: string): boolean {
    const active = (factors ?? []).filter(
      (factor) => factor.status === 'active',
    )
    return active.length === 1 && active[0]?.id === factorId
  }

  function factorIsGone(error: unknown): boolean {
    return errorCodeOf(error) === 'authn.factor_not_found'
  }

  function removeFactor(factorId: string): Promise<void> {
    const removed = factors?.find((factor) => factor.id === factorId)
    const removedLabel =
      removed === undefined
        ? ''
        : removed.label != null && removed.label !== ''
          ? removed.label
          : factorKindLabel(removed.kind ?? '')
    // Only an active factor rotates the credential; a name the list no longer
    // carries stays on the rotating side. A non-rotating removal runs bare so a
    // genuine anonymous answer still ends the session.
    const rotates = removed === undefined || removed.status === 'active'
    const lastActive = rotates && isLastActiveFactor(factorId)
    const kind = removed?.kind ?? ''
    removedLanding.current = {
      id: factorId,
      kind,
      siblings: siblingIdsOf(kind),
      control: removeButtons.current.get(factorId) ?? null,
      outcome: 'removed',
    }
    setPendingRemoveId(factorId)
    return feedback
      .run(async () => {
        try {
          const write = () => removeMutation.mutateAsync({ id: factorId })
          const settle = async (): Promise<string> => {
            setRemoveTarget(null)
            await invalidateFactors()
            return t(lastActive ? 'mfa.removedLast' : 'mfa.removed', {
              factor: removedLabel,
              interpolation: { escapeValue: false },
            })
          }
          if (rotates) {
            return await runIdentityTransition(session, write, settle)
          }
          await write()
          return await settle()
        } catch (error) {
          removedLanding.current = null
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
      .finally(() => setPendingRemoveId(null))
  }

  // The label is metadata, so no step-up gates this write.
  function renameFactor(factorId: string, label: string): Promise<void> {
    const kind =
      factors?.find((factor) => factor.id === factorId)?.kind ?? ''
    setPendingRenameId(factorId)
    return feedback
      .run(async () => {
        try {
          const updated = await updateMutation.mutateAsync({
            id: factorId,
            data: { label },
          })
          const updatedKind = updated.kind ?? kind
          renameLanding.current = {
            id: factorId,
            kind: updatedKind,
            siblings: siblingIdsOf(updatedKind),
            removed: false,
          }
          setRenameDraft(null)
          await invalidateFactors()
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
            renameLanding.current = {
              id: factorId,
              kind,
              siblings: siblingIdsOf(kind),
              removed: true,
            }
            setRenameDraft(null)
            await invalidateFactors()
          }
          throw error
        }
      })
      .finally(() => setPendingRenameId(null))
  }

  function cancelRename(): void {
    if (renameDraft === null) {
      return
    }
    const kind =
      factors?.find((factor) => factor.id === renameDraft.id)?.kind ?? ''
    renameLanding.current = {
      id: renameDraft.id,
      kind,
      siblings: siblingIdsOf(kind),
      removed: false,
    }
    setRenameDraft(null)
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

  // A plain block needs its own name, distinct from the page block above it.
  const sectionTitle =
    surface === 'plain' ? t('mfa.landmarkName') : t('mfa.title')

  return (
    <>
      <SectionCard title={sectionTitle}>
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
          const enabledFactorCount = settledFactors.filter(
            (factor) => factor.status === 'active',
          ).length
          const visibleFactors = settledFactors.filter(
            (factor) => factor.status !== 'pending',
          )
          const factorKinds = Array.from(
            new Set(visibleFactors.map((factor) => factor.kind ?? '')),
          )
          const allKinds = Array.from(
            new Set([...enrollEntries, ...factorKinds]),
          ).filter((kind) => kind !== '')
          // Fixed by strength, never by which group is enabled; unknown kinds
          // share the last rank in entry order.
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
              <Box
                ref={groupsContainer}
                role="group"
                aria-label={sectionTitle}
                tabIndex={-1}
                sx={{
                  display: 'flex',
                  flexDirection: 'column',
                  gap: 3,
                }}
              >
                {orderedKinds.length === 0 ? (
                  // Keeps the same focusable container when a removal empties the section.
                  <Typography
                    variant="body2"
                    color="text.secondary"
                    sx={{ py: 1 }}
                  >
                    {t('mfa.empty')}
                  </Typography>
                ) : (
                  orderedKinds.map((kind) => {
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
                    const replace =
                      kind === 'totp' &&
                      kindFactors.some((factor) => factor.status === 'active')
                    return (
                      <Box
                        component="section"
                        key={kind}
                        ref={groupRefFor(kind)}
                        aria-label={headingText}
                        tabIndex={-1}
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
                            <ActionButton
                              ref={addRefFor(kind)}
                              size="small"
                              unavailable={writeInFlight}
                              data-testid={`mfa-add-${kind}`}
                              // Contains the visible label (WCAG 2.5.3) while naming the method.
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
                            </ActionButton>
                          )}
                        </Box>
                        {open !== null ? (
                          <Box sx={{ maxWidth: FORM_MAX_WIDTH }}>
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
                                slotProps={{
                                  htmlInput: { readOnly: writeInFlight },
                                }}
                                fullWidth
                                sx={{ mt: 2 }}
                              />
                            ) : null}
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
                                    unavailable={writeInFlight}
                                    onClick={cancelEnroll}
                                  >
                                    {t('mfa.enroll.cancelLabel')}
                                  </ActionButton>
                                  <ActionButton
                                    type="button"
                                    emphasis="primary"
                                    busy={
                                      enrolling && startOrigin === 'retry'
                                    }
                                    unavailable={
                                      writeInFlight &&
                                      !(enrolling && startOrigin === 'retry')
                                    }
                                    onClick={() => {
                                      const token = ++panelTokenRef.current
                                      void startEnrollFlow(
                                        kind,
                                        token,
                                        open.label,
                                        'retry',
                                      )
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
                                submitDisabled={writeInFlight && !enrolling}
                                answerStep="enroll"
                                submitLabel={t('mfa.enroll.confirmLabel')}
                                onSubmit={(answer) => {
                                  void answerEnroll(open, answer)
                                }}
                                secondaryAction={
                                  <ActionButton
                                    type="button"
                                    unavailable={writeInFlight}
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
                                            <FactorRenameForm
                                              label={renameDraft.label}
                                              busy={
                                                pendingRenameId ===
                                                renameDraft.id
                                              }
                                              locked={writeInFlight}
                                              onLabelChange={(label) =>
                                                setRenameDraft({
                                                  id: renameDraft.id,
                                                  label,
                                                })
                                              }
                                              onCancel={cancelRename}
                                              onSave={() => {
                                                void renameFactor(
                                                  renameDraft.id,
                                                  renameDraft.label,
                                                )
                                              }}
                                            />
                                          )}
                                      </Box>
                                      {id !== null ? (
                                        <FactorActions
                                          canRename={
                                            !disabled && takesName(kind)
                                          }
                                          removeBusy={
                                            pendingRemoveId === id
                                          }
                                          unavailable={writeInFlight}
                                          renameRef={renameRefFor(id)}
                                          removeRef={removeRefFor(id)}
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
                  })
                )}
              </Box>

              <WriteFeedbackRegion outcome={feedback.outcome} />
            </Box>
          )
        }}
      </AccountAsyncSection>
      </SectionCard>

      <ConfirmDialog
        ref={confirmDialog}
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

      <StepUpChallengeFrame
        ref={stepUpDialog}
        open={gate !== null}
        reason={gate?.reason}
        session={session}
        onSuccess={handleStepUpSuccess}
        onCancel={() => {
          if (gate?.kind === 'enroll') {
            enrollLanding.current = { kind: gate.factorKind }
          } else if (gate?.kind === 'remove') {
            const removed = factors?.find(
              (factor) => factor.id === gate.factorId,
            )
            const kind = removed?.kind ?? ''
            removedLanding.current = {
              id: gate.factorId,
              kind,
              siblings: siblingIdsOf(kind),
              control: removeButtons.current.get(gate.factorId) ?? null,
              outcome: 'refused',
            }
          }
          setGate(null)
        }}
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
