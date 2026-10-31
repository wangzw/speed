/**
 * MfaSection: the two-step-verification (TOTP authenticator + passkey)
 * surface of the account page.
 *
 * The authn factor surface lists and removes factors (GET /api/v1/auth/factors,
 * DELETE /api/v1/auth/factors/{id}); registration is not among its routes -- the
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
 *  - Enroll. `startAuthenticatedFlow({purpose: 'enroll', input: {kind}})`
 *    opens the wizard; each reply's next step is rendered (FlowStep) and
 *    answered. A `webauthn` step hands the factor its own JSON -- TOTP's
 *    otpauth_uri, or a passkey's creation options -- of which the wizard
 *    renders the URI alone, as its own read-only field (it ships no QR or
 *    WebAuthn dependency) beside one free-text field. The flow settles
 *    `enrolled` without changing this session's level; the factors list is
 *    then refetched.
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
 * enrollment entries, which are where another one is registered. The
 * count comes from the same factors read; pending and disabled rows do not
 * count. The note is withheld when the deployment reports no enrollable
 * kind, because then there is no entry to point at.
 */

import { useEffect, useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Chip from '@mui/material/Chip'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnMfaFactorsQueryKey,
  useAuthnMfaRemoveFactor,
} from '@speed/api-sdk'
import type { AuthSession, AuthnStep, StepUpReason } from '@speed/auth-core'
import { stepUpReasonOf } from '@speed/auth-core'
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

/** The factor statuses the list answers with, one bundle key each under
 * mfa.factor.status. A value outside the list renders no chip. */
const KNOWN_FACTOR_STATUSES = new Set<string>(['active', 'pending', 'disabled'])


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

/** The open enrollment wizard: the step it is parked on and its sequence. */
interface EnrollWizard {
  readonly step: AuthnStep
  readonly seq: number
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

export function MfaSection({ session, returnPath }: MfaSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, isFetching, error, refetch } = useFactorsQuery()
  const removeMutation = useAuthnMfaRemoveFactor()

  // The action a won step-up re-runs, if any.
  const [gate, setGate] = useState<GatedAction | null>(null)
  // The open enrollment, if any.
  const [wizard, setWizard] = useState<EnrollWizard | null>(null)
  // The enrollment's in-flight state.
  const [enrolling, setEnrolling] = useState(false)
  // The row whose removal is being confirmed.
  const [removeTarget, setRemoveTarget] = useState<string | null>(null)
  // The kind of the enrollment in progress, so its confirmation names the
  // factor kind once the flow settles.
  const [enrollKind, setEnrollKind] = useState<string | null>(null)
  // Enrollment and removal are two control groups with their own scopes: the
  // enrollment result sits below its entries, the removal result below the
  // list, and one group's outcome never clears the other's.
  const enrollFeedback = useWriteFeedback()
  const removeFeedback = useWriteFeedback()
  // The deployment's enrollable factor kinds (GET /api/v1/auth/methods'
  // mfa.factors). null is unresolved -- still reading, or the read failed:
  // the enroll entries are then withheld rather than guessed.
  const [enrollableKinds, setEnrollableKinds] = useState<
    readonly string[] | null
  >(null)

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
    enrollFeedback.pending ||
    removeFeedback.pending

