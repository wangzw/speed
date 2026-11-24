/**
 * SocialBindingsSection checks bound identities, provider resolution, the unlink route and the link flow.
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

const ACTIVE_FACTOR = {
  id: 'factor-1',
  kind: 'totp',
  label: 'App',
  status: 'active',
  created_at: '2026-01-01T00:00:00Z',
}

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
    human: {
      phone: { required: false, provider: 'off' },
      other: { required: false, provider: 'off' },
    },
  }
}

function profile(links: readonly Record<string, unknown>[]): Record<string, unknown> {
  return {
    account_id: 'user-1',
    kind: 'user',
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

function bindButton(label: string): HTMLElement {
  return screen.getByRole('button', {
    name: zhCN.bindings.bindAriaWithProvider.replace('{{provider}}', label),
  })
}

async function findBindButton(label: string): Promise<HTMLElement> {
  return screen.findByRole('button', {
    name: zhCN.bindings.bindAriaWithProvider.replace('{{provider}}', label),
  })
}

function unbindButton(label: string): HTMLElement {
  return screen.getByRole('button', {
    name: zhCN.bindings.unbindAriaWithProvider.replace('{{provider}}', label),
  })
}

function deferredResponse(): {
  readonly response: Promise<Response>
  readonly release: (response: Response) => void
} {
  let release: (response: Response) => void = () => {}
  const response = new Promise<Response>((resolve) => {
    release = resolve
  })
  return { response, release }
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

    expect(await screen.findByText('WeChat')).toBeTruthy()
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
            {
              name: 'github',
              label: 'GitHub Enterprise',
              bindingProviders: ['gh:'],
            },
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

    expect(
      await screen.findByText(zhCN.bindings.provider.github),
    ).toBeTruthy()
    expect(screen.getByText('GitLab')).toBeTruthy()
    expect(
      bindButton('GitHub Enterprise'),
    ).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'GitLab' })).toBeNull()
    await expectNoAxeViolations()
  })

  it('gives two sign-in modes that share a descriptor label distinct accessible names', async () => {
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
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
      />,
    )

    const emptyTitle = await screen.findByText(zhCN.bindings.empty.title)
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
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
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

  it('keeps the row unlink busy while the profile refetch settles', async () => {
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

    await waitFor(() => expect(refetchPending).toBe(true))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(rowUnlink()).toHaveAttribute('aria-disabled', 'true')
    expect(rowUnlink()).toHaveAttribute('aria-busy', 'true')
    await waitFor(() => expect(rowUnlink()).toHaveFocus())

    await act(async () => {
      releaseRefetch?.(jsonResponse(200, profile([])))
    })
    await waitFor(() => expect(rowUnlink()).toBeNull())
  })

  it('holds one result slot for the unlink and add-area writes', async () => {
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

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await user.click(bindButton('GitHub'))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.bad_request)
    expect(screen.queryByText(unlinkedText)).toBeNull()
  })

  it('keeps the unbind confirmation when the list empties', async () => {
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
    expect(screen.getByText(zhCN.bindings.provider.github)).toBeTruthy()
  })

  it('opens the step-up challenge when the unlink asks for a fresh authentication', async () => {
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

    expect(
      await screen.findByRole('dialog', { name: zhCN.mfa.stepUp.title }),
    ).toBeTruthy()
    expect(await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp })).toBeTruthy()
    expect(screen.queryAllByRole('alert', { hidden: true })).toHaveLength(0)
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

    await user.click(await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp }))
    await user.type(
      await screen.findByLabelText(zhCN.flowStep.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

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
    await user.click(await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp }))
    await user.type(
      await screen.findByLabelText(zhCN.flowStep.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() => expect(reports).toEqual([URL]))
    expect(linkStarts).toHaveLength(2)
  })

  it('records the bind it must resume when the re-login exit is chosen', async () => {
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
    expect(pendingStepUpResume('user-1')).toEqual({
      account: 'user-1',
      scope: 'social-bindings',
      path: '/account/security',
      operation: { kind: 'authorize', method: 'wechat' },
    })
  })

  it('re-issues the recorded bind when the page mounts after the new sign-in', async () => {
    rememberStepUpResume('user-1', 'social-bindings', '/account/security', {
      kind: 'authorize',
      method: 'wechat',
    })
    const reports: string[] = []
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
    const rig = await makeRotationRig(respond)
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
        returnPath="/account/security"
        onAuthorizeUrl={(url) => reports.push(url)}
      />,
    )

    await waitFor(() => expect(reports).toEqual([WECHAT_AUTHORIZE_URL]))
    expect(pendingStepUpResume('user-1')).toBeNull()
  })

  it('leaves a record that names another page for that page to resume', async () => {
    rememberStepUpResume('user-1', 'social-bindings', '/account/sessions', {
      kind: 'authorize',
      method: 'wechat',
    })
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
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(
      <SocialBindingsSection
        session={rig.session}
        returnTo={ACCOUNT_CALLBACK}
        returnPath="/account/security"
        onAuthorizeUrl={() => undefined}
      />,
    )

    await waitFor(() =>
      expect(
        rig.calls.some(
          (call) =>
            call.method === 'POST' && call.path === LINK_FLOW_PATH,
        ),
      ).toBe(false),
    )
    expect(pendingStepUpResume('user-1')).toEqual({
      account: 'user-1',
      scope: 'social-bindings',
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
    expect(start?.body).toEqual({
      purpose: 'link',
      method: 'wechat',
      return_to: ACCOUNT_CALLBACK,
    })
  })

  it('carries the source page inside the link flow return_to', async () => {
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
    captureRefreshWarning({ expected: true })
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

    expect(screen.getByText('ada@example.test')).toBeTruthy()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.stale.description)
    expect(screen.queryByText(zhCN.bindings.error.title)).toBeNull()

    const readsBeforeRetry = reads
    await user.click(screen.getByRole('button', { name: zhCN.bindings.retry }))
    await waitFor(() => expect(reads).toBeGreaterThan(readsBeforeRetry))
  })

  describe('focusable initiating controls', () => {
    function linksWithout(
      unlinked: readonly string[],
      rows: readonly Record<string, unknown>[],
    ): Record<string, unknown> {
      return profile(rows.filter((row) => !unlinked.includes(row.id as string)))
    }

    it('keeps focus on the row unlink action after the confirmation closes while the attempt is in flight', async () => {
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
            profile(
              unlinked ? [] : [{ id: 'github-1', provider: 'github' }],
            ),
          )
        }
        if (call.method === 'GET' && call.path === METHODS_PATH) {
          return jsonResponse(200, methodsDoc([]))
        }
        if (
          call.method === 'DELETE' &&
          call.path === '/api/v1/auth/links/github-1'
        ) {
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
      const label = zhCN.bindings.provider.github

      await user.click(await screen.findByRole('button', {
        name: zhCN.bindings.unbindAriaWithProvider.replace(
          '{{provider}}',
          label,
        ),
      }))
      const dialog = await screen.findByRole('dialog')
      await user.click(
        within(dialog).getByRole('button', {
          name: zhCN.bindings.confirmLabel,
        }),
      )

      await waitFor(() => expect(refetchPending).toBe(true))
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
      const rowAction = unbindButton(label)
      await waitFor(() => expect(rowAction).toHaveFocus())
      expect(rowAction).toHaveAttribute('aria-disabled', 'true')
      expect(rowAction).toHaveAttribute('aria-busy', 'true')

      await act(async () => {
        releaseRefetch?.(jsonResponse(200, profile([])))
      })
      await waitFor(() =>
        expect(
          screen.queryByRole('button', {
            name: zhCN.bindings.unbindAriaWithProvider.replace(
              '{{provider}}',
              label,
            ),
          }),
        ).toBeNull(),
      )
    })

    it('leaves focus on the row unlink action when the unlink is refused', async () => {
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
        if (
          call.method === 'DELETE' &&
          call.path === '/api/v1/auth/links/github-1'
        ) {
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
      const label = zhCN.bindings.provider.github

      await user.click(await screen.findByRole('button', {
        name: zhCN.bindings.unbindAriaWithProvider.replace(
          '{{provider}}',
          label,
        ),
      }))
      const dialog = await screen.findByRole('dialog')
      await user.click(
        within(dialog).getByRole('button', {
          name: zhCN.bindings.confirmLabel,
        }),
      )

      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
      expect(
        await screen.findByText(zhCN.errors.authn.last_login_method),
      ).toBeTruthy()
      const rowAction = unbindButton(label)
      await waitFor(() => expect(rowAction).toHaveFocus())
      expect(rowAction).not.toHaveAttribute('aria-disabled', 'true')
    })

    it("moves focus to the next row's unlink action when a row leaves", async () => {
      const unlinked = new Set<string>()
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            linksWithout([...unlinked], [
              { id: 'github-1', provider: 'github' },
              { id: 'feishu-1', provider: 'feishu' },
            ]),
          )
        }
        if (call.method === 'GET' && call.path === METHODS_PATH) {
          return jsonResponse(200, methodsDoc([]))
        }
        if (
          call.method === 'DELETE' &&
          call.path === '/api/v1/auth/links/github-1'
        ) {
          unlinked.add('github-1')
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

      await waitFor(() =>
        expect(screen.queryByText(zhCN.bindings.provider.github)).toBeNull(),
      )
      await waitFor(() =>
        expect(unbindButton(zhCN.bindings.provider.feishu)).toHaveFocus(),
      )
    })

    it("moves focus to the previous row's unlink action when the last row leaves", async () => {
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            profile([
              { id: 'github-1', provider: 'github' },
              { id: 'feishu-1', provider: 'feishu' },
            ]),
          )
        }
        if (call.method === 'GET' && call.path === METHODS_PATH) {
          return jsonResponse(200, methodsDoc([]))
        }
        if (
          call.method === 'DELETE' &&
          call.path === '/api/v1/auth/links/feishu-1'
        ) {
          return new Response(null, { status: 204 })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      let reads = 0
      const rig = makeRealClientRig((call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          reads += 1
          if (reads > 1) {
            return jsonResponse(
              200,
              profile([{ id: 'github-1', provider: 'github' }]),
            )
          }
        }
        return respond(call)
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
            zhCN.bindings.provider.feishu,
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
        expect(screen.queryByText(zhCN.bindings.provider.feishu)).toBeNull(),
      )
      await waitFor(() =>
        expect(unbindButton(zhCN.bindings.provider.github)).toHaveFocus(),
      )
    })

    it('hands focus to the section container when the only binding leaves', async () => {
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
        if (
          call.method === 'DELETE' &&
          call.path === '/api/v1/auth/links/github-1'
        ) {
          return new Response(null, { status: 204 })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      let reads = 0
      const rig = makeRealClientRig((call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          reads += 1
          if (reads > 1) {
            return jsonResponse(200, profile([]))
          }
        }
        return respond(call)
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
      const dialog = await screen.findByRole('dialog')
      await user.click(
        within(dialog).getByRole('button', {
          name: zhCN.bindings.confirmLabel,
        }),
      )

      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
      const region = screen.getByRole('group', {
        name: zhCN.bindings.title,
      })
      await waitFor(() => expect(region).toHaveFocus())
    })

    it('does not take focus back when the operator moved it during the removal', async () => {
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
            profile([{ id: 'github-1', provider: 'github' }]),
          )
        }
        if (call.method === 'GET' && call.path === METHODS_PATH) {
          return jsonResponse(
            200,
            methodsDoc([{ name: 'wechat', label: 'WeChat' }]),
          )
        }
        if (
          call.method === 'DELETE' &&
          call.path === '/api/v1/auth/links/github-1'
        ) {
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

      await waitFor(() => expect(refetchPending).toBe(true))
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
      const elsewhere = await findBindButton('WeChat')
      elsewhere.focus()
      await act(async () => {
        releaseRefetch?.(jsonResponse(200, profile([])))
      })
      await waitFor(() =>
        expect(screen.queryByText(zhCN.bindings.provider.github)).toBeNull(),
      )
      expect(elsewhere).toHaveFocus()
    })

    it('holds the sibling rows and channel actions unavailable without a reason while an unlink is in flight', async () => {
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
            profile([
              { id: 'github-1', provider: 'github' },
              { id: 'feishu-1', provider: 'feishu' },
            ]),
          )
        }
        if (call.method === 'GET' && call.path === METHODS_PATH) {
          return jsonResponse(
            200,
            methodsDoc([{ name: 'wechat', label: 'WeChat' }]),
          )
        }
        if (
          call.method === 'DELETE' &&
          call.path === '/api/v1/auth/links/github-1'
        ) {
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
      await waitFor(() => expect(refetchPending).toBe(true))
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

      const started = unbindButton(zhCN.bindings.provider.github)
      const sibling = unbindButton(zhCN.bindings.provider.feishu)
      const channel = bindButton('WeChat')
      expect(started).toHaveAttribute('aria-disabled', 'true')
      expect(started).toHaveAttribute('aria-busy', 'true')
      expect(sibling).toHaveAttribute('aria-disabled', 'true')
      expect(sibling).not.toHaveAttribute('aria-busy')
      expect(sibling).not.toHaveAccessibleDescription()
      expect(channel).toHaveAttribute('aria-disabled', 'true')
      expect(channel).not.toHaveAttribute('aria-busy')
      expect(channel).not.toHaveAccessibleDescription()

      await user.click(sibling)
      expect(screen.queryByRole('dialog')).toBeNull()
      await user.click(channel)
      sibling.focus()
      await user.keyboard('{Enter}')
      expect(screen.queryByRole('dialog')).toBeNull()
      channel.focus()
      await user.keyboard('{Enter}')
      expect(
        rig.calls.filter(
          (call) => call.method === 'POST' && call.path === LINK_FLOW_PATH,
        ),
      ).toHaveLength(0)
      expect(
        rig.calls.filter(
          (call) =>
            call.method === 'DELETE' &&
            call.path === '/api/v1/auth/links/feishu-1',
        ),
      ).toHaveLength(0)

      await act(async () => {
        releaseRefetch?.(
          jsonResponse(
            200,
            profile([{ id: 'feishu-1', provider: 'feishu' }]),
          ),
        )
      })
    })

    it('marks only the channel being started busy and holds the others unavailable', async () => {
      const started = deferredResponse()
      const linkStarts: string[] = []
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            profile([{ id: 'feishu-1', provider: 'feishu' }]),
          )
        }
        if (call.method === 'GET' && call.path === METHODS_PATH) {
          return jsonResponse(
            200,
            methodsDoc([
              { name: 'wechat', label: 'WeChat' },
              { name: 'github', label: 'GitHub' },
            ]),
          )
        }
        if (call.method === 'POST' && call.path === LINK_FLOW_PATH) {
          linkStarts.push('link')
          return started.response
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const rig = makeRealClientRig(respond)
      rig.store.set('access-1')
      renderWithProviders(
        <SocialBindingsSection
          session={rig.session}
          returnTo={ACCOUNT_CALLBACK}
          onAuthorizeUrl={() => undefined}
        />,
      )
      const user = userEvent.setup()
      const wechat = await findBindButton('WeChat')
      const github = bindButton('GitHub')
      const rowAction = unbindButton(zhCN.bindings.provider.feishu)

      await user.click(wechat)
      await waitFor(() => expect(linkStarts).toHaveLength(1))

      expect(wechat).toHaveAttribute('aria-busy', 'true')
      expect(wechat).toHaveAttribute('aria-disabled', 'true')
      expect(github).toHaveAttribute('aria-disabled', 'true')
      expect(github).not.toHaveAttribute('aria-busy')
      expect(github).not.toHaveAccessibleDescription()
      expect(rowAction).toHaveAttribute('aria-disabled', 'true')
      expect(rowAction).not.toHaveAttribute('aria-busy')
      expect(rowAction).not.toHaveAccessibleDescription()

      await user.click(github)
      expect(linkStarts).toHaveLength(1)
      await user.click(rowAction)
      expect(screen.queryByRole('dialog')).toBeNull()
      github.focus()
      await user.keyboard('{Enter}')
      expect(linkStarts).toHaveLength(1)
      rowAction.focus()
      await user.keyboard('{Enter}')
      expect(screen.queryByRole('dialog')).toBeNull()

      await act(async () => {
        started.release(
          jsonResponse(200, {
            seq: 1,
            state: 'first',
            next: {
              kind: 'redirect',
              url: WECHAT_AUTHORIZE_URL,
              method: 'GET',
            },
          }),
        )
      })
    })
  })
})
