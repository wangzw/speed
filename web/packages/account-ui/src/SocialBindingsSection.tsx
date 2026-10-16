/**
 * SocialBindingsSection: the social-account bindings surface of the
 * account page.
 *
 * Lists every external identity the authn module holds bound to the
 * signed-in account. The bound identities are the profile's `links`
 * (GET /auth/me), which carries each binding's own identifier -- there is
 * no separate list endpoint, so the section reads the same profile query
 * the preferences and sessions surfaces read. Each row names the provider
 * (mapped through bindings.provider.<provider> for the five social
 * providers the spec hosts and through the bindings.provider.oidc family
 * label for the enterprise-SSO channel, whose live provider values are
 * the dynamic per-tenant names "oidc:<tenant>" -- the raw value renders
 * as the row's reference line so one tenant's binding never reads as
 * another's; a value outside both sets, a future channel, renders the
 * generic "other" label, never a raw value) and the provider account's
 * email when the answer carries one. A row whose link carries an id has a
 * row-end "unlink" action that sits behind the ui-kit danger
 * ConfirmDialog: unbinding is irreversible (the binding is re-established
 * only by walking the provider's flow again), so the row asks once before
 * the DELETE goes out. After a successful unbind the profile query is
 * invalidated, so the rows converge on the server's answer; a refused
 * unbind renders its code text above the list -- authn.conflict
 * when the account would be left with no sign-in method (the server
 * decides by its own login-method count, never computable client-side),
 * anything else through the whitelist.
 *
 * The add area renders one button per configured provider that is not
 * already bound; clicking starts a `purpose: 'link'` flow on the session
 * (session.startAuthenticatedFlow) whose first step is a `redirect` step
 * -- the provider's authorization URL, a pure request reported upward
 * through onAuthorizeUrl, never a navigation the package performs -- and
 * the host completes the flow at its callback route with
 * BindingCallbackHandler. A refused start renders its code text above the
 * add area (the same InlineError banner the unbind refusal uses) and the
 * provider button re-arms for another attempt. One bind flow at a time:
 * while a channel's flow is being started every provider button is
 * disabled, so a second start -- any channel's -- cannot begin before the
 * first answers. When every configured provider is already bound the add
 * area does not render.
 *
 * The provider vocabulary is deliberately not imported from @speed/auth-ui
 * (same-layer packages never import each other): SocialProvider and
 * SocialProviderConfig are copied here, shaped identically to auth-ui's
 * own definitions, and must be kept in sync with them -- a social channel
 * is a deployment login-method name (a method-name string, not a closed
 * set; the WeChat pair arrives as `wechat.web` / `wechat.mp`), and the
 * same name lands in both packages' copies together.
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
 * The one exception is a first-run account with an unbound configured
 * provider: there a genuinely empty list is exactly the add area's cue,
 * so the header stays and the add area is the whole of the content.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  authnLoginUnlink,
  getAuthnProfileGetQueryKey,
} from '@speed/api-sdk'
import { useMutation } from '@tanstack/react-query'
import type { AuthSession } from '@speed/auth-core'
import {
  AsyncSection,
  ConfirmDialog,
  errorCodeOf,
  InlineSuccess,
  ListSkeleton,
} from '@speed/ui-kit'
import { InlineError } from './internal/inline-error.js'
import { useProfileQuery } from './internal/queries.js'
import { useAccountUiTranslation } from './internal/translation.js'

/**
 * A social sign-in method's name -- the account-ui copy of
 * @speed/auth-ui's SocialProvider, kept in sync with it (the same-layer
 * rule forbids importing the sign-in package). The set is the
 * deployment's: a social channel is a login-method name (the WeChat pair
 * arrives as `wechat.web` / `wechat.mp`), so this is a method name rather
 * than a closed union.
 */
export type SocialProvider = string

/**
 * One configured channel: which provider, the brand label to render it
 * with (empty for a name-keyed translation), and the redirect URI the
 * host's callback route listens on. The account-ui copy of auth-ui's
 * SocialProviderConfig, kept in sync with it.
 */
export interface SocialProviderConfig {
  readonly provider: SocialProvider
  /** The method's brand name (a provider's own name, not translated)
   * where the host supplies one. */
  readonly label?: string
  readonly redirectUri: string
}

export interface SocialBindingsSectionProps {
  /** The session that starts the `link` flow for the add area. */
  readonly session: AuthSession
  /** The channels the host offers for binding, in the order wanted. */
  readonly providers: readonly SocialProviderConfig[]
  /**
   * Receives the authorization URL from the started `link` flow's first
   * step; the host navigates. Omit to run the section without a follow-up
   * (a caller that only exercises the request path).
   */
  readonly onAuthorizeUrl?: (url: string) => void
}

