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
 * descriptors; an unlink DELETE-s the link and refetches the profile, and
 * its row action stays disabled through that refetch; a refused unlink (the
 * account's last sign-in method) renders its code text below the list and
 * leaves the row; the unlink actions and the add area's flow starts share one
 * write scope, so their results never stand side by side; and the add area's
 * click starts a link flow carrying the host's `return_to` at the top level,
 * whose redirect step's URL is reported upward (the section never navigates).
 */

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
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
import { captureRefreshWarning } from '../test-utils/refresh-warning.js'
import {
  AUTHENTICATED_SESSION,
  EMPTY_FEATURES,
  FEATURES_PATH,
  SESSION_PATH,
  makeRotationRig,
} from '../test-utils/rotation.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import {
  pendingStepUpResume,
  rememberStepUpResume,
} from './internal/step-up-resume.js'
import { SocialBindingsSection } from './SocialBindingsSection.js'

const PROFILE_PATH = '/api/v1/auth/me'
const METHODS_PATH = '/api/v1/auth/methods'
const FACTORS_PATH = '/api/v1/auth/factors'
const LINK_FLOW_PATH = '/api/v1/auth/flows/authenticated'
const FLOW_ANSWER = '/api/v1/auth/flows/answer'
const ACCOUNT_CALLBACK = '/account/callback'

/** One active factor, as the step-up challenge's own factor read answers it. */
const ACTIVE_FACTOR = {
  id: 'factor-1',
  kind: 'totp',
  label: 'App',
  status: 'active',
  created_at: '2026-01-01T00:00:00Z',
}

/** A fresh password-established session view, as the no-factor branch of the
 * step-up challenge reads it. */
function sessionView(): Record<string, unknown> {
  return {
    authenticated: true,
    session_id: 'sess-1',
    account_id: 'user-1',
    methods: ['pwd'],
    assurance: 'single',
    auth_time: new Date().toISOString(),
  }
}

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
    human: { required: false, provider: 'off' },
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

/** The bind action of the provider row whose title is `label`. */
function bindButton(label: string): HTMLElement {
  return screen.getByRole('button', {
    name: zhCN.bindings.bindAriaWithProvider.replace('{{provider}}', label),
  })
}

/** Waits for the provider row, then returns its bind action. */
async function findBindButton(label: string): Promise<HTMLElement> {
  return screen.findByRole('button', {
    name: zhCN.bindings.bindAriaWithProvider.replace('{{provider}}', label),
  })
}

