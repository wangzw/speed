/**
 * SocialBindingsSection behaviour: the bound identities read from the
 * profile's `links` (GET /api/v1/auth/me), the unlink route (DELETE
 * /api/v1/auth/links/{id}), the deployment's discovery document
 * (GET /api/v1/auth/methods) and the add area's `purpose: 'link'` flow
 * start (POST /api/v1/auth/flows/authenticated).
 *
 * What this suite pins: the links render with the provider label of the
 * discovery descriptor that claims the value through its
 * `binding_providers` (an exact entry or a namespace prefix; a descriptor
 * declaring none matches by name), falling back to the family's closed
 * vocabulary for a value no descriptor claims -- and the add area offers
 * only the deployment's unbound redirect methods, labelled by their
 * descriptors; an unlink DELETE-s the link and refetches the profile; a
 * refused unlink (the account's last sign-in method) renders its code
 * text above the list and leaves the row; and the add area's click starts
 * a link flow carrying the host's `return_to` at the top level, whose
 * redirect step's URL is reported upward (the section never navigates).
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
import { SocialBindingsSection } from './SocialBindingsSection.js'

const PROFILE_PATH = '/api/v1/auth/me'
const METHODS_PATH = '/api/v1/auth/methods'
const LINK_FLOW_PATH = '/api/v1/auth/flows/authenticated'
const ACCOUNT_CALLBACK = '/account/callback'

/** The deployment's discovery document, carrying redirect methods. */
function methodsDoc(
  methods: readonly {
    name: string
    label?: string
    bindingProviders?: readonly string[]
  }[],
): Record<string, unknown> {
  return {
    methods: methods.map((method) => ({
      name: method.name,
      kind: 'redirect',
      ...(method.label === undefined ? {} : { label: method.label }),
      ...(method.bindingProviders === undefined
        ? {}
        : { binding_providers: [...method.bindingProviders] }),
    })),
    signup: { enabled: false, password: { enabled: false } },
    mfa: { factors: [] },
  }
}

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

const WECHAT_AUTHORIZE_URL = 'https://open.weixin.qq.com/connect/qrconnect?appid=x'

