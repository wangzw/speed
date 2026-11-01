/**
 * SocialBindingsSection: the social-account bindings surface of the
 * account page.
 *
 * Lists every external identity the authn module holds bound to the
 * signed-in account. The bound identities are the profile's `links`
 * (GET /api/v1/auth/me), which carries each binding's own identifier -- there is
 * no separate list endpoint, so the section reads the same profile query
 * the preferences and sessions surfaces read. Each row resolves its
 * provider through the deployment's discovery document
 * (session.listMethods(), GET /api/v1/auth/methods): a stored provider
 * value belongs to the method whose descriptor declares it through
 * `binding_providers` -- the value equals an entry or continues one as a
 * namespace prefix, as "wechat" and "wechat-openid:" do for both WeChat
 * methods -- and a descriptor that declares none matches by its name. A
 * method's own `label` names the row, except that the package's
 * mode-keyed wording wins where a value is itself a sign-in mode's name,
 * keeping the modes distinguishable. A value no descriptor claims falls
 * back to the family's closed vocabulary (the five social names it has
 * wording for, the enterprise-SSO family prefix and the generic "other"
 * label), whose live oidc values are the dynamic per-tenant names
 * "oidc:<tenant>" -- the raw value renders as the row's reference line so
 * one tenant's binding never reads as another's. A row whose link carries
 * an id has a row-end
 * "unlink" action that sits behind the ui-kit danger ConfirmDialog:
 * unbinding is irreversible (the binding is re-established only by
 * walking the provider's flow again), so the row asks once before the
 * DELETE goes out. After a successful unbind the profile query is
 * invalidated, so the rows converge on the server's answer, and the
 * confirmation names the provider. The unlink replaces this session's
 * credential, so it runs inside an identity-transition window
 * (session.beginIdentityTransition): a read that lands with the outgoing
 * credential and answers anonymous is discarded instead of ending the
 * session, and the window closes once the refetch that follows the write
 * has settled. A refused unbind renders its code text
 * in the same region below the list -- authn.last_login_method when the account
 * would be left with no sign-in method (the server decides by its own
 * login-method count, never computable client-side), anything else through
 * the whitelist -- except a step-up shortfall. The route asks for a fresh
 * local multi-factor authentication, so a 401
 * authn.insufficient_user_authentication opens the shared step-up dialog
 * with the row remembered, and a won challenge re-runs that one unlink
 * exactly once. The failed attempt releases its identity-transition
 * window, and the re-run is a fresh write that opens its own. A caller
 * holding no factor gets the shared dialog's own answer for that state --
 * the missing-two-step-verification hint, the session's first-factor
 * re-proof, or the re-login exit -- exactly as the other gated surfaces
 * do. The re-login exit records the pending operation before the session
 * ends -- a bind's method or an unbind's row, both pure requests with no
 * credential to replay -- and the surface re-issues it once when it mounts
 * again on the same page after the new sign-in. A record naming another
 * page is left for the surface that owns it.
 *
 * The add area's channels come from the same discovery document: every
 * method whose kind is `redirect` and that is not already bound -- none
 * of its binding providers matches a stored link's provider value -- gets
 * one button, labelled with the package's mode-keyed wording for a brand's
 * several sign-in modes, otherwise the descriptor's own `label`,
 * defaulting to the method name. A deployment that configures no redirect
 * method -- or
 * whose discovery read failed -- offers no add area (fail closed): the
 * section never offers a channel the deployment cannot finish. Clicking a
 * button starts a `purpose: 'link'` flow on the session
 * (session.startAuthenticatedFlow) whose request carries the host's
 * `returnTo` as the flow's top-level `return_to`, and whose first step is
 * a `redirect` step -- the provider's authorization URL, a pure request
 * reported upward through onAuthorizeUrl, never a navigation the package
 * performs. The backend lands the browser on `return_to` once the
 * provider answers, and the host completes the flow there with
 * BindingCallbackHandler. `returnTo` thus moves the round trip's landing
 * point into the flow record itself; in a split-origin deployment it is
 * an absolute URL under an origin the deployment trusts (the server
 * accepts a path or such a URL). The rows' unlink actions and the add
 * area's flow starts report through the list's one write-feedback scope:
 * its one region below the rows holds at most one result, so a refused
 * start replaces an earlier unlink confirmation instead of standing beside
 * it, and the button re-arms for another attempt. Starting the flow does
 * not rotate this session's credential -- the callback handler completes
 * it -- so no identity-transition window is opened here. One bind flow at
 * a time: while a channel's flow is being started every provider button is
 * disabled, so a second start -- any channel's -- cannot begin before the
 * first answers. When every redirect method is already bound the add area
 * does not render.
 *
 * The section no longer takes a host-supplied channel list: a provider is
 * the deployment's login-method name, and the discovery document is the
 * one authority for which channels exist. The provider vocabulary is
 * deliberately not imported from @speed/auth-ui (same-layer packages
 * never import each other).
 *
 * An unresolved load -- the first load in flight, or parked by
 * react-query's default networkMode 'online' while the device is
 * offline -- keeps the section header and the loading skeleton; the
 * block must never silently vanish while the answer has not arrived.
 * Settled states render one ui-kit EmptyState with the section header
 * hidden: an answer listing zero bindings renders the empty variant as a
 * headerless status placeholder (emptyHeadingLevel="none", so it does not
 * enter the page outline), while a load that failed -- and a successful
 * answer that omits the optional links key, as unreadable as an error and
 * never grounds for a "no linked accounts" claim or an add area -- render
 * the error variant with a retry button.
 * The one exception is a first-run account with an unbound redirect
 * method: there a genuinely empty list is exactly the add area's cue, so
 * the header stays and the add area is the whole of the content.
 */

