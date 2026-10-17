/**
 * MfaSection: the two-step-verification (TOTP authenticator + passkey)
 * surface of the account page.
 *
 * The authn factor surface lists and removes factors (GET /auth/factors,
 * DELETE /auth/factors/{id}); registration is not among its routes -- the
 * one entry for it is the login engine's `enroll` purpose. The section
 * therefore renders the factors it reads and offers the enrollment entries
 * THIS deployment carries, each of which opens a `purpose: 'enroll'` flow
 * (session.startAuthenticatedFlow) whose steps the wizard answers until the
 * flow settles `enrolled`. Which kinds the deployment carries is its own
 * answer (GET /auth/methods' `mfa.factors`, read once through
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
 *  - Remove. A row's row-end action sends DELETE /auth/factors/{id}
 *    behind the ui-kit danger ConfirmDialog (removing a factor is
 *    irreversible). The route asks for a fresh multi-factor
 *    authentication; an unelevated caller is answered 401
 *    authn.insufficient_user_authentication, which opens the step-up dialog and, on
 *    success, re-runs the removal exactly once. A 409 (the factor is the
 *    account's last one) renders its code text and the section stays put.
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
} from '@speed/ui-kit'
import { InlineError } from './internal/inline-error.js'
import { FlowStep } from './internal/flow-step.js'
import type { FlowStepAnswer } from './internal/flow-step.js'
import { useFactorsQuery } from './internal/queries.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountUiTranslation } from './internal/translation.js'

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

/** The enrollment entries this package can render, one bundle key each.
 * Each is offered only when the deployment reports its kind (see the
 * section header): the list is filtered against GET /auth/methods'
 * `mfa.factors`, so an entry the deployment cannot carry never renders. */
const ENROLL_ENTRIES = [
  { kind: 'totp', labelKey: 'mfa.enroll.totp' },
  { kind: 'webauthn', labelKey: 'mfa.enroll.webauthn' },
] as const

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
  // Every failure answer: code text above the current view.
  const [banner, setBanner] = useState<string | null>(null)
  // The deployment's enrollable factor kinds (GET /auth/methods'
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
  const busy = enrolling || removeMutation.isPending
  // Only the entries the deployment reported are offered.
  const enrollEntries =
    enrollableKinds === null
      ? []
      : ENROLL_ENTRIES.filter((entry) => enrollableKinds.includes(entry.kind))

  function invalidateFactors(): Promise<void> {
    return queryClient.invalidateQueries({
      queryKey: getAuthnMfaFactorsQueryKey(),
    })
  }

  /** Start (or restart) an enrollment for one factor kind. */
  async function startEnroll(factorKind: string): Promise<void> {
    setBanner(null)
    setEnrolling(true)
    try {
      const state = await session.startAuthenticatedFlow({
        purpose: 'enroll',
        input: { kind: factorKind },
      })
      if ('next' in state) {
        setWizard({ step: state.next, seq: state.seq })
        return
      }
      if (state.state === 'enrolled') {
        setWizard(null)
        await invalidateFactors()
        return
      }
      // A settled state other than 'enrolled' is not an enroll outcome.
      setBanner('client.unknown')
    } catch (error) {
      const code = errorCodeOf(error)
      if (code === 'authn.insufficient_user_authentication') {
        setGate({ kind: 'enroll', factorKind })
        return
      }
      setBanner(code)
    } finally {
      setEnrolling(false)
    }
  }

  /** Answer the open enrollment's current step. */
  async function answerEnroll(answer: FlowStepAnswer): Promise<void> {
    if (wizard === null) {
      return
    }
    setBanner(null)
    setEnrolling(true)
    try {
      const state = await session.answerFlow({
        seq: wizard.seq,
        step: answer.step,
        input: answer.input,
      })
      if ('next' in state) {
        setWizard({ step: state.next, seq: state.seq })
        return
      }
      if (state.state === 'enrolled') {
        setWizard(null)
        await invalidateFactors()
        return
      }
      // A settled state other than 'enrolled' is not an enroll outcome.
      setBanner('client.unknown')
      setWizard(null)
    } catch (error) {
      setBanner(errorCodeOf(error))
    } finally {
      setEnrolling(false)
    }
  }

  /** Remove one factor. */
  async function removeFactor(factorId: string): Promise<void> {
    setBanner(null)
    try {
      await removeMutation.mutateAsync({ id: factorId })
      setRemoveTarget(null)
      await invalidateFactors()
    } catch (error) {
      setRemoveTarget(null)
      const code = errorCodeOf(error)
      if (code === 'authn.insufficient_user_authentication') {
        setGate({ kind: 'remove', factorId })
        return
      }
      setBanner(code)
    }
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
    <Box>
      <Box sx={{ mb: 2 }}>
        <Typography variant="h5" component="h2">
          {t('mfa.title')}
        </Typography>
      </Box>

      <InlineError code={banner} />

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
                    setBanner(null)
                  }}
                >
                  {t('mfa.enroll.cancelLabel')}
                </Button>
              </Box>
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
                    {enrollEntries.map((entry) => (
                      <Button
                        key={entry.kind}
                        variant="outlined"
                        disabled={busy}
                        onClick={() => void startEnroll(entry.kind)}
                      >
                        {t(entry.labelKey)}
                      </Button>
                    ))}
                  </Box>
                </Box>
              )}
            </Box>
          )
        }
      </AsyncSection>

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
    </Box>
  )
}
