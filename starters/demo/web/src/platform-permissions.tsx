/**
 * Attaches the signed-in caller's own permission sets, so the shell can gate
 * the admin on them (src/app.tsx's AdminEntry and AdminAreaGate).
 *
 * The platform publishes the caller's held permissions at
 * GET /api/v1/authz/me/permissions: `platform` is the set the caller holds in
 * the platform domain and `tenant` the set it holds in the tenant the request
 * resolved to. auth-core carries the sets the host attaches and evaluates
 * nothing itself -- setPermissionSet is the seam, and usePermission is a pure
 * set lookup over what was attached. The read is the caller's own, so it needs
 * neither the declared-permission catalog nor any platform-staff permission to
 * answer.
 *
 * The read waits for the feature document to be `ready` and for the current
 * tenant to be settled: while the feature state is `pending` or `failed`, or
 * the host has not decided the current tenant yet, both sets stay unknown
 * (null), so the admin area shows its frameless wait or failure rather than
 * misreading the caller as a member. That gating is what keeps the admin
 * area's "feature state first, then permission set" order meaningful -- a
 * failed feature read never looks like an empty permission set.
 *
 * The tenant half is the request's own requirement, not a preference: the
 * route sits on the API endpoint, which the tenant resolver guards, and a
 * request carrying no tenant name is refused 404 before any handler runs.
 * Reading before the host settles the current tenant would therefore spend a
 * request on a known refusal -- a console error at every sign-in -- and attach
 * the fail-closed empty set to a caller whose sets were never actually asked
 * for. The tenant is a host fact settled outside the session (src/app.tsx's
 * tenant frame, or the admin area's own settle), and the read follows it.
 *
 * Once the document is ready the read is (re)issued whenever the effective
 * identity changes -- an authenticated transition, a re-login as another
 * principal, or a started or ended impersonation -- and whenever the current
 * tenant moves. A login clears the sets (auth-core's survival rule), and
 * restore() retains whatever was attached and cannot know the identity
 * changed, so re-reading on this edge is what keeps a stale set from outliving
 * the identity it was read under.
 *
 * The tenant is a second key, and it is the one that pairs a set with a
 * tenant: the tenant set is the set held in the tenant the request resolved
 * to. The host settles and switches the current tenant outside the session
 * (src/chrome.tsx's cell, recorded by the tenant area), so a read issued
 * before the tenant settles answers an empty tenant set, and a switch would
 * otherwise leave the old tenant's set attached for the frames before this
 * effect re-runs. Two mechanisms close that window. Every read captures the
 * tenant it was issued for and a sequence number, and an answer whose tenant
 * is no longer the current cell's -- or whose sequence a newer read has
 * superseded -- is discarded rather than attached, so a slow answer for a
 * tenant the viewer has left cannot land in the sets. And the tenant-change
 * entry (useTenantSelection.select, the one path every switch funnels through)
 * clears the tenant domain synchronously before it announces the new tenant,
 * so the first frame after a switch already reads the tenant domain as unknown
 * and fails closed instead of the previous tenant's permissions.
 *
 * A read also resets both sets to unknown immediately before it issues its
 * request; the reset belongs to the read, not to the identity-change action,
 * so a read that hangs or is refused does not leave the admin waiting on a set
 * the action cleared. A failed read is not an empty set: the sets stay
 * unknown, the read state reports the failure, and a surface can retry. Only
 * an answered document attaches sets -- an answer whose domains are both empty
 * is a known empty set, distinct from a failure.
 *
 * The read state is tagged with the tenant its sets belong to, so a consumer
 * renders the tenant set only when the tag still names the tenant it renders
 * for (useTenantPermissionSet is that seam). A frame can therefore never pair
 * the new tenant with the set read for the previous one.
 *
 * The read is deferred into a promise so a synchronous throw from the api-sdk
 * request seam (no request function bound) lands in the same catch as any other
 * failed read rather than escaping the effect.
 */

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import type { ReactElement, ReactNode } from 'react'
import { useAuthState, useFeatureState } from '@speed/auth-core'
import type { AuthPermissionSets, AuthSession } from '@speed/auth-core'
import { authzSelfPermissions } from './app-api/index.js'
import { getCurrentTenantId, useCurrentTenantId } from './chrome.js'

/**
 * The permission domains auth-core carries: 'system' is the tenant-independent
 * platform set and 'tenant' the set the principal holds in the tenant its
 * requests are resolved in.
 */
const PLATFORM_DOMAIN = 'system'
const TENANT_DOMAIN = 'tenant'