import { useCallback, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Chip from '@mui/material/Chip'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  authnLoginUnlink,
  getAuthnProfileGetQueryKey,
} from '@speed/api-sdk'
import { useMutation } from '@tanstack/react-query'
import type { AuthnMethodDescriptor, AuthSession } from '@speed/auth-core'
import { useStepUpGate } from '@speed/auth-core'
import {
  ConfirmDialog,
  ListSkeleton,
  SectionCard,
  useWriteFeedback,
} from '@speed/ui-kit'
import { AccountAsyncSection } from './internal/async-section.js'
import { withBindingReturn } from './internal/binding-return.js'
import { runIdentityTransition } from './internal/identity-transition.js'
import { useProfileQuery } from './internal/queries.js'
import {
  rememberStepUpResume,
  takeStepUpResume,
} from './internal/step-up-resume.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'

export interface SocialBindingsSectionProps {
  /** The session that reads the deployment's methods and starts the
   * `link` flow for the add area. */
  readonly session: AuthSession
  /**
   * The host's binding callback route, sent as the `link` flow's
   * top-level `return_to`. The backend lands the browser there once the
   * provider answers. A split-origin deployment passes an absolute URL
   * under an origin the deployment trusts.
   */
  readonly returnTo: string
  /**
   * Receives the authorization URL from the started `link` flow's first
   * step; the host navigates. Omit to run the section without a follow-up
   * (a caller that only exercises the request path).
   */
  readonly onAuthorizeUrl?: (url: string) => void
  /**
   * Optional brand icon for a channel, by the provider's method name.
   * Rendered beside the add-area button and the bound row's label.
   */
  readonly providerIcon?: (provider: string) => ReactNode
  /**
   * The host's current path. The link flow carries it inside `return_to` as
   * the page the round trip returns to, and the step-up flow sends it as
   * its own `return_to`. Only a site-internal relative path is carried;
   * anything else is dropped and the callback falls back to its safe
   * landing. Omitted, the deployment's default return target applies.
   */
  readonly returnPath?: string
}

/** The unlink a won step-up re-runs: the row's identifier and the provider
 * identity its confirmation names. */
interface UnbindAction {
  readonly id: string
  readonly label: string
}

/** What a won step-up replays in this section: the refusal it was answering
 * came either from an unlink or from starting a bind flow, and the replay has
 * to be the same call. */
type BindingAction =
  | ({ readonly kind: 'unbind' } & UnbindAction)
  | { readonly kind: 'authorize'; readonly method: AuthnMethodDescriptor }

