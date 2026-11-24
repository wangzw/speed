/**
 * SocialBindingsSection: the social-account bindings surface of the account
 * page, listing bound identities and the channels a deployment offers.
 */

import { useCallback, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
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
  ActionButton,
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

/**
 * The host supplies the session, the callback route and the navigation
 * action; the section reads the offered providers and starts or removes a
 * binding.
 */
export interface SocialBindingsSectionProps {
  /** The session that reads the deployment's methods and starts the
   * `link` flow for the add area. */
  readonly session: AuthSession
  /** The host's binding callback route, sent as the `link` flow's top-level
   * `return_to`. A split-origin deployment passes an absolute URL under a
   * trusted origin. */
  readonly returnTo: string
  /** Receives the authorization URL from the started `link` flow's first
   * step; the host navigates. */
  readonly onAuthorizeUrl?: (url: string) => void
  /** Optional brand icon for a channel, by the provider's method name. */
  readonly providerIcon?: (provider: string) => ReactNode
  /** The host's current path, carried in `return_to` and sent as the step-up
   * flow's `return_to`. Only a site-internal relative path is carried. */
  readonly returnPath?: string
}

interface UnbindAction {
  readonly id: string
  readonly label: string
}

type BindingAction =
  | ({ readonly kind: 'unbind' } & UnbindAction)
  | { readonly kind: 'authorize'; readonly method: AuthnMethodDescriptor }

/** Both operations a re-login can resume are pure requests, so a JSON-safe
 * descriptor carries them. */
type ResumeOperation =
  | { readonly kind: 'authorize'; readonly method: string }
  | { readonly kind: 'unbind'; readonly id: string; readonly label: string }

const RESUME_SCOPE = 'social-bindings'

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

/** The provider values with their own bundle key; anything else renders the
 * OIDC family label or the generic other label. */
const KNOWN_PROVIDERS = new Set<string>([
  'google',
  'github',
  'wechat',
  'dingtalk',
  'feishu',
])

/** pkg/authn stores each tenant's connection as "oidc:<tenant>", so the set
 * is dynamic; the raw value is the row's reference line. */
const OIDC_PROVIDER_PREFIX = 'oidc:'

const PROVIDER_LABEL_KEYS: Readonly<Record<string, string>> = {
  google: 'bindings.provider.google',
  github: 'bindings.provider.github',
  wechat: 'bindings.provider.wechat',
  dingtalk: 'bindings.provider.dingtalk',
  feishu: 'bindings.provider.feishu',
}

/** Every mode of a brand carries the same descriptor label, so this wording
 * outranks it and keeps the modes distinguishable. */
const METHOD_MODE_LABEL_KEYS: Readonly<Record<string, string>> = {
  'wechat.mp': 'bindings.provider.wechatOfficialAccount',
  'wechat.web': 'bindings.provider.wechatWeb',
}

/** A descriptor's own `binding_providers` (exact values or namespace
 * prefixes), or the method's name when it declares none. */
function methodBindingProviders(
  method: AuthnMethodDescriptor,
): readonly string[] {
  const declared = method.binding_providers
  return declared !== undefined && declared.length > 0
    ? declared
    : [method.name]
}

/** An empty entry is skipped, because as a prefix it would claim every value. */
function methodOwnsProvider(
  method: AuthnMethodDescriptor,
  provider: string,
): boolean {
  return methodBindingProviders(method).some(
    (entry) =>
      entry !== '' && (provider === entry || provider.startsWith(entry)),
  )
}

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

interface UnbindLanding {
  readonly id: string
  readonly order: readonly string[]
  readonly control: HTMLElement | null
}

/** Whether focus was lost when the row left. Focus still in a dialog counts
 * as lost: its exit would hand focus back to the removed control. */
function focusFellWith(removed: HTMLElement | null): boolean {
  const active = document.activeElement
  if (active === null || active === document.body) {
    return true
  }
  if (!(active instanceof HTMLElement) || !active.isConnected) {
    return true
  }
  if (removed !== null && removed.contains(active)) {
    return true
  }
  return active.closest('[role="dialog"]') !== null
}

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

/**
 * The social-bindings surface: starts a provider's link flow and removes
 * existing bindings, reporting each write's outcome beside its controls.
 */
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

  const [unbindTarget, setUnbindTarget] = useState<{
    readonly id: string
    readonly label: string
  } | null>(null)
  const [busyProvider, setBusyProvider] = useState<string | null>(null)
  // Only the row whose unlink is in flight reports busy; the rest of the
  // list's one write scope is unavailable without a reason.
  const [unbindRowId, setUnbindRowId] = useState<string | null>(null)
  const unbindButtons = useRef(new Map<string, HTMLButtonElement>())
  const unbindLanding = useRef<UnbindLanding | null>(null)
  // The focus landing when no neighbour action remains.
  const bindingsRegion = useRef<HTMLDivElement | null>(null)
  // One write scope for the whole list; the attempt that settles last wins.
  const feedback = useWriteFeedback()
  // The challenge replays exactly the remembered action once.
  const gate = useStepUpGate<BindingAction>((action) => {
    if (action.kind === 'authorize') {
      void handleAuthorize(action.method)
      return
    }
    void handleUnbind(action.id, action.label)
  })
  // null is unresolved (still reading, or the read failed): the add area is
  // withheld rather than guessed.
  const [methods, setMethods] = useState<
    readonly AuthnMethodDescriptor[] | null
  >(null)

  // A failed read offers no channel: it is never assumed to carry any.
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

  // The record is bound to the snapshot's account, so it never replays for a
  // different account a later sign-in lands on.
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

  // Waits for the methods so an authorize can resolve the recorded method; a
  // record for another page is put back untouched.
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
  // isPending alone tracks "no answer yet" across the in-flight and the
  // parked (offline) states, where isLoading is false.
  const pending = isPending && data === undefined
  const rows = !pending && links !== undefined ? links : undefined

  /** Never a raw bundle key: falls from the mode wording to the claiming
   * method's label, to the closed-set wording, to the generic other label. */
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
  // A method counts as bound when any provider value it declares is a stored
  // link's, so one WeChat identity withholds both WeChat methods.
  const available = (methods ?? []).filter(
    (method) =>
      method.kind === 'redirect' &&
      !boundProviders.some((provider) =>
        methodOwnsProvider(method, provider),
      ),
  )
  const hasAddArea = rows !== undefined && available.length > 0
  // Headerless: the placeholder is a status, not a section.
  const showEmptyState = rows !== undefined && rows.length === 0 && !hasAddArea
  const showHeader =
    pending || (rows !== undefined && (rows.length > 0 || hasAddArea))

  function handleUnbind(linkId: string, providerLabel: string): Promise<void> {
    // The order and control captured here name the focus neighbour.
    setUnbindRowId(linkId)
    unbindLanding.current = {
      id: linkId,
      order: (rows ?? [])
        .map((link) => link.id)
        .filter((id): id is string => id != null),
      control: unbindButtons.current.get(linkId) ?? null,
    }
    return feedback
      .run(async () => {
        try {
          return await runIdentityTransition(
            session,
            () => unbindMutation.mutateAsync(linkId),
            async () => {
              setUnbindTarget(null)
              await queryClient.invalidateQueries({
                queryKey: getAuthnProfileGetQueryKey(),
              })
              return t('bindings.unlinked', {
                provider: providerLabel,
                interpolation: { escapeValue: false },
              })
            },
          )
        } catch (error) {
          setUnbindTarget(null)
          // The row stays, so there is no removal to land from; a replay
          // captures its own landing.
          unbindLanding.current = null
          // A step-up shortfall opens the challenge with this row remembered.
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
      .finally(() => setUnbindRowId(null))
  }

  // A passive effect, not a layout one: the confirmation or challenge may be
  // closing in the same commit, and its focus trap refuses focus until its
  // own passive cleanup has run.
  useEffect(() => {
    const target = unbindLanding.current
    if (target === null) {
      return
    }
    const linkRows = rows
    if (linkRows === undefined) {
      return
    }
    const remaining = new Set(
      linkRows
        .map((link) => link.id)
        .filter((id): id is string => id != null),
    )
    if (remaining.has(target.id)) {
      // Settled without removing the row: there is nothing to land from.
      if (unbindRowId === null) {
        unbindLanding.current = null
      }
      return
    }
    unbindLanding.current = null
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
      const button = unbindButtons.current.get(id)
      if (button !== undefined && button.isConnected) {
        button.focus()
        return
      }
    }
    bindingsRegion.current?.focus()
  }, [rows, unbindRowId])

  function handleAuthorize(method: AuthnMethodDescriptor): Promise<void> {
    // One attempt at a time across every provider and row action.
    if (busyProvider !== null || unbindRowId !== null) {
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
        // A step-up shortfall opens the challenge with this method remembered.
        if (gate.intercept(error, { kind: 'authorize', method })) {
          return null
        }
        throw error
      } finally {
        setBusyProvider(null)
      }
      // Starting the flow is not a settled action; the callback reports it.
      return null
    })
  }

  const writeInFlight = unbindRowId !== null || busyProvider !== null

  return (
    <>
      <SectionCard title={showHeader ? t('bindings.title') : undefined}>
      <Box
        ref={bindingsRegion}
        tabIndex={-1}
        role="group"
        aria-label={t('bindings.title')}
      >
      {/* Headerless (emptyHeadingLevel="none"): the placeholder is a status,
          not a section, so it must not enter the page outline. */}
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
                      <ActionButton
                        ref={(element) => {
                          if (element === null) {
                            unbindButtons.current.delete(id)
                          } else {
                            unbindButtons.current.set(id, element)
                          }
                        }}
                        size="small"
                        tone="danger"
                        busy={unbindRowId === id}
                        unavailable={writeInFlight && unbindRowId !== id}
                        aria-label={t('bindings.unbindAriaWithProvider', {
                          provider: unbindIdentity,
                          interpolation: { escapeValue: false },
                        })}
                        onClick={() =>
                          setUnbindTarget({ id, label: unbindIdentity })
                        }
                      >
                        {t('bindings.unbind')}
                      </ActionButton>
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
                    <ActionButton
                      size="small"
                      busy={busyProvider === method.name}
                      unavailable={
                        writeInFlight && busyProvider !== method.name
                      }
                      data-testid={`bind-${method.name}`}
                      aria-label={t('bindings.bindAriaWithProvider', {
                        provider: label,
                        interpolation: { escapeValue: false },
                      })}
                      onClick={() => handleAuthorize(method)}
                    >
                      {t('bindings.bind')}
                    </ActionButton>
                  </Box>
                )
              })}
            </Box>

          </Box>
        )}
      </AccountAsyncSection>
      {/* Outside the settled content, so it survives the list becoming empty. */}
      <WriteFeedbackRegion outcome={feedback.outcome} sx={{ mt: 1.5 }} />
      </Box>
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