/** The read's outcome where a surface has to decide what to show: the sets it
 * attached, tagged with the tenant they belong to, whether a read is in
 * flight, and how to try again after a failure. */
export interface PermissionReadState {
  /** The tenant the last settled read answered for. A consumer renders
   * `perms` only when this still names the tenant it is rendering for: the
   * tenant domain set belongs to the tenant the request resolved to, and a
   * switched tenant must never read the previous tenant's set. */
  readonly tenantId: string | null
  /** The sets the last settled read attached, or null while unknown -- a read
   * is pending, the last read failed, or no read has been issued. The system
   * set is the tenant-independent platform set; the tenant set is the one
   * held in `tenantId`. */
  readonly perms: AuthPermissionSets | null
  /** A read is in flight for `tenantId`; `perms` is not yet its answer. */
  readonly pending: boolean
  /** The last read failed: the sets are unknown, not empty. */
  readonly failed: boolean
  /** Issues the read again; the surface retries by asking for it. */
  readonly retry: () => void
}

/** The part of the read state the read itself produces; `retry` belongs to
 * the provider's control surface, not to an attempt. */
interface PermissionOutcome {
  readonly tenantId: string | null
  readonly perms: AuthPermissionSets | null
  readonly pending: boolean
  readonly failed: boolean
}

const NO_READ_OUTCOME: PermissionOutcome = {
  tenantId: null,
  perms: null,
  pending: false,
  failed: false,
}

const NO_PERMISSION_READ: PermissionReadState = {
  ...NO_READ_OUTCOME,
  retry: () => undefined,
}

const PermissionReadContext = createContext<PermissionReadState>(
  NO_PERMISSION_READ,
)

/**
 * The read's outcome for a surface that must not read an unknown set as an
 * empty one: the tagged sets, whether a read is in flight, the last failure,
 * and the retry. A tree without the provider (a unit test mounting one
 * surface) reads unknown sets, no failure and a retry that does nothing.
 */
export function usePermissionRead(): PermissionReadState {
  return useContext(PermissionReadContext)
}

/**
 * The current tenant's permission set from the tagged read: null unless the
 * last settled read belongs to the current tenant. A surface that renders
 * tenant-scoped permissions reads this instead of the session snapshot, so a
 * frame after a switch can never pair the new tenant with the set read for the
 * previous one. The set is unknown (null) while a read is pending, when it
 * failed, and before the first read answers.
 */
export function useTenantPermissionSet(): readonly string[] | null {
  const read = usePermissionRead()
  const tenantId = useCurrentTenantId()
  if (read.tenantId !== tenantId || read.perms === null) {
    return null
  }
  return read.perms.tenant
}

/**
 * Mounts the attach for a session and renders its children unchanged: the
 * production assembly wraps the view in this so the caller's permission sets
 * are attached once a viewer is signed in.
 */
export function PlatformPermissions({
  session,
  children,
}: {
  /** The assembled session the sets are attached to (the one attachSession bound). */
  readonly session: AuthSession
  readonly children: ReactNode
}): ReactElement {
  // A retry re-runs the read: the attempt is an effect key. The outcome --
  // which sets landed, for which tenant, and whether an attempt failed -- is
  // published to the surfaces that must not read an unknown set as an empty
  // one.
  const [attempt, setAttempt] = useState(0)
  const [outcome, setOutcome] = useState<PermissionOutcome>(NO_READ_OUTCOME)
  usePlatformPermissions(session, attempt, setOutcome)
  const retry = useCallback(() => setAttempt((current) => current + 1), [])
  const value = useMemo<PermissionReadState>(
    () => ({
      tenantId: outcome.tenantId,
      perms: outcome.perms,
      pending: outcome.pending,
      failed: outcome.failed,
      retry,
    }),
    [outcome, retry],
  )
  return (
    <PermissionReadContext.Provider value={value}>
      {children}
    </PermissionReadContext.Provider>
  )
}