  /** The translated name of a factor kind, the guarded vocabulary's generic
   * label for a kind this package has no wording for. */
  function factorKindLabel(kind: string): string {
    return KNOWN_FACTOR_KINDS.has(kind)
      ? t(`mfa.factor.kind.${kind}`)
      : t('mfa.factor.kind.other')
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

  /** Start (or restart) an enrollment for one factor kind. */
  function startEnroll(factorKind: string): Promise<void> {
    setEnrollKind(factorKind)
    return enrollFeedback.run(async () => {
      setEnrolling(true)
      try {
        const state = await session.startAuthenticatedFlow({
          purpose: 'enroll',
          input: { kind: factorKind },
          ...(returnPath === undefined ? {} : { return_to: returnPath }),
        })
        if ('next' in state) {
          setWizard({ step: state.next, seq: state.seq })
          return null
        }
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
          setGate({ kind: 'enroll', factorKind, reason })
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
    const kind = enrollKind ?? ''
    return enrollFeedback.run(async () => {
      setEnrolling(true)
      try {
        const state = await session.answerFlow({
          seq: wizard.seq,
          step: answer.step,
          input: answer.input,
        })
        if ('next' in state) {
          setWizard({ step: state.next, seq: state.seq })
          return null
        }
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

  function handleStepUpSuccess(): void {
    const action = gate
    setGate(null)
    if (action?.kind === 'enroll') {
      void startEnroll(action.factorKind)
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
          // The note above the enrollment entries speaks for the enabled rows
          // alone: a pending or disabled factor is not a fallback the account
          // can use to sign in.
          const enabledFactorCount = settledFactors.filter(
            (factor) => factor.status === 'active',
          ).length
          const factorKinds = Array.from(
            new Set(settledFactors.map((factor) => factor.kind ?? '')),
          )
          const allKinds = Array.from(
            new Set([...enrollEntries, ...factorKinds]),
          ).filter((kind) => kind !== '')
          const isActive = (kind: string): boolean =>
            settledFactors.some(
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
          return wizard !== null ? (
            <Box sx={{ py: 1.5, maxWidth: FORM_MAX_WIDTH }}>
              <Typography variant="subtitle1" component="h3">
                {t('mfa.enroll.title')}
              </Typography>
              {/* The step's own confirm and the wizard's cancel share one
                  action row: cancel on the left, confirm closing the row on
                  the right. The step's fields are capped to the same width
                  as the pane's other forms. */}
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
                      setWizard(null)
                      enrollFeedback.reset()
                    }}
                  >
                    {t('mfa.enroll.cancelLabel')}
                  </Button>
                }
              />
              {/* The enrollment group's result, below the step and its
                  actions. */}
              <WriteFeedbackRegion outcome={enrollFeedback.outcome} />
            </Box>
          ) : (
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
              {/* Methods with an active factor first, then the ones still to
                  set up; each row is one method, and its action starts that
                  method's own enrollment flow. */}
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
                component="ul"
                role="list"
                sx={{
                  m: 0,
                  p: 0,
                  listStyle: 'none',
                  display: 'flex',
                  flexDirection: 'column',
                  gap: 2,
                }}
              >
                {orderedKinds.map((kind) => {
                  const kindFactors = settledFactors.filter(
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
                  return (
                    <Box
                      component="li"
                      key={kind}
                      sx={{
                        display: 'grid',
                        gridTemplateColumns: 'minmax(0, 1fr) auto',
                        columnGap: 2,
                        alignItems: 'start',
                      }}
                    >
                      <Box sx={{ minWidth: 0 }}>
                        <Box
                          sx={{
                            display: 'flex',
                            alignItems: 'center',
                            gap: 1,
                            flexWrap: 'wrap',
                          }}
                        >
                          <Typography variant="body1" sx={{ fontWeight: 600 }}>
                            {kindLabel}
                          </Typography>
                          <Chip
                            size="small"
                            variant="outlined"
                            color={activeCount > 0 ? 'success' : 'default'}
                            label={
                              activeCount > 0
                                ? t('mfa.state.on')
                                : t('mfa.state.off')
                            }
                          />
                        </Box>
                        {help === undefined ? null : (
                          <Typography
                            variant="body2"
                            color="text.secondary"
                            sx={{ mt: 0.5 }}
                          >
                            {t(help)}
                          </Typography>
                        )}
                      </Box>
                      {enrollable ? (
                        <Button
                          variant="outlined"
                          size="small"
                          disabled={busy}
                          data-testid={`mfa-add-${kind}`}
                          // The visible text is one action word for every
                          // method, and the accessible name begins with that
                          // same word followed by the method, so it contains
                          // the visible label (WCAG 2.5.3) while still telling
                          // several method buttons apart.
                          aria-label={
                            activeCount > 0
                              ? t('mfa.manageAria', {
                                  method: kindLabel,
                                  interpolation: { escapeValue: false },
                                })
                              : t('mfa.addAria', {
                                  method: kindLabel,
                                  interpolation: { escapeValue: false },
                                })
                          }
                          onClick={() => void startEnroll(kind)}
                        >
                          {activeCount > 0
                            ? t('mfa.action.manage')
                            : t('mfa.action.add')}
                        </Button>
                      ) : null}
                      {kindFactors.length > 0 && (
                        <Box
                          component="ul"
                          // The factor rows span the whole method block,
                          // so every Remove closes on the same right edge as
                          // the method's Add/Manage action above it: one
                          // rail, not one that shifts with the method's
                          // enrollability.
                          sx={{
                            gridColumn: '1 / -1',
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
                            const status =
                              factor.status != null &&
                              KNOWN_FACTOR_STATUSES.has(factor.status)
                                ? factor.status
                                : null
                            return (
                              <Box
                                component="li"
                                key={id ?? String(factorIndex)}
                                sx={{
                                  display: 'flex',
                                  alignItems: 'center',
                                  gap: 1.5,
                                  minWidth: 0,
                                }}
                              >
                                <Typography variant="body2">
                                  {factor.label != null &&
                                  factor.label !== ''
                                    ? factor.label
                                    : kindLabel}
                                </Typography>
                                {status !== null && (
                                  <Chip
                                    size="small"
                                    variant="outlined"
                                    label={t(`mfa.factor.status.${status}`)}
                                  />
                                )}
                                {id !== null && (
                                  <Button
                                    variant="outlined"
                                    color="error"
                                    size="small"
                                    disabled={busy}
                                    aria-label={t('mfa.removeAria', {
                                      factor: kindLabel,
                                      interpolation: { escapeValue: false },
                                    })}
                                    onClick={() => setRemoveTarget(id)}
                                    sx={{ marginLeft: 'auto' }}
                                  >
                                    {t('mfa.remove')}
                                  </Button>
                                )}
                              </Box>
                            )
                          })}
                        </Box>
                      )}
                    </Box>
                  )
                })}
              </Box>
              )}

              {/* The removal group's result, below the list its row actions
                  belong to. */}
              <WriteFeedbackRegion outcome={removeFeedback.outcome} />
              {enrollFeedback.outcome === null ? null : (
                <Box sx={{ mt: 2 }}>
                  <WriteFeedbackRegion outcome={enrollFeedback.outcome} />
                </Box>
              )}
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