describe('SocialBindingsSection', () => {
  it('renders the bound links and offers only the unbound redirect methods', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profile([
            { id: 'github-1', provider: 'github', email: 'ada@example.test' },
          ]),
        )
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(
          200,
          methodsDoc([
            // The bound provider resolves through the descriptor's
            // binding_providers, not through a matching method name: the
            // method is called "github-org", and a section that matched by
            // name alone would render the closed vocabulary's "GitHub" on
            // the row and offer the channel again in the add area.
            {
              name: 'github-org',
              label: 'GitHub Org',
              bindingProviders: ['github'],
            },
            { name: 'wechat', label: 'WeChat' },
          ]),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
      />,
    )

    expect(
      await screen.findByRole('heading', { name: zhCN.bindings.title }),
    ).toBeTruthy()
    // The bound row's provider label comes from the descriptor, not from
    // the closed vocabulary ("GitHub"): a row rendering the vocabulary's
    // word here would be indistinguishable from one that never resolved
    // the descriptor at all.
    expect(await screen.findByText('GitHub Org')).toBeTruthy()
    expect(screen.getByText('ada@example.test')).toBeTruthy()
    expect(
      screen.queryByRole('button', { name: 'GitHub Org' }),
    ).toBeNull()
    expect(
      await screen.findByRole('button', { name: 'WeChat' }),
    ).toBeTruthy()

    await expectNoAxeViolations()
  })

  it('attributes a stored open-id value to the method through its namespace prefix', async () => {
    // A WeChat identity without a union id is stored under
    // "wechat-openid:<app id>". The method declares that namespace as a
    // binding-provider prefix, so the value resolves to it without the
    // client knowing the storage naming.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profile([{ id: 'wechat-1', provider: 'wechat-openid:wx-app-1' }]),
        )
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(
          200,
          methodsDoc([
            {
              name: 'wechat.web',
              label: 'WeChat',
              bindingProviders: ['wechat', 'wechat-openid:'],
            },
          ]),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
      />,
    )

    // The descriptor's own label names the row; the stored value is not a
    // sign-in mode's name, so no mode-keyed wording applies.
    expect(await screen.findByText('WeChat')).toBeTruthy()
    // The method is already bound through the prefix, so the add area does
    // not offer it again (the mode-keyed label names the button it would
    // have carried).
    expect(
      screen.queryByRole('button', {
        name: zhCN.bindings.provider.wechatWeb,
      }),
    ).toBeNull()
    await expectNoAxeViolations()
  })

  it('treats a declared binding-provider list as authoritative and a missing one as the method name', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profile([
            { id: 'gh-1', provider: 'github' },
            { id: 'gl-1', provider: 'gitlab' },
          ]),
        )
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(
          200,
          methodsDoc([
            // Declaring a namespace replaces the name default: a
            // "github" value is not this method's binding even though the
            // method is called "github".
            {
              name: 'github',
              label: 'GitHub Enterprise',
              bindingProviders: ['gh:'],
            },
            // No declaration: the method's name is its binding provider.
            { name: 'gitlab', label: 'GitLab' },
          ]),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
      />,
    )

    // The uncovered "github" value falls back to the closed vocabulary.
    expect(
      await screen.findByText(zhCN.bindings.provider.github),
    ).toBeTruthy()
    // The undeclared method claims its own name, so its row carries the
    // descriptor label and its channel is withheld.
    expect(screen.getByText('GitLab')).toBeTruthy()
    expect(
      screen.getByRole('button', { name: 'GitHub Enterprise' }),
    ).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'GitLab' })).toBeNull()
    await expectNoAxeViolations()
  })

  it('gives two sign-in modes that share a descriptor label distinct accessible names', async () => {
    // Both WeChat methods describe themselves as "WeChat". The package's
    // mode-keyed wording must outrank the descriptor label, or the two
    // buttons would be announced identically.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profile([]))
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(
          200,
          methodsDoc([
            {
              name: 'wechat.web',
              label: 'WeChat',
              bindingProviders: ['wechat', 'wechat-openid:'],
            },
            {
              name: 'wechat.mp',
              label: 'WeChat',
              bindingProviders: ['wechat', 'wechat-openid:'],
            },
          ]),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
      />,
    )

    const web = await screen.findByRole('button', {
      name: zhCN.bindings.provider.wechatWeb,
    })
    const officialAccount = screen.getByRole('button', {
      name: zhCN.bindings.provider.wechatOfficialAccount,
    })
    // Two buttons, two distinct names: neither is reachable by the shared
    // descriptor label alone.
    expect(web).not.toBe(officialAccount)
    expect(screen.queryAllByRole('button', { name: 'WeChat' })).toHaveLength(0)
  })

  it('renders the empty placeholder headerless when the deployment offers no channel', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profile([]))
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc([]))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    // No redirect method and no bound links: the section's content is the
    // empty placeholder alone.
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
      />,
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
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(
          200,
          methodsDoc([{ name: 'github', label: 'GitHub' }]),
        )
      }
      if (call.method === 'DELETE' && call.path === '/api/v1/auth/links/github-1') {
        unlinked = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
      />,
    )
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.bindings.unbindAriaWithProvider.replace('{{provider}}', 'GitHub'),
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
        name: zhCN.bindings.unbindAriaWithProvider.replace('{{provider}}', 'GitHub'),
      }),
    ).toBeNull()
    expect(
      screen.getByRole('button', { name: 'GitHub' }),
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
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc([]))
      }
      if (call.method === 'DELETE' && call.path === '/api/v1/auth/links/github-1') {
        return errorResponse(409, 'authn.conflict')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
      />,
    )
    const user = userEvent.setup()

    // No descriptor matches "github", so the row falls back to the
    // family's closed-set wording.
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

  it('starts a link flow with the top-level return_to and reports the redirect URL upward', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profile([]))
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(
          200,
          methodsDoc([{ name: 'wechat', label: 'WeChat' }]),
        )
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
        returnTo={ACCOUNT_CALLBACK}
        onAuthorizeUrl={(url) => reports.push(url)}
      />,
    )
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: 'WeChat',
      }),
    )

    await waitFor(() => expect(reports).toEqual([WECHAT_AUTHORIZE_URL]))
    const start = rig.calls.find(
      (call) => call.method === 'POST' && call.path === LINK_FLOW_PATH,
    )
    // The link flow starts with the purpose, the method name and the
    // host's callback route as the top-level return_to; the section reads
    // the URL off the redirect step, it does not navigate.
    expect(start?.body).toEqual({
      purpose: 'link',
      method: 'wechat',
      return_to: ACCOUNT_CALLBACK,
    })
  })

  it('falls back to the closed provider vocabulary when no descriptor matches', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profile([
            { id: 'wechat-1', provider: 'wechat.web' },
            { id: 'acme-1', provider: 'acme-sso' },
          ]),
        )
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        // The deployment offers two methods this family has no wording
        // for; the add area still uses their own labels (or names).
        return jsonResponse(
          200,
          methodsDoc([
            { name: 'mystery', label: 'Mystery IdP' },
            { name: 'unlabelled' },
          ]),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
      />,
    )

    // A bound value no descriptor matches renders the family's own
    // wording for a name it knows (the WeChat pair, mode-keyed) and the
    // generic other label otherwise; the add area falls back to the
    // method name, never a raw bundle key.
    expect(
      await screen.findByText(zhCN.bindings.provider.wechatWeb),
    ).toBeTruthy()
    expect(screen.getByText(zhCN.bindings.provider.other)).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Mystery IdP' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'unlabelled' })).toBeTruthy()
  })

  it('renders provider icons supplied by the host', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profile([]))
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(
          200,
          methodsDoc([{ name: 'github', label: 'GitHub' }]),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
        providerIcon={(provider) => (
          <span data-testid={`icon-${provider}`} aria-hidden="true" />
        )}
      />,
    )

    expect(await screen.findByRole('button', { name: 'GitHub' })).toBeTruthy()
    expect(screen.getByTestId('icon-github')).toBeTruthy()
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
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc([]))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
      />,
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
