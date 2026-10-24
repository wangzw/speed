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
 *    factors of one kind can stand side by side. The route rotates the
 *    requesting session's cookie, so the removal runs inside an
 *    identity-transition window (session.beginIdentityTransition): a read
 *    that lands with the outgoing credential and answers anonymous is
 *    discarded instead of ending the session, and the window closes once
 *    the refetch that follows the write has settled.
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
 */

import { useEffect, useState } from 'react'
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
import type { AuthSession, AuthnStep } from '@speed/auth-core'
import {
  AsyncSection,
  ConfirmDialog,
  errorCodeOf,
  ListSkeleton,
  SectionCard,
  useWriteFeedback,
} from '@speed/ui-kit'
import { FlowStep } from './internal/flow-step.js'
import type { FlowStepAnswer } from './internal/flow-step.js'
import { useFactorsQuery } from './internal/queries.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'

export interface MfaSectionProps {
  /** The session that drives the enroll flow and the step-up dialog. */
  readonly session: AuthSession
}

/** The factor kinds the authn factor surface records (pkg/authn's
 * FactorKind), one bundle key each under mfa.factor.kind. A value outside
 * the list renders the generic "other" label, never a raw value. */
const KNOWN_FACTOR_KINDS = new Set<string>(['totp', 'webauthn', 'sms'])

/** The factor statuses the list answers with, one bundle key each under
 * mfa.factor.status. A value outside the list renders no chip. */
const KNOWN_FACTOR_STATUSES = new Set<string>(['active', 'pending', 'disabled'])

/** The enrollable factor kinds this package has wording for, one bundle
 * key each. The entries themselves are the deployment's own answer (see
 * the section header): every kind GET /api/v1/auth/methods' `mfa.factors`
 * lists gets one entry, a known kind renders its translated name and an
 * unknown kind renders the generic other label while still starting that
 * kind's flow. */
const ENROLL_LABEL_KEYS: Readonly<Record<string, string>> = {
  totp: 'mfa.enroll.totp',
  webauthn: 'mfa.enroll.webauthn',
  sms: 'mfa.enroll.sms',
}

/** The action a won step-up re-runs. */
type GatedAction =
  | { readonly kind: 'enroll'; readonly factorKind: string }
  | { readonly kind: 'remove'; readonly factorId: string }

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

export function MfaSection({ session }: MfaSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, refetch } = useFactorsQuery()
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
        if (code === 'authn.insufficient_user_authentication') {
          setGate({ kind: 'enroll', factorKind })
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
    return removeFeedback.run(async () => {
      // The route rotates this session's cookie, so a read that left under
      // the outgoing cookie can answer 401 and the client's silent restore
      // would read that answer as the end of the session. The window is open
      // across the write and the refetch that follows it: an anonymous
      // answer read under the replaced credential is discarded, while the
      // request that produced it keeps its own 401. The release is
      // idempotent, and a step-up re-run opens its own window.
      const releaseIdentityTransition = session.beginIdentityTransition()
      try {
        try {
          await removeMutation.mutateAsync({ id: factorId })
        } catch (error) {
          setRemoveTarget(null)
          const code = errorCodeOf(error)
          if (code === 'authn.insufficient_user_authentication') {
            setGate({ kind: 'remove', factorId })
            return null
          }
          throw error
        }
        setRemoveTarget(null)
        await invalidateFactors()
        // The confirmation names the factor row removed.
        return t('mfa.removed', {
          factor: removedLabel,
          interpolation: { escapeValue: false },
        })
      } finally {
        releaseIdentityTransition()
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
        <AsyncSection
        pending={pending}
        payload={factors}
        empty={false}
        loading={<FactorListSkeleton label={t('mfa.loading')} />}
        errorState={{
          title: t('mfa.error.title'),
          description: t('mfa.error.description'),
          retryLabel: t('mfa.retry'),
          onRetry: () => void refetch(),
        }}
      >
        {(settledFactors) =>
          wizard !== null ? (
            <Box sx={{ py: 1.5 }}>
              <Typography variant="h6" component="h3">
                {t('mfa.enroll.title')}
              </Typography>
              <FlowStep
                key={`${wizard.seq}-${wizard.step.kind}`}
                step={wizard.step}
                busy={enrolling}
                answerStep="enroll"
                submitLabel={t('mfa.enroll.confirmLabel')}
                onSubmit={(answer) => void answerEnroll(answer)}
              />
              <Box sx={{ mt: 1.5 }}>
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
              </Box>
              {/* The enrollment group's result, below the step and its
                  actions. */}
              <WriteFeedbackRegion outcome={enrollFeedback.outcome} />
            </Box>
          ) : (
            <Box>
              <Box
                component="ul"
                role="list"
                sx={{ m: 0, p: 0, listStyle: 'none' }}
              >
                {settledFactors.map((factor, index) => {
                  const id = factor.id ?? null
                  const kind = factor.kind ?? ''
                  const kindLabel = KNOWN_FACTOR_KINDS.has(kind)
                    ? t(`mfa.factor.kind.${kind}`)
                    : t('mfa.factor.kind.other')
                  const status =
                    factor.status != null &&
                    KNOWN_FACTOR_STATUSES.has(factor.status)
                      ? factor.status
                      : null
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
                          <Typography variant="body1" sx={{ fontWeight: 500 }}>
                            {factor.label != null && factor.label !== ''
                              ? factor.label
                              : kindLabel}
                          </Typography>
                          <Typography variant="body2" color="text.secondary">
                            {kindLabel}
                          </Typography>
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
                            >
                              {t('mfa.remove')}
                            </Button>
                          )}
                        </Box>
                      </Box>
                    </Box>
                  )
                })}
              </Box>

              {settledFactors.length === 0 && (
                <Typography variant="body2" color="text.secondary" sx={{ py: 1 }}>
                  {t('mfa.empty')}
                </Typography>
              )}

              {/* The removal group's result, below the list its row actions
                  belong to. */}
              <WriteFeedbackRegion outcome={removeFeedback.outcome} />

              {enrollEntries.length > 0 && (
                <Box
                  sx={{
                    mt: 2,
                    borderTop: '1px solid',
                    borderColor: 'divider',
                    pt: 2,
                  }}
                >
                  <Typography
                    variant="body2"
                    color="text.secondary"
                    sx={{ mb: 1.5 }}
                  >
                    {t('mfa.enroll.sectionTitle')}
                  </Typography>
                  <Box
                    sx={{
                      display: 'flex',
                      flexDirection: 'column',
                      alignItems: 'flex-start',
                      gap: 1,
                    }}
                  >
                    {enrollEntries.map((kind) => (
                      <Button
                        key={kind}
                        variant="outlined"
                        disabled={busy}
                        onClick={() => void startEnroll(kind)}
                      >
                        {t(ENROLL_LABEL_KEYS[kind] ?? 'mfa.enroll.other')}
                      </Button>
                    ))}
                  </Box>
                  {/* The enrollment group's result, below the entries that
                      started it. */}
                  <WriteFeedbackRegion outcome={enrollFeedback.outcome} />
                </Box>
              )}
            </Box>
          )
        }
      </AsyncSection>
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
        session={session}
        onSuccess={handleStepUpSuccess}
        onCancel={() => setGate(null)}
      />
    </>
  )
}