/** The binding operations a re-login can resume. Both are pure requests with
 * no credential to replay, so a JSON-safe descriptor carries them across the
 * sign-in; an operation that would need a password or a one-time code could
 * not be recorded this way and is left for the person to redo. */
type ResumeOperation =
  | { readonly kind: 'authorize'; readonly method: string }
  | { readonly kind: 'unbind'; readonly id: string; readonly label: string }

/** The scope this surface's re-login resume record carries. */
const RESUME_SCOPE = 'social-bindings'

/** Whether a stored value is a binding operation this section can re-issue. */
function isResumeOperation(value: unknown): value is ResumeOperation {
  if (typeof value !== 'object' || value === null) {
    return false
  }
  const operation = value as {
    readonly kind?: unknown
    readonly method?: unknown
    readonly id?: unknown
    readonly label?: unknown
  }
  if (operation.kind === 'authorize') {
    return typeof operation.method === 'string'
  }
  if (operation.kind === 'unbind') {
    return (
      typeof operation.id === 'string' && typeof operation.label === 'string'
    )
  }
  return false
}

/**
 * The provider values the authn identity surface answers with, one
 * bundle key each under bindings.provider. A bound row calls t() only
 * for a value on this list -- anything else either carries the enterprise
 * OIDC_PROVIDER_PREFIX below (the live oidc: family, dynamic per
 * tenant, mapped by its prefix and never enumerable) or renders the
 * generic bindings.provider.other label.
 */
const KNOWN_PROVIDERS = new Set<string>([
  'google',
  'github',
  'wechat',
  'dingtalk',
  'feishu',
])

/**
 * The enterprise-SSO provider family prefix: pkg/authn stores each
 * tenant's enterprise connection binding under the per-tenant provider
 * name "oidc:<tenant>", so the value set is dynamic -- enumeration can
 * never suffice. A provider value carrying this prefix renders the
 * bindings.provider.oidc family label with the raw value as the row's
 * reference line (the label maps the family, the raw value tells one
 * tenant's binding from another's).
 */
const OIDC_PROVIDER_PREFIX = 'oidc:'

/**
 * The bundle keys of the social method names the family has wording for.
 * A bound provider value no discovery descriptor matches renders its
 * closed-set wording, and a name outside the map renders the generic
 * other label.
 */
const PROVIDER_LABEL_KEYS: Readonly<Record<string, string>> = {
  google: 'bindings.provider.google',
  github: 'bindings.provider.github',
  wechat: 'bindings.provider.wechat',
  dingtalk: 'bindings.provider.dingtalk',
  feishu: 'bindings.provider.feishu',
}

/**
 * The method names whose wording distinguishes one brand's sign-in modes.
 * Every mode of a brand carries the same descriptor label (both WeChat
 * methods say "WeChat"), so this wording outranks the descriptor label
 * and keeps the buttons and rows distinguishable.
 */
const METHOD_MODE_LABEL_KEYS: Readonly<Record<string, string>> = {
  'wechat.mp': 'bindings.provider.wechatOfficialAccount',
  'wechat.web': 'bindings.provider.wechatWeb',
}

/**
 * The provider values a method's stored bindings appear under: the
 * discovery descriptor's own `binding_providers`, each entry either an
 * exact value (as "google") or a namespace prefix the stored value
 * continues after (as "oidc:", "saml:" and "wechat-openid:"), and the
 * method's name for a descriptor that declares none -- the contract's
 * default for bindings that land under the method's own name.
 */
function methodBindingProviders(
  method: AuthnMethodDescriptor,
): readonly string[] {
  const declared = method.binding_providers
  return declared !== undefined && declared.length > 0
    ? declared
    : [method.name]
}

/**
 * Whether a stored provider value belongs to a method: it equals one of
 * the method's binding providers or continues one of them as a namespace
 * prefix. An empty entry is skipped, because as a prefix it would claim
 * every value.
 */
function methodOwnsProvider(
  method: AuthnMethodDescriptor,
  provider: string,
): boolean {
  return methodBindingProviders(method).some(
    (entry) =>
      entry !== '' && (provider === entry || provider.startsWith(entry)),
  )
}