/** The transient outcome of an unbind action, rendered above the list. */
type Notice =
  | { readonly kind: 'unbind-failed'; readonly code: string }
  | { readonly kind: 'unbind-done' }
  | null

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
 * The enterprise-SSO provider family prefix: go/authn stores each
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
 * A configured channel is a deployment login-method name, so a name the
 * family knows renders its label and a name outside the map renders the
 * generic other label.
 */
const PROVIDER_LABEL_KEYS: Readonly<Record<string, string>> = {
  google: 'bindings.provider.google',
  github: 'bindings.provider.github',
  wechat: 'bindings.provider.wechat',
  'wechat.web': 'bindings.provider.wechat',
  'wechat.mp': 'bindings.provider.wechat',
  dingtalk: 'bindings.provider.dingtalk',
  feishu: 'bindings.provider.feishu',
}

/**
 * A configured channel's display label: the method's brand name where the
 * host supplies one, otherwise the bundle's wording for a method the
 * family knows, otherwise the generic other label -- the same guarded
 * vocabulary the bound rows render provider values with, never a raw
 * bundle key.
 */
function configuredProviderLabel(
  config: SocialProviderConfig,
  t: (key: string) => string,
): string {
  if (config.label !== undefined && config.label !== '') {
    return config.label
  }
  return t(PROVIDER_LABEL_KEYS[config.provider] ?? 'bindings.provider.other')
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
  providers,
  onAuthorizeUrl,
}: SocialBindingsSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, refetch } = useProfileQuery()
  const unbindMutation = useMutation({
    mutationFn: (id: string) => authnLoginUnlink(id),
  })

  const [unbindTarget, setUnbindTarget] = useState<string | null>(null)
  const [busyProvider, setBusyProvider] = useState<SocialProvider | null>(null)
  const [notice, setNotice] = useState<Notice>(null)
  const [authorizeError, setAuthorizeError] = useState<string | null>(null)

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

  const boundProviders =
    rows === undefined
      ? new Set<string>()
      : new Set(
          rows
            .map((link) => link.provider)
            .filter((provider): provider is string => provider != null),
        )
  const available = providers.filter(
    (config) => !boundProviders.has(config.provider),
  )
  const hasAddArea = rows !== undefined && available.length > 0
  // Empty list with nothing to offer: the EmptyState's own empty placeholder
  // shows with the section header hidden. The placeholder is a status, not a
  // section, so it renders headerless (emptyHeadingLevel="none" below) and
  // does not enter the page outline.
  const showEmptyState = rows !== undefined && rows.length === 0 && !hasAddArea
  const showHeader =
    pending || (rows !== undefined && (rows.length > 0 || hasAddArea))

  async function handleUnbind(linkId: string): Promise<void> {
    setNotice(null)
    try {
      await unbindMutation.mutateAsync(linkId)
      setUnbindTarget(null)
      setNotice({ kind: 'unbind-done' })
      await queryClient.invalidateQueries({
        queryKey: getAuthnProfileGetQueryKey(),
      })
    } catch (error) {
      setUnbindTarget(null)
      setNotice({ kind: 'unbind-failed', code: errorCodeOf(error) })
    }
  }

  async function handleAuthorize(config: SocialProviderConfig): Promise<void> {
    // One bind flow at a time, across every provider: the busy slot is
    // provider-wide, so a second authorize click while a flow is being
    // started -- this channel's or any other's -- is refused outright
    // instead of racing the in-flight request.
    if (busyProvider !== null) {
      return
    }
    setAuthorizeError(null)
    setBusyProvider(config.provider)
    try {
      const state = await session.startAuthenticatedFlow({
        purpose: 'link',
        method: config.provider,
        input: { redirect_uri: config.redirectUri },
      })
      if ('next' in state && state.next.kind === 'redirect') {
        onAuthorizeUrl?.(state.next.url)
      }
    } catch (error) {
      setAuthorizeError(errorCodeOf(error))
    } finally {
      setBusyProvider(null)
    }
  }

  const unbindBusy = unbindTarget !== null && unbindMutation.isPending

  return (
    <Box>
      {showHeader && (
        <Box sx={{ mb: 2 }}>
          <Typography variant="h5" component="h2">
            {t('bindings.title')}
          </Typography>
        </Box>
      )}

      {/* The section header is hidden while the empty placeholder shows, but
          the empty placeholder is a status, not a section: a heading here
          ("No linked social accounts") would still enter the page outline as
          a chapter. It renders headerless as one role="status" region
          (emptyHeadingLevel="none") instead, and the page's heading order is
          left to its real headings. */}
      <AsyncSection
        pending={pending}
        payload={rows}
        empty={showEmptyState}
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
            {notice?.kind === 'unbind-failed' && <InlineError code={notice.code} />}
            {/* The landed unbind's confirmation, in a standing polite live
                region mounted empty until an unbind lands. */}
            <InlineSuccess
              message={notice?.kind === 'unbind-done' ? t('bindings.unlinked') : null}
            />
            {/* The binding rows are one real list, as the sessions rows
                are: a screen-reader user hears each binding as one item
                of a numbered set. The notice above and the add area
                below are page-level content and stay outside the list.
                role="list" keeps the list semantics under WebKit, which
                strips them from a list-style-none ul. */}
            <Box component="ul" role="list" sx={{ m: 0, p: 0, listStyle: 'none' }}>
              {settledRows.map((link, index) => {
                const id = link.id ?? null
                const provider = link.provider ?? null
                // Enterprise-SSO bindings arrive under the dynamic
                // per-tenant provider name "oidc:<tenant>": the family
                // label renders for the prefix, and the raw value stays
                // in the row as its reference line, so two SSO bindings
                // in two tenants never read as one identical row.
                const oidcProvider =
                  provider !== null &&
                  provider.startsWith(OIDC_PROVIDER_PREFIX)
                    ? provider
                    : null
                const providerLabel =
                  provider !== null && KNOWN_PROVIDERS.has(provider)
                    ? t(`bindings.provider.${provider}`)
                    : oidcProvider !== null
                      ? t('bindings.provider.oidc')
                      : t('bindings.provider.other')
                // The row identity the unbind action is named after (the
                // sessions twin names its revoke action after the row's
                // device label): the raw provider value when the label
                // is a shared family label, the label itself otherwise.
                const unbindIdentity = oidcProvider ?? providerLabel
                const email =
                  link.email != null && link.email !== '' ? link.email : null
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
                      sx={{
                        display: 'flex',
                        alignItems: 'center',
                        gap: 1.5,
                      }}
                    >
                      <Box sx={{ minWidth: 0 }}>
                        <Typography variant="body1" sx={{ fontWeight: 500 }}>
                          {providerLabel}
                        </Typography>
                        {oidcProvider !== null && (
                          <Typography
                            variant="body2"
                            color="text.secondary"
                            noWrap
                            title={oidcProvider}
                            sx={{ minWidth: 0 }}
                          >
                            {oidcProvider}
                          </Typography>
                        )}
                        {email !== null && (
                          <Typography
                            variant="body2"
                            color="text.secondary"
                            noWrap
                          >
                            {email}
                          </Typography>
                        )}
                      </Box>
                      {id !== null && (
                        <Box sx={{ marginLeft: 'auto', flexShrink: 0 }}>
                          <Button
                            variant="outlined"
                            color="error"
                            size="small"
                            disabled={unbindBusy}
                            aria-label={t('bindings.unbindAriaWithProvider', {
                              provider: unbindIdentity,
                              // The label is an aria-label, not HTML: the
                              // row identity (an oidc:<tenant> raw value
                              // included) must reach the accessibility
                              // tree verbatim (i18next's default value
                              // escaping would embed a literal `&#x2F;`).
                              interpolation: { escapeValue: false },
                            })}
                            onClick={() => setUnbindTarget(id)}
                          >
                            {t('bindings.unbind')}
                          </Button>
                        </Box>
                      )}
                    </Box>
                  </Box>
                )
              })}
            </Box>

            {hasAddArea && (
              <AddArea
                available={available}
                busy={busyProvider !== null}
                authorizeError={authorizeError}
                sectionLabel={t('bindings.addSectionTitle')}
                providerLabel={(config) => configuredProviderLabel(config, t)}
                onAuthorize={handleAuthorize}
              />
            )}
          </Box>
        )}
      </AsyncSection>

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
            void handleUnbind(unbindTarget)
          }
        }}
      />
    </Box>
  )
}

