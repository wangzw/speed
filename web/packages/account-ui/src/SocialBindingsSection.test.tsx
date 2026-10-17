/**
 * SocialBindingsSection behaviour: the bound identities read from the
 * profile's `links` (GET /auth/me), the unlink route (DELETE
 * /auth/links/{id}) and the add area's `purpose: 'link'` flow start
 * (POST /auth/flows/authenticated).
 *
 * What this suite pins: the links render with their provider label and
 * email and the add area offers only the configured providers that are
 * not already bound; an unlink DELETE-s the link and refetches the
 * profile; a refused unlink (the account's last sign-in method) renders
 * its code text above the list and leaves the row; and the add area's
 * click starts a link flow whose redirect step's URL is reported upward
 * (the section never navigates).
 */

import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  errorResponse,
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import {
  SocialBindingsSection,
  type SocialProviderConfig,
} from './SocialBindingsSection.js'

const PROFILE_PATH = '/auth/me'
const LINK_FLOW_PATH = '/auth/flows/authenticated'

const GITHUB_REDIRECT_URI = 'https://app.example.test/callback/social/github'
const WECHAT_REDIRECT_URI = 'https://app.example.test/callback/social/wechat'
const PROVIDERS: readonly SocialProviderConfig[] = [
  { provider: 'github', redirectUri: GITHUB_REDIRECT_URI },
  { provider: 'wechat', redirectUri: WECHAT_REDIRECT_URI },
]
const WECHAT_AUTHORIZE_URL = 'https://open.weixin.qq.com/connect/qrconnect?appid=x'

function profile(links: readonly Record<string, unknown>[]): Record<string, unknown> {
  return {
    account_id: 'user-1',
    kind: 'person',
    display_name: 'Ada',
    mfa_required: false,
    password_set: true,
    created_at: '2026-01-01T00:00:00Z',
    attributes: {},
    locale: '',
    time_zone: '',
    identifiers: [],
    links,
  }
}