/** A method's add-area display name: the mode-keyed wording for a brand
 * served through several modes, otherwise the descriptor's own label,
 * defaulting to the method name. */
function methodLabel(
  method: AuthnMethodDescriptor,
  t: (key: string) => string,
): string {
  const modeKey = METHOD_MODE_LABEL_KEYS[method.name]
  if (modeKey !== undefined) {
    return t(modeKey)
  }
  return method.label !== undefined && method.label !== ''
    ? method.label
    : method.name
}

/** The pending-state placeholder: rows shaped like the content rows,
 * read aloud as one loading announcement, never a fake heading. */
function BindingListSkeleton({ label }: { readonly label: string }) {
  const widths = [
    { primary: '38%', secondary: '52%' },
    { primary: '28%', secondary: '46%' },
    { primary: '34%', secondary: '58%' },
  ]
  return (
    <ListSkeleton
      label={label}
      rows={widths.map((width) => (
        <>
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
            }}
          >
            <Skeleton variant="text" width={width.primary} />
            <Skeleton variant="text" width="22%" />
          </Box>
          <Skeleton variant="text" width={width.secondary} />
        </>
      ))}
    />
  )
}

export function SocialBindingsSection({
  session,
  returnTo,
  onAuthorizeUrl,
  providerIcon,
  returnPath,
}: SocialBindingsSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, isFetching, error, refetch } = useProfileQuery()
  const unbindMutation = useMutation({
    mutationFn: (id: string) => authnLoginUnlink(id),
  })

  // The row whose unlink is being confirmed, with the provider identity its
  // confirmation names.
  const [unbindTarget, setUnbindTarget] = useState<{
    readonly id: string
    readonly label: string
  } | null>(null)
  const [busyProvider, setBusyProvider] = useState<string | null>(null)
  // One write scope for the whole list: the bound rows' unlink actions and
  // the add area's flow starts report through one slot, rendered in one
  // region below the rows, so two results never stand side by side and the
  // attempt that settles last is the one the region shows.
  const feedback = useWriteFeedback()
  // The unlink a won step-up re-runs, remembered through the shared gate so
  // the challenge replays exactly that row once and a cancel replays none.
  const gate = useStepUpGate<BindingAction>((action) => {
    if (action.kind === 'authorize') {
      void handleAuthorize(action.method)
      return
    }
    void handleUnbind(action.id, action.label)
  })
  // The deployment's methods, as read once per session. null is
  // unresolved (still reading, or the read failed): the add area is
  // withheld rather than guessed, and the bound rows fall back to the
  // family's closed vocabulary.
  const [methods, setMethods] = useState<
    readonly AuthnMethodDescriptor[] | null
  >(null)

  // Read the deployment's discovery document once per session (the same
  // seam the enroll wizard and the sign-in surface read). A failed read
  // offers no channel: a deployment whose methods cannot be read is not
  // assumed to carry any.
  useEffect(() => {
    let active = true
    setMethods(null)
    void (async () => {
      try {
        const list = await session.listMethods()
        if (active) {
          setMethods(list.methods)
        }
      } catch {
        if (active) {
          setMethods([])
        }
      }
    })()
    return () => {
      active = false
    }
  }, [session])

  // The re-login exit records the operation a new sign-in must re-issue. Both
  // binding operations are pure requests, so a JSON-safe descriptor carries
  // them; the gate hands the remembered action back before the dialog's
  // cancel clears it. The record is bound to the account the session snapshot
  // carries, so it never replays for an account a later sign-in lands on.
  const rememberRelogin = useCallback(() => {
    if (returnPath === undefined) {
      return
    }
    const action = gate.peek()
    if (action === null) {
      return
    }
    const operation: ResumeOperation =
      action.kind === 'authorize'
        ? { kind: 'authorize', method: action.method.name }
        : { kind: 'unbind', id: action.id, label: action.label }
    rememberStepUpResume(
      session.getSnapshot().principal?.account_id ?? '',
      RESUME_SCOPE,
      returnPath,
      operation,
    )
  }, [returnPath, gate.peek, session])

  // The page a re-login returns to re-issues the recorded operation once, as
  // soon as the deployment's methods have loaded so an authorize can resolve
  // the recorded method again. A record belonging to another page is put
  // back untouched for that page's surface.
  const resumed = useRef(false)
  useEffect(() => {
    if (resumed.current || methods === null || returnPath === undefined) {
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
    const operation = record.operation
    if (!isResumeOperation(operation)) {
      return
    }
    if (operation.kind === 'authorize') {
      const method = methods.find(
        (candidate) => candidate.name === operation.method,
      )
      if (method !== undefined) {
        void handleAuthorize(method)
      }
      return
    }
    void handleUnbind(operation.id, operation.label)
  }, [methods, returnPath, session])

  const links = data?.links
  // The answer is unresolved whenever there is no data yet: the first
  // load in flight, or parked by react-query's default networkMode
  // 'online' while the device is offline -- such a fetch sits at
  // fetchStatus 'paused', where isFetching (and isLoading, its
  // isPending-and-isFetching conjunction) is false and isError stays
  // false too, so a pending test derived from isLoading would miss
  // every branch and the block would silently vanish. isPending alone
  // tracks "no answer yet" across the in-flight and the parked states.
  const pending = isPending && data === undefined
  const rows = !pending && links !== undefined ? links : undefined

  /**
   * A binding's provider display label: the mode-keyed wording where the
   * stored value is itself a sign-in mode's name, otherwise the label of
   * the method that claims the value through its binding providers, else
   * the family's closed-set wording (a social name, the enterprise-SSO
   * family), else the generic other label -- never a raw bundle key.
   */
  function providerLabel(provider: string | null): string {
    if (provider === null) {
      return t('bindings.provider.other')
    }
    const modeKey = METHOD_MODE_LABEL_KEYS[provider]
    if (modeKey !== undefined) {
      return t(modeKey)
    }
    const descriptor = (methods ?? []).find((method) =>
      methodOwnsProvider(method, provider),
    )
    if (descriptor?.label !== undefined && descriptor.label !== '') {
      return descriptor.label
    }
    if (KNOWN_PROVIDERS.has(provider)) {
      return t(`bindings.provider.${provider}`)
    }
    if (provider.startsWith(OIDC_PROVIDER_PREFIX)) {
      return t('bindings.provider.oidc')
    }
    return t(PROVIDER_LABEL_KEYS[provider] ?? 'bindings.provider.other')
  }

  const boundProviders =
    rows === undefined
      ? []
      : rows
          .map((link) => link.provider)
          .filter((provider): provider is string => provider != null)
  // The add area offers only the deployment's redirect methods, minus the
  // ones already bound. A method counts as bound when any provider value
  // it declares is a stored link's -- so a WeChat identity stored as
  // "wechat" or "wechat-openid:<app>" withholds both WeChat methods. A
  // method whose kind is not redirect cannot start a `link` flow, and one
  // the deployment does not configure is absent from the document
  // entirely.
  const available = (methods ?? []).filter(
    (method) =>
      method.kind === 'redirect' &&
      !boundProviders.some((provider) =>
        methodOwnsProvider(method, provider),
      ),
  )
  const hasAddArea = rows !== undefined && available.length > 0
  // Empty list with nothing to offer: the EmptyState's own empty placeholder
  // shows with the section header hidden. The placeholder is a status, not a
  // section, so it renders headerless (emptyHeadingLevel="none" below) and
  // does not enter the page outline.
  const showEmptyState = rows !== undefined && rows.length === 0 && !hasAddArea
  const showHeader =
    pending || (rows !== undefined && (rows.length > 0 || hasAddArea))

  function handleUnbind(linkId: string, providerLabel: string): Promise<void> {
    return feedback.run(async () => {
      try {
        // The unlink replaces this session's credential as it commits, so
        // the window is open before the write and closes once the profile
        // refetch that follows it has settled: a read still carrying the
        // outgoing credential is discarded instead of ending the session.
        return await runIdentityTransition(
          session,
          () => unbindMutation.mutateAsync(linkId),
          async () => {
            setUnbindTarget(null)
            await queryClient.invalidateQueries({
              queryKey: getAuthnProfileGetQueryKey(),
            })
            // The confirmation names the provider that was unlinked.
            return t('bindings.unlinked', {
              provider: providerLabel,
              interpolation: { escapeValue: false },
            })
          },
        )
      } catch (error) {
        setUnbindTarget(null)
        // The route asks for a fresh local multi-factor authentication: that
        // shortfall opens the shared challenge with this row remembered
        // instead of settling as a refusal. The failed write released its own
        // window above; a won challenge's replay opens a fresh one.
        if (
          gate.intercept(error, {
            kind: 'unbind',
            id: linkId,
            label: providerLabel,
          })
        ) {
          return null
        }
        throw error
      }
    })
  }

  function handleAuthorize(method: AuthnMethodDescriptor): Promise<void> {
    // One bind flow at a time, across every provider: the busy slot is
    // provider-wide, so a second authorize click while a flow is being
    // started -- this channel's or any other's -- is refused outright
    // instead of racing the in-flight request.
    if (busyProvider !== null) {
      return Promise.resolve()
    }
    setBusyProvider(method.name)
    return feedback.run(async () => {
      try {
        const state = await session.startAuthenticatedFlow({
          purpose: 'link',
          method: method.name,
          return_to: withBindingReturn(returnTo, returnPath),
        })
        if ('next' in state && state.next.kind === 'redirect') {
          onAuthorizeUrl?.(state.next.url)
        }
      } catch (error) {
        // The route asks for the same fresh local multi-factor an unlink does:
        // a shortfall opens the shared challenge with this method remembered,
        // and a won challenge starts this one bind flow again.
        if (gate.intercept(error, { kind: 'authorize', method })) {
          return null
        }
        throw error
      } finally {
        setBusyProvider(null)
      }
      // Starting the flow is not a settled action: the host leaves for the
      // provider and the callback handler reports the completion.
      return null
    })
  }

  // Disabled through the whole attempt, including the profile refetch that
  // follows the mutation, so the row's unlink action cannot re-arm while the
  // scope is still pending -- the dialog closing is not the attempt settling.
  const unbindBusy = unbindMutation.isPending || feedback.pending

  return (
    <>
      <SectionCard title={showHeader ? t('bindings.title') : undefined}>
      {/* The section header is hidden while the empty placeholder shows, but
          the empty placeholder is a status, not a section: a heading here
          ("No linked social accounts") would still enter the page outline as
          a chapter. It renders headerless as one role="status" region
          (emptyHeadingLevel="none") instead, and the page's heading order is
          left to its real headings. */}
      <AccountAsyncSection
        pending={pending}
        payload={rows}
        empty={showEmptyState}
        error={error}
        refreshing={isFetching}
        loading={<BindingListSkeleton label={t('bindings.loading')} />}
        errorState={{
          title: t('bindings.error.title'),
          description: t('bindings.error.description'),
          retryLabel: t('bindings.retry'),
          onRetry: () => void refetch(),
        }}
        emptyState={{
          title: t('bindings.empty.title'),
          description: t('bindings.empty.description'),
        }}
        emptyHeadingLevel="none"
      >
        {(settledRows) => (
          <Box>
            {/* One list states every provider in the same grammar: the one
                already linked and the ones the deployment could link. A
                linked row carries its state, its reference and the unlink
                action; an unlinked row carries the action that starts the
                link flow. */}
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
              {settledRows.map((link, index) => {
                const id = link.id ?? null
                const provider = link.provider ?? null
                const oidcProvider =
                  provider !== null &&
                  provider.startsWith(OIDC_PROVIDER_PREFIX)
                    ? provider
                    : null
                const label = providerLabel(provider)
                const unbindIdentity = oidcProvider ?? label
                const email =
                  link.email != null && link.email !== '' ? link.email : null
                const icon =
                  provider !== null && providerIcon !== undefined
                    ? providerIcon(provider)
                    : null
                const reference = email ?? oidcProvider
                return (
                  <Box
                    component="li"
                    key={id ?? String(index)}
                    sx={{
                      display: 'grid',
                      gridTemplateColumns: 'minmax(0, 1fr) auto',
                      columnGap: 2,
                      alignItems: 'center',
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
                        {icon !== null && (
                          <Box
                            component="span"
                            sx={{ display: 'inline-flex', flexShrink: 0 }}
                          >
                            {icon}
                          </Box>
                        )}
                        <Typography variant="body1" sx={{ fontWeight: 600 }}>
                          {label}
                        </Typography>
                        <Chip
                          size="small"
                          variant="outlined"
                          color="success"
                          label={t('bindings.status.bound')}
                        />
                      </Box>
                      <Typography
                        variant="body2"
                        color="text.secondary"
                        noWrap
                        title={reference ?? undefined}
                        sx={{ mt: 0.5, minWidth: 0 }}
                      >
                        {reference ?? t('bindings.description')}
                      </Typography>
                    </Box>
                    {id !== null && (
                      <Button
                        variant="outlined"
                        color="error"
                        size="small"
                        disabled={unbindBusy}
                        aria-label={t('bindings.unbindAriaWithProvider', {
                          provider: unbindIdentity,
                          interpolation: { escapeValue: false },
                        })}
                        onClick={() =>
                          setUnbindTarget({ id, label: unbindIdentity })
                        }
                      >
                        {t('bindings.unbind')}
                      </Button>
                    )}
                  </Box>
                )
              })}
              {available.map((method) => {
                const label = methodLabel(method, t)
                const icon =
                  providerIcon !== undefined
                    ? providerIcon(method.name)
                    : null
                return (
                  <Box
                    component="li"
                    key={`method-${method.name}`}
                    sx={{
                      display: 'grid',
                      gridTemplateColumns: 'minmax(0, 1fr) auto',
                      columnGap: 2,
                      alignItems: 'center',
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
                        {icon !== null && (
                          <Box
                            component="span"
                            sx={{ display: 'inline-flex', flexShrink: 0 }}
                          >
                            {icon}
                          </Box>
                        )}
                        <Typography variant="body1" sx={{ fontWeight: 600 }}>
                          {label}
                        </Typography>
                        <Chip
                          size="small"
                          variant="outlined"
                          label={t('bindings.status.unbound')}
                        />
                      </Box>
                      <Typography
                        variant="body2"
                        color="text.secondary"
                        sx={{ mt: 0.5 }}
                      >
                        {t('bindings.description')}
                      </Typography>
                    </Box>
                    <Button
                      variant="outlined"
                      size="small"
                      disabled={busyProvider !== null}
                      data-testid={`bind-${method.name}`}
                      aria-label={t('bindings.bindAriaWithProvider', {
                        provider: label,
                        interpolation: { escapeValue: false },
                      })}
                      onClick={() => handleAuthorize(method)}
                    >
                      {t('bindings.bind')}
                    </Button>
                  </Box>
                )
              })}
            </Box>

          </Box>
        )}
      </AccountAsyncSection>
      {/* The list's one write scope: the unlink row actions and the add
          area's flow starts both report here, so their results share one
          slot and never stand side by side. The region sits below the rows
          and outside the settled content, so it survives the list becoming
          empty when the last binding goes. Its success names the provider,
          and a failure is one banner in the same place. */}
      <WriteFeedbackRegion outcome={feedback.outcome} sx={{ mt: 1.5 }} />
      </SectionCard>

      <ConfirmDialog
        open={unbindTarget !== null}
        title={t('bindings.confirmTitle')}
        message={t('bindings.confirmMessage')}
        variant="danger"
        confirmLabel={t('bindings.confirmLabel')}
        confirmLoading={unbindMutation.isPending}
        onCancel={() => setUnbindTarget(null)}
        onConfirm={() => {
          if (unbindTarget !== null) {
            void handleUnbind(unbindTarget.id, unbindTarget.label)
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

/** The add area: one button per unbound redirect method, plus the
 * authorize-path failure banner. Rendered only when the list has loaded
 * and at least one redirect method is unbound. The button name is the
 * mode-keyed wording for a brand's several sign-in modes, otherwise the
 * descriptor's own label, defaulting to the method name. While a bind
 * flow is being started every button is disabled together -- the busy
 * flag is provider-wide, never one channel's alone. */