/** The add area: one button per unbound configured provider, plus the
 * authorize-path failure banner. Rendered only when the list has
 * loaded and at least one configured provider is unbound. While a bind
 * flow is being started every button is disabled together -- the busy
 * flag is provider-wide, never one channel's alone. */
function AddArea({
  available,
  busy,
  authorizeError,
  sectionLabel,
  providerLabel,
  onAuthorize,
}: {
  readonly available: readonly SocialProviderConfig[]
  /** True while any channel's link flow is being started. */
  readonly busy: boolean
  readonly authorizeError: string | null
  readonly sectionLabel: string
  readonly providerLabel: (config: SocialProviderConfig) => string
  readonly onAuthorize: (config: SocialProviderConfig) => void
}) {
  return (
    <Box sx={{ mt: 2, borderTop: '1px solid', borderColor: 'divider', pt: 2 }}>
      <Typography
        variant="body2"
        color="text.secondary"
        sx={{ mb: 1.5 }}
      >
        {sectionLabel}
      </Typography>
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'flex-start',
          gap: 1,
        }}
      >
        {available.map((config) => (
          <Button
            key={config.provider}
            variant="outlined"
            disabled={busy}
            onClick={() => onAuthorize(config)}
          >
            {providerLabel(config)}
          </Button>
        ))}
      </Box>
      <InlineError code={authorizeError} />
    </Box>
  )
}