/**
 * Reads the caller's permission sets and attaches them to the session.
 *
 * The read is gated on the feature document being `ready` and the current
 * tenant being settled, not on any one feature: the permission read is scoped
 * to the caller, so it belongs to no optional capability. While the document
 * is `pending` or `failed`, or the current tenant is undecided, both sets stay
 * unknown (null) -- neither "absent" nor "present" -- and the admin area
 * shows its frameless wait/failure rather than misreading the operator as a
 * member. The guarded route answers 404 to a request without a tenant name, so
 * an undecided tenant is a request that would be refused before any handler;
 * it is not read. On every authenticated transition with a ready document the read
 * is (re)issued: a login clears the sets (auth-core's survival rule), so a
 * re-login as another principal replaces them rather than inheriting the
 * previous principal's. The current tenant is a second key: the tenant set is
 * the set held in the tenant the request resolved to, and the tenant settles
 * and changes outside the session, so a re-read follows it too. A re-read
 * first resets both sets to unknown, immediately before issuing its read.
 *
 * Each read is tagged with the tenant it was issued for and a sequence number.
 * An answer whose tenant is no longer the current cell's, or whose sequence a
 * newer read has superseded, is discarded: a slow answer for a tenant the
 * viewer has left cannot attach its set, and two reads for the same tenant
 * settle in issue order.
 *
 * A failed read is not an empty set. A transport or server failure leaves both
 * sets unknown and reports the failure, and the admin area shows its wait or
 * its retry instead of reading "no permissions" into a fault. Only an answered
 * document attaches a set -- an answer whose domains are both empty is a known
 * empty set, distinct from a failure.
 */
function usePlatformPermissions(
  session: AuthSession,
  attempt: number,
  publish: (outcome: PermissionOutcome) => void,
): void {
  const snapshot = useAuthState()
  const authenticated = snapshot.state === 'authenticated'
  // The identity is an input: a login attaches a set for a different
  // principal, and a started or ended impersonation swaps the effective
  // account without the feature state moving. restore() retains whatever
  // set was attached and cannot know the identity changed, so re-reading on
  // this edge is what keeps a stale (or, during an impersonation, refused)
  // set from outliving the identity it was read under.
  const principalId =
    snapshot.state === 'authenticated'
      ? (snapshot.principal?.account_id ?? '')
      : ''
  const impersonating = snapshot.impersonating
  // The tenant the host currently resolves requests to. It is not part of the
  // session snapshot -- the tenant area settles and switches it -- so it is
  // read from the host's own cell and joins the identity as a re-read key.
  const tenantId = useCurrentTenantId()
  const features = useFeatureState()
  const ready = features.state === 'ready'
  // The newest read this component issued. A read captures the value it bumps
  // this to at issue time; an answer whose captured value is no longer the
  // newest belongs to a superseded read and is discarded. The current tenant
  // is checked beside it, because a switch clears the tenant domain
  // synchronously and may not have re-run this effect yet when a slow answer
  // settles.
  const sequence = useRef(0)
  useEffect(() => {
    const issuedSequence = sequence.current + 1
    sequence.current = issuedSequence
    if (!authenticated || !ready || tenantId === null) {
      // Nothing is read: no answer belongs to this state. The tagged set is
      // unknown for the tenant the host holds, and a previous failure no
      // longer describes this state.
      publish({ tenantId, perms: null, pending: false, failed: false })
      return undefined
    }
    const issuedTenant = tenantId
    // The answer applies only while it is still the newest read and the tenant
    // it was issued for is still the current one.
    const applies = (): boolean =>
      sequence.current === issuedSequence &&
      getCurrentTenantId() === issuedTenant
    // Resetting to unknown belongs to this read, not to the identity-change
    // action that prompted it: the sets must go unknown immediately before a
    // new read is issued, so a read that hangs or is refused leaves the admin
    // waiting on an unknown set rather than a stale one.
    session.setPermissionSet(PLATFORM_DOMAIN, null)
    session.setPermissionSet(TENANT_DOMAIN, null)
    publish({
      tenantId: issuedTenant,
      perms: null,
      pending: true,
      failed: false,
    })
    void Promise.resolve()
      .then(() => authzSelfPermissions())
      .then((answer) => {
        if (!applies()) {
          return
        }
        session.setPermissionSet(PLATFORM_DOMAIN, answer.platform)
        session.setPermissionSet(TENANT_DOMAIN, answer.tenant)
        publish({
          tenantId: issuedTenant,
          perms: { system: answer.platform, tenant: answer.tenant },
          pending: false,
          failed: false,
        })
      })
      .catch(() => {
        if (!applies()) {
          return
        }
        // The sets were reset to unknown above and stay there: a failed read
        // says nothing about which permissions the caller holds.
        publish({
          tenantId: issuedTenant,
          perms: null,
          pending: false,
          failed: true,
        })
      })
    return () => {
      // A read that outlives this effect is stale even if it settles after a
      // newer effect has not run yet.
      sequence.current += 1
    }
  }, [
    authenticated,
    ready,
    session,
    principalId,
    impersonating,
    tenantId,
    attempt,
    publish,
  ])
}