describe('SocialBindingsSection', () => {
  it('renders the bound links and offers only the unbound providers', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profile([
            { id: 'github-1', provider: 'github', email: 'ada@example.test' },
          ]),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <SocialBindingsSection session={rig.session} providers={PROVIDERS} />,
    )

    expect(
      await screen.findByRole('heading', { name: zhCN.bindings.title }),
    ).toBeTruthy()
    expect(await screen.findByText(zhCN.bindings.provider.github)).toBeTruthy()
    expect(screen.getByText('ada@example.test')).toBeTruthy()
    // The bound provider is not offered again; the unbound one is.
    expect(
      screen.queryByRole('button', { name: zhCN.bindings.provider.github }),
    ).toBeNull()
    expect(
      screen.getByRole('button', { name: zhCN.bindings.provider.wechat }),
    ).toBeTruthy()

    await expectNoAxeViolations()
  })

  it('renders the empty placeholder headerless as a status region', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profile([]))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    // No configured channels and no bound links: the section's content is the
    // empty placeholder alone.
    renderWithProviders(
      <SocialBindingsSection session={rig.session} providers={[]} />,
    )

    const emptyTitle = await screen.findByText(zhCN.bindings.empty.title)
    // The empty placeholder is a status, not a section: its title must not
    // enter the page heading outline, and the placeholder is a polite status
    // region instead.
    expect(
      screen.queryByRole('heading', { name: zhCN.bindings.empty.title }),
    ).toBeNull()
    expect(emptyTitle.closest('[role="status"]')).not.toBeNull()
    await expectNoAxeViolations()
  })

  it('unlinks a binding and refetches the profile', async () => {
    let unlinked = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profile(
            unlinked
              ? []
              : [{ id: 'github-1', provider: 'github', email: 'ada@example.test' }],
          ),
        )
      }
      if (call.method === 'DELETE' && call.path === '/auth/links/github-1') {
        unlinked = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <SocialBindingsSection session={rig.session} providers={PROVIDERS} />,
    )
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.bindings.unbindAriaWithProvider.replace(
          '{{provider}}',
          zhCN.bindings.provider.github,
        ),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.bindings.confirmLabel,
      }),
    )

    await waitFor(() =>
      expect(screen.queryByText('ada@example.test')).toBeNull(),
    )
    // The unbound provider's row is gone; its channel is now offered
    // again in the add area (the bound-provider row's unlink action is
    // gone with it). The confirm dialog's exit transition must finish
    // before the page behind it is queryable by role again.
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    // The landed unbind confirms itself in the standing status region rather
    // than succeeding silently.
    expect(await screen.findByText(zhCN.bindings.unlinked)).toBeTruthy()
    expect(
      screen.queryByRole('button', {
        name: zhCN.bindings.unbindAriaWithProvider.replace(
          '{{provider}}',
          zhCN.bindings.provider.github,
        ),
      }),
    ).toBeNull()
    expect(
      screen.getByRole('button', { name: zhCN.bindings.provider.github }),
    ).toBeTruthy()
    expect(
      rig.calls.filter(
        (call) => call.method === 'GET' && call.path === PROFILE_PATH,
      ).length,
    ).toBeGreaterThanOrEqual(2)
  })

  it('renders a refused unlink (last sign-in method) as a code-text banner', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profile([{ id: 'github-1', provider: 'github' }]),
        )
      }
      if (call.method === 'DELETE' && call.path === '/auth/links/github-1') {
        return errorResponse(409, 'authn.conflict')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <SocialBindingsSection session={rig.session} providers={PROVIDERS} />,
    )
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.bindings.unbindAriaWithProvider.replace(
          '{{provider}}',
          zhCN.bindings.provider.github,
        ),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.bindings.confirmLabel,
      }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.conflict)
    // The refusal leaves the row in place.
    expect(screen.getByText(zhCN.bindings.provider.github)).toBeTruthy()
  })

  it('starts a link flow for an unbound provider and reports the redirect URL upward', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profile([]))
      }
      if (call.method === 'POST' && call.path === LINK_FLOW_PATH) {
        return jsonResponse(200, {
          seq: 1,
          state: 'first',
          next: {
            kind: 'redirect',
            url: WECHAT_AUTHORIZE_URL,
            method: 'GET',
          },
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const reports: string[] = []
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        providers={PROVIDERS}
        onAuthorizeUrl={(url) => reports.push(url)}
      />,
    )
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.bindings.provider.wechat,
      }),
    )

    await waitFor(() => expect(reports).toEqual([WECHAT_AUTHORIZE_URL]))
    const start = rig.calls.find(
      (call) => call.method === 'POST' && call.path === LINK_FLOW_PATH,
    )
    // The link flow starts with the purpose and the channel's redirect
    // URI; the section reads the URL off the redirect step, it does not
    // navigate.
    expect(start?.body).toEqual({
      purpose: 'link',
      method: 'wechat',
      input: { redirect_uri: WECHAT_REDIRECT_URI },
    })
  })

  it('renders a configured method by its brand label or bundle name, never a raw key', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profile([]))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    // A deployment's method names, not a closed set: the WeChat web channel
    // is `wechat.web`, a host may give a channel a brand label, and a
    // method the family has no wording for still must not leak a raw key.
    const providers: readonly SocialProviderConfig[] = [
      { provider: 'wechat.web', redirectUri: WECHAT_REDIRECT_URI },
      { provider: 'acme-sso', label: 'Acme SSO', redirectUri: WECHAT_REDIRECT_URI },
      { provider: 'mystery', redirectUri: WECHAT_REDIRECT_URI },
    ]
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <SocialBindingsSection session={rig.session} providers={providers} />,
    )

    // `wechat.web` resolves to the bundle's WeChat name, the host's brand
    // label renders verbatim, and the unknown method falls to the generic
    // other label -- never a raw bundle key.
    expect(
      await screen.findByRole('button', { name: zhCN.bindings.provider.wechat }),
    ).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Acme SSO' })).toBeTruthy()
    expect(
      screen.getByRole('button', { name: zhCN.bindings.provider.other }),
    ).toBeTruthy()
  })

  it('raises the unbind action to the 44px floor under a coarse pointer', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profile([
            { id: 'github-1', provider: 'github', email: 'ada@example.test' },
          ]),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <SocialBindingsSection session={rig.session} providers={PROVIDERS} />,
    )
    const unbind = await screen.findByRole('button', {
      name: zhCN.bindings.unbindAriaWithProvider.replace(
        '{{provider}}',
        zhCN.bindings.provider.github,
      ),
    })
    expect(hasCoarsePointerTouchTarget(unbind)).toBe(true)
  })
})