describe('SocialBindingsSection', () => {
  beforeEach(() => {
    window.sessionStorage.clear()
  })

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
      await findBindButton('WeChat'),
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
      bindButton('GitHub Enterprise'),
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
      name: zhCN.bindings.bindAriaWithProvider.replace(
        '{{provider}}',
        zhCN.bindings.provider.wechatWeb,
      ),
    })
    const officialAccount = screen.getByRole('button', {
      name: zhCN.bindings.bindAriaWithProvider.replace(
        '{{provider}}',
        zhCN.bindings.provider.wechatOfficialAccount,
      ),
    })
    // Two buttons, two distinct names: neither is reachable by the shared
    // descriptor label alone.
    expect(web).not.toBe(officialAccount)
    expect(
      screen.queryAllByRole('button', {
        name: zhCN.bindings.bindAriaWithProvider.replace('{{provider}}', 'WeChat'),
      }),
    ).toHaveLength(0)
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
    // than succeeding silently, and the confirmation names the provider.
    expect(zhCN.bindings.unlinked).toContain('{{provider}}')
    expect(
      await screen.findByText(
        zhCN.bindings.unlinked.replace('{{provider}}', 'GitHub'),
      ),
    ).toBeTruthy()
    expect(
      screen.queryByRole('button', {
        name: zhCN.bindings.unbindAriaWithProvider.replace('{{provider}}', 'GitHub'),
      }),
    ).toBeNull()
    expect(
      bindButton('GitHub'),
    ).toBeTruthy()
    expect(
      rig.calls.filter(
        (call) => call.method === 'GET' && call.path === PROFILE_PATH,
      ).length,
    ).toBeGreaterThanOrEqual(2)
  })

  it('keeps the row unlink disabled while the profile refetch settles', async () => {
    let unlinked = false
    let refetchPending = false
    let releaseRefetch: ((response: Response) => void) | undefined
    const respond: RealResponder = async (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        if (refetchPending) {
          return new Promise<Response>((resolve) => {
            releaseRefetch = resolve
          })
        }
        return jsonResponse(
          200,
          profile(unlinked ? [] : [{ id: 'github-1', provider: 'github' }]),
        )
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc([]))
      }
      if (call.method === 'DELETE' && call.path === '/api/v1/auth/links/github-1') {
        unlinked = true
        refetchPending = true
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
    const rowUnlink = () =>
      screen.queryByRole('button', {
        name: zhCN.bindings.unbindAriaWithProvider.replace(
          '{{provider}}',
          zhCN.bindings.provider.github,
        ),
      })

    await user.click(
      (await screen.findByRole('button', {
        name: zhCN.bindings.unbindAriaWithProvider.replace(
          '{{provider}}',
          zhCN.bindings.provider.github,
        ),
      })) as HTMLElement,
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.bindings.confirmLabel }),
    )

    // The confirm dialog closes when the mutation settles, but the attempt
    // is still in flight through the refetch that follows it: the row's
    // action must stay disabled, not re-arm for a second DELETE against the
    // row the first one already unlinked.
    await waitFor(() => expect(refetchPending).toBe(true))
    // Wait out the dialog's exit transition: while it runs the page behind
    // it is hidden from the accessibility tree and role queries.
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(rowUnlink()).toBeDisabled()

    await act(async () => {
      releaseRefetch?.(jsonResponse(200, profile([])))
    })
    await waitFor(() => expect(rowUnlink()).toBeNull())
  })

  it('holds one result slot for the unlink and add-area writes', async () => {
    // One list, one scope: the unlink success and the add area's refused
    // start report through the same slot, so they can never stand side by
    // side -- the attempt that settles last replaces the one before it.
    let unlinked = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profile(
            unlinked
              ? []
              : [{ id: 'github-1', provider: 'github' }],
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
      if (call.method === 'POST' && call.path === LINK_FLOW_PATH) {
        return errorResponse(400, 'authn.bad_request')
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
        name: zhCN.bindings.unbindAriaWithProvider.replace(
          '{{provider}}',
          'GitHub',
        ),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.bindings.confirmLabel }),
    )
    const unlinkedText = zhCN.bindings.unlinked.replace(
      '{{provider}}',
      'GitHub',
    )
    expect(await screen.findByText(unlinkedText)).toBeTruthy()

    // Now drive the same scope to a failure from the add area.
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await user.click(bindButton('GitHub'))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.bad_request)
    // The failure is the slot's one result: the earlier unlink confirmation
    // was cleared when this attempt started and does not stand beside it.
    expect(screen.queryByText(unlinkedText)).toBeNull()
  })

  it('keeps the unbind confirmation when the list empties', async () => {
    // The only binding goes and the deployment offers no channel to replace
    // it, so the section falls back to its empty placeholder. The confirmation
    // still names the provider: a settled result must not vanish with the
    // rows it replaced.
    let unlinked = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profile(
            unlinked ? [] : [{ id: 'github-1', provider: 'github' }],
          ),
        )
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc([]))
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

    expect(await screen.findByText(zhCN.bindings.empty.title)).toBeTruthy()
    const confirmation = await screen.findByText(
      zhCN.bindings.unlinked.replace(
        '{{provider}}',
        zhCN.bindings.provider.github,
      ),
    )
    expect(confirmation).toBeTruthy()
    // The confirmation sits below the list (and the empty placeholder that
    // replaced it), as the ui-kit rule asks for a control group's region.
    expect(
      screen
        .getByText(zhCN.bindings.empty.title)
        .compareDocumentPosition(confirmation) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    await expectNoAxeViolations()
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
        return errorResponse(409, 'authn.last_login_method')
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
    expect(alert).toHaveTextContent(zhCN.errors.authn.last_login_method)
    // The refusal leaves the row in place.
    expect(screen.getByText(zhCN.bindings.provider.github)).toBeTruthy()
  })

  it('opens the step-up challenge when the unlink asks for a fresh authentication', async () => {
    // The unlink route requires a fresh local multi-factor authentication. An
    // account that holds an active factor can satisfy that; the refusal is
    // handed to the shared challenge instead of only rendering its code.
    captureRefreshWarning()
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
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [ACTIVE_FACTOR] })
      }
      if (call.method === 'DELETE' && call.path === '/api/v1/auth/links/github-1') {
        return errorResponse(401, 'authn.insufficient_user_authentication')
      }
      if (call.method === 'POST' && call.path === LINK_FLOW_PATH) {
        return jsonResponse(200, {
          seq: 1,
          state: 'second',
          next: {
            kind: 'factor',
            factors: [{ id: 'factor-1', kind: 'totp', label: 'App' }],
          },
        })
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
        name: zhCN.bindings.unbindAriaWithProvider.replace(
          '{{provider}}',
          zhCN.bindings.provider.github,
        ),
      }),
    )
    const confirm = await screen.findByRole('dialog', {
      name: zhCN.bindings.confirmTitle,
    })
    await user.click(
      within(confirm).getByRole('button', {
        name: zhCN.bindings.confirmLabel,
      }),
    )

    // The challenge opens on the factor step, ready for the factor the owner
    // holds. The refusal is not also settled as a failure in the section's
    // region: the attempt was handed to the challenge. The scan includes
    // hidden nodes because an open dialog hides the page behind it.
    expect(
      await screen.findByRole('dialog', { name: zhCN.mfa.stepUp.title }),
    ).toBeTruthy()
    expect(await screen.findByRole('button', { name: 'App' })).toBeTruthy()
    expect(screen.queryAllByRole('alert', { hidden: true })).toHaveLength(0)
    // No automatic replay: the unlink waits for a won challenge.
    expect(
      rig.calls.filter(
        (call) =>
          call.method === 'DELETE' &&
          call.path === '/api/v1/auth/links/github-1',
      ),
    ).toHaveLength(1)
  })

  it('retries the unlink exactly once after the step-up challenge is won', async () => {
    captureRefreshWarning()
    let elevated = false
    let unlinked = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profile(
            unlinked ? [] : [{ id: 'github-1', provider: 'github' }],
          ),
        )
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc([]))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [ACTIVE_FACTOR] })
      }
      if (call.method === 'DELETE' && call.path === '/api/v1/auth/links/github-1') {
        if (!elevated) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        unlinked = true
        return new Response(null, { status: 204 })
      }
      if (call.method === 'POST' && call.path === LINK_FLOW_PATH) {
        return jsonResponse(200, {
          seq: 1,
          state: 'second',
          next: {
            kind: 'factor',
            factors: [{ id: 'factor-1', kind: 'totp', label: 'App' }],
          },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        const body = call.body as { seq: number }
        if (body.seq === 1) {
          return jsonResponse(200, {
            seq: 2,
            state: 'second',
            next: {
              kind: 'challenge',
              fields: [{ name: 'code', kind: 'text' }],
            },
          })
        }
        elevated = true
        return jsonResponse(200, {
          state: 'elevated',
          token_type: 'Bearer',
          access_token: 'access-2',
          expires_in: 3600,
        })
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
        name: zhCN.bindings.unbindAriaWithProvider.replace(
          '{{provider}}',
          zhCN.bindings.provider.github,
        ),
      }),
    )
    const confirm = await screen.findByRole('dialog', {
      name: zhCN.bindings.confirmTitle,
    })
    await user.click(
      within(confirm).getByRole('button', {
        name: zhCN.bindings.confirmLabel,
      }),
    )

    await user.click(await screen.findByRole('button', { name: 'App' }))
    await user.type(
      await screen.findByLabelText(zhCN.flowStep.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    // The won challenge replays the refused unlink, which now lands and names
    // the provider it removed.
    expect(
      await screen.findByText(
        zhCN.bindings.unlinked.replace(
          '{{provider}}',
          zhCN.bindings.provider.github,
        ),
      ),
    ).toBeTruthy()
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(
      rig.calls.filter(
        (call) =>
          call.method === 'DELETE' &&
          call.path === '/api/v1/auth/links/github-1',
      ),
    ).toHaveLength(2)
  })

  it('shows the shared no-factor explanation when the account holds no factor', async () => {
    // An account with no active factor cannot satisfy a local multi-factor
    // requirement. The shared challenge names the missing two-step
    // verification, starts no flow and offers no re-login here: a fresh
    // single-factor session would meet the same wall. The surface inherits
    // that shape unchanged.
    captureRefreshWarning()
    const rig = makeRealClientRig((call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profile([{ id: 'github-1', provider: 'github' }]),
        )
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc([]))
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [] })
      }
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return jsonResponse(200, sessionView())
      }
      if (call.method === 'DELETE' && call.path === '/api/v1/auth/links/github-1') {
        return errorResponse(401, 'authn.insufficient_user_authentication')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    })
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
        name: zhCN.bindings.unbindAriaWithProvider.replace(
          '{{provider}}',
          zhCN.bindings.provider.github,
        ),
      }),
    )
    const confirm = await screen.findByRole('dialog', {
      name: zhCN.bindings.confirmTitle,
    })
    await user.click(
      within(confirm).getByRole('button', {
        name: zhCN.bindings.confirmLabel,
      }),
    )

    expect(
      await screen.findByText(zhCN.mfa.stepUp.noFactor),
    ).toBeTruthy()
    expect(
      screen.queryByRole('button', { name: zhCN.mfa.stepUp.relogin }),
    ).toBeNull()
    expect(
      rig.calls.some(
        (call) =>
          call.method === 'POST' && call.path === LINK_FLOW_PATH,
      ),
    ).toBe(false)
  })

  it('opens the step-up challenge when starting a bind asks for a fresh authentication', async () => {
    // Starting a link flow asks for the same fresh local multi-factor an
    // unlink does. The shortfall opens the shared challenge with this method
    // remembered, and a won challenge starts this one bind flow again.
    captureRefreshWarning()
    const URL = 'https://provider.example/authorize'
    let elevated = false
    const linkStarts: string[] = []
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
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [ACTIVE_FACTOR] })
      }
      if (call.method === 'POST' && call.path === LINK_FLOW_PATH) {
        const body = call.body as { purpose?: string; seq?: number }
        if (body.purpose === 'step-up') {
          return jsonResponse(200, {
            seq: 1,
            state: 'second',
            next: {
              kind: 'factor',
              factors: [{ id: 'factor-1', kind: 'totp', label: 'App' }],
            },
          })
        }
        linkStarts.push('link')
        if (!elevated) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        return jsonResponse(200, {
          seq: 1,
          state: 'first',
          next: { kind: 'redirect', url: URL, method: 'GET' },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        const body = call.body as { seq: number }
        if (body.seq === 1) {
          return jsonResponse(200, {
            seq: 2,
            state: 'second',
            next: {
              kind: 'challenge',
              fields: [{ name: 'code', kind: 'text' }],
            },
          })
        }
        elevated = true
        return jsonResponse(200, {
          state: 'elevated',
          token_type: 'Bearer',
          access_token: 'access-2',
          expires_in: 3600,
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

    await user.click(await findBindButton('WeChat'))
    // The refusal opens the shared challenge rather than settling as a banner.
    await user.click(await screen.findByRole('button', { name: 'App' }))
    await user.type(
      await screen.findByLabelText(zhCN.flowStep.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    // The bind flow started once, was refused, and the won challenge started
    // it again -- never a second, different call.
    await waitFor(() => expect(reports).toEqual([URL]))
    expect(linkStarts).toHaveLength(2)
  })

  it('records the bind it must resume when the re-login exit is chosen', async () => {
    // The deployment cannot raise this session with the account's factors, so
    // the challenge offers "sign in to continue". Choosing it records the
    // page and the bind operation before the session ends, so the surface can
    // re-issue that bind after the new sign-in.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profile([]))
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(
          200,
          methodsDoc([{ name: 'wechat', label: 'WeChat' }]),
        )
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [ACTIVE_FACTOR] })
      }
      if (call.method === 'POST' && call.path === LINK_FLOW_PATH) {
        // Both the bind start and the step-up start are refused: no factor
        // may strengthen this session, so no step can satisfy the challenge.
        return errorResponse(401, 'authn.insufficient_user_authentication')
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/logout') {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    const logout = vi.spyOn(rig.session, 'logout')
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
        returnPath="/account/security"
        onAuthorizeUrl={() => undefined}
      />,
    )
    const user = userEvent.setup()

    await user.click(await findBindButton('WeChat'))
    expect(
      await screen.findByText(zhCN.mfa.stepUp.cannotElevate),
    ).toBeTruthy()
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.relogin }),
    )

    expect(logout).toHaveBeenCalledTimes(1)
    expect(pendingStepUpResume()).toEqual({
      path: '/account/security',
      operation: { kind: 'authorize', method: 'wechat' },
    })
  })

  it('re-issues the recorded bind when the page mounts after the new sign-in', async () => {
    // The surface mounted again on the page the record names: it starts the
    // recorded bind once, without a click, and consumes the record.
    rememberStepUpResume('/account/security', {
      kind: 'authorize',
      method: 'wechat',
    })
    const reports: string[] = []
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
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
        returnPath="/account/security"
        onAuthorizeUrl={(url) => reports.push(url)}
      />,
    )

    await waitFor(() => expect(reports).toEqual([WECHAT_AUTHORIZE_URL]))
    expect(pendingStepUpResume()).toBeNull()
  })

  it('leaves a record that names another page for that page to resume', async () => {
    rememberStepUpResume('/account/sessions', {
      kind: 'authorize',
      method: 'wechat',
    })
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
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
        returnPath="/account/security"
        onAuthorizeUrl={() => undefined}
      />,
    )

    // The section never starts a flow it does not own, and the record stays
    // for the page it names.
    await waitFor(() =>
      expect(
        rig.calls.some(
          (call) =>
            call.method === 'POST' && call.path === LINK_FLOW_PATH,
        ),
      ).toBe(false),
    )
    expect(pendingStepUpResume()).toEqual({
      path: '/account/sessions',
      operation: { kind: 'authorize', method: 'wechat' },
    })
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

    await user.click(await findBindButton('WeChat'))

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

  it('carries the source page inside the link flow return_to', async () => {
    // A binding starts from an account pane, but the provider lands the
    // browser on the host's callback route. The source page rides inside the
    // flow's return_to as the callback route's `return` query, so the
    // callback can send the viewer back where they started.
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
          next: { kind: 'redirect', url: WECHAT_AUTHORIZE_URL, method: 'GET' },
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
        returnPath="/account/security"
        onAuthorizeUrl={() => undefined}
      />,
    )
    const user = userEvent.setup()

    await user.click(await findBindButton('WeChat'))

    await waitFor(() =>
      expect(
        rig.calls.find(
          (call) => call.method === 'POST' && call.path === LINK_FLOW_PATH,
        )?.body,
      ).toEqual({
        purpose: 'link',
        method: 'wechat',
        return_to: `${ACCOUNT_CALLBACK}?return=%2Faccount%2Fsecurity`,
      }),
    )
  })

  it('drops a source that names another origin from the return_to', async () => {
    // Only a site-internal path is carried. A source value that names an
    // origin is dropped rather than sent, and the callback uses its own safe
    // landing.
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
          next: { kind: 'redirect', url: WECHAT_AUTHORIZE_URL, method: 'GET' },
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
        returnPath="//elsewhere.example/account"
        onAuthorizeUrl={() => undefined}
      />,
    )
    const user = userEvent.setup()

    await user.click(await findBindButton('WeChat'))

    await waitFor(() =>
      expect(
        rig.calls.find(
          (call) => call.method === 'POST' && call.path === LINK_FLOW_PATH,
        )?.body,
      ).toEqual({
        purpose: 'link',
        method: 'wechat',
        return_to: ACCOUNT_CALLBACK,
      }),
    )
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
    expect(bindButton('Mystery IdP')).toBeTruthy()
    expect(bindButton('unlabelled')).toBeTruthy()
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

    expect(await findBindButton('GitHub')).toBeTruthy()
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

  it('holds the session through the credential rotation an unlink performs', async () => {
    // The unlink replaces the requesting session's credential. The profile
    // refetch that follows the write is answered 401, standing for a read
    // that left under the outgoing credential. The surface opens an
    // identity-transition window around the write, so the anonymous answer
    // is discarded: the snapshot stays authenticated and no sign-out edge is
    // observed. Without the window the same script ends the session.
    const warn = captureRefreshWarning()
    let rotated = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return rotated
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return rotated
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(
              200,
              profile([
                {
                  id: 'github-1',
                  provider: 'github',
                  email: 'ada@example.test',
                },
              ]),
            )
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(
          200,
          methodsDoc([{ name: 'github', label: 'GitHub' }]),
        )
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/links/github-1'
      ) {
        rotated = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
      />,
    )
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.bindings.unbindAriaWithProvider.replace(
          '{{provider}}',
          'GitHub',
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
      expect(warn).toHaveBeenCalledWith(
        'access token refresh failed',
        expect.objectContaining({ status: 401 }),
      ),
    )
    expect(rig.session.getSnapshot().state).toBe('authenticated')
    expect(rig.endedEdges()).toBe(0)
  })

  it('releases the window a refused unlink opened', async () => {
    // A refused unlink must not leave its window open: the next genuine
    // anonymous read has to end the session. The unlink is refused 409, so
    // no silent leg runs; once the failure is on screen the window is
    // closed, and a fresh 401 read turns the snapshot anonymous with one
    // sign-out edge. A window still open would discard that answer.
    let gone = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return gone
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return gone
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(
              200,
              profile([
                {
                  id: 'github-1',
                  provider: 'github',
                  email: 'ada@example.test',
                },
              ]),
            )
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(
          200,
          methodsDoc([{ name: 'github', label: 'GitHub' }]),
        )
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/links/github-1'
      ) {
        return errorResponse(409, 'authn.last_login_method')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
      />,
    )
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.bindings.unbindAriaWithProvider.replace(
          '{{provider}}',
          'GitHub',
        ),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.bindings.confirmLabel,
      }),
    )

    await screen.findByText(zhCN.errors.authn.last_login_method)
    expect(rig.transitionBegins()).toBe(1)

    // A fresh 401 read now settles the true state.
    gone = true
    await rig.session.restore()
    expect(rig.session.getSnapshot().state).toBe('anonymous')
    expect(rig.endedEdges()).toBe(1)
  })

  it('keeps the bound rows and marks them stale when a refresh fails', async () => {
    const user = userEvent.setup()
    let reads = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        reads += 1
        return reads === 1
          ? jsonResponse(
              200,
              profile([
                { id: 'github-1', provider: 'github', email: 'ada@example.test' },
              ]),
            )
          : errorResponse(500, 'authn.unavailable')
      }
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, methodsDoc([]))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const { queryClient } = renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
      />,
    )

    expect(await screen.findByText('ada@example.test')).toBeTruthy()

    await act(async () => {
      await queryClient.invalidateQueries()
    })

    // The failed refresh keeps the settled rows on screen under the stale
    // notice rather than replacing them with the error placeholder.
    expect(screen.getByText('ada@example.test')).toBeTruthy()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.stale.description)
    expect(screen.queryByText(zhCN.bindings.error.title)).toBeNull()

    const readsBeforeRetry = reads
    await user.click(screen.getByRole('button', { name: zhCN.bindings.retry }))
    await waitFor(() => expect(reads).toBeGreaterThan(readsBeforeRetry))
  })
})
