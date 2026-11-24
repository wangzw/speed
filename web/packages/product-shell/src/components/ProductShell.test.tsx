import { useEffect, useRef } from 'react'
import { act, fireEvent, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { attachSession } from '@speed/auth-core'
import type { AuthSession } from '@speed/auth-core'
import { createI18n, registerNamespace } from '@speed/i18n'
import {
  AUTH_UI_NAMESPACE,
  authUiResources,
} from '@speed/auth-ui'
import {
  LAYOUT_KIT_NAMESPACE,
  layoutKitResources,
} from '@speed/layout-kit'
import { PageLayout, UI_KIT_NAMESPACE, uiKitResources } from '@speed/ui-kit'
import { TEST_LANGUAGES } from '@speed/test-utils/render'
import authUiZhCN from '../../../auth-ui/src/locales/zh-CN.json' with { type: 'json' }
import layoutKitZhCN from '../../../layout-kit/src/locales/zh-CN.json' with { type: 'json' }
import productShellZhCN from '../locales/zh-CN.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import {
  authenticatedCompletion,
  jsonResponse,
  makeRealClientRig,
  openFlow,
  principal,
  type RealClientRig,
} from '../../test-utils/real-client.js'
import { ProductShell } from './ProductShell.js'

const FLOWS_START = 'POST /api/v1/auth/flows'
const FLOWS_ANSWER = 'POST /api/v1/auth/flows/answer'
const PROFILE = 'GET /api/v1/auth/me'
const LOGOUT = 'POST /api/v1/auth/logout'

const NAV_ITEMS = [
  { id: 'home', label: 'Home', href: '/home', selected: true },
] as const

function makeJourneyRig(): RealClientRig {
  return makeRealClientRig((call) => {
    const key = `${call.method} ${call.path}`
    if (key === FLOWS_START) {
      return jsonResponse(200, openFlow())
    }
    if (key === FLOWS_ANSWER) {
      return jsonResponse(200, authenticatedCompletion())
    }
    if (key === PROFILE) {
      return jsonResponse(200, principal())
    }
    if (key === LOGOUT) {
      return new Response(null, { status: 204 })
    }
    throw new Error(`unexpected request: ${key}`)
  })
}

async function signInTo(session: AuthSession): Promise<void> {
  await act(async () => {
    const first = await session.startFlow({ method: 'password' })
    if (!('next' in first)) {
      return
    }
    await session.answerFlow({
      seq: first.seq,
      step: 'answer',
      input: { identifier: 'alice@example.com', password: 'password-1' },
    })
  })
}

async function signOutOf(session: AuthSession): Promise<void> {
  await act(async () => {
    await session.logout()
  })
}

function SelfFocusingChild({ label }: { readonly label: string }) {
  const ref = useRef<HTMLInputElement>(null)
  useEffect(() => {
    ref.current?.focus()
  }, [])
  return <input ref={ref} aria-label={label} />
}

describe('ProductShell view machine', () => {
  it('renders the host sign-in view while anonymous, and never the frame', async () => {
    const rig = makeJourneyRig()
    attachSession(rig.session)
    const utils = renderWithProviders(
      <ProductShell navItems={NAV_ITEMS} header="My App" signIn={<p>sign-in view</p>}>
        <p>app content</p>
      </ProductShell>,
    )
    expect(utils.getByText('sign-in view')).toBeInTheDocument()
    expect(utils.queryByText('app content')).not.toBeInTheDocument()
    expect(utils.queryByRole('banner')).not.toBeInTheDocument()
    expect(utils.queryByRole('main')).not.toBeInTheDocument()
    expect(
      utils.queryByRole('navigation', { name: layoutKitZhCN.appShell.navLabel }),
    ).not.toBeInTheDocument()
    expect(utils.queryByText(authUiZhCN.sessionEnded.title)).not.toBeInTheDocument()
  })

  it('mounts the AppShell frame when the attached session turns authenticated', async () => {
    const rig = makeJourneyRig()
    attachSession(rig.session)
    const utils = renderWithProviders(
      <ProductShell navItems={NAV_ITEMS} header="My App" signIn={<p>sign-in view</p>}>
        <p>app content</p>
      </ProductShell>,
    )
    expect(utils.getByText('sign-in view')).toBeInTheDocument()
    await signInTo(rig.session)
    expect(utils.getByRole('banner')).toBeInTheDocument()
    expect(utils.getByRole('main')).toBeInTheDocument()
    expect(
      utils.getByRole('navigation', { name: layoutKitZhCN.appShell.navLabel }),
    ).toBeInTheDocument()
    expect(utils.getByRole('link', { name: 'Home' })).toHaveAttribute('href', '/home')
    expect(utils.getByRole('link', { name: 'Home' })).toHaveAttribute(
      'aria-current',
      'page',
    )
    expect(utils.getByText('My App')).toBeInTheDocument()
    expect(utils.getByText('app content')).toBeInTheDocument()
    expect(utils.queryByText('sign-in view')).not.toBeInTheDocument()
  })

  it('renders nothing for a fresh visitor when no sign-in view is given', async () => {
    const rig = makeJourneyRig()
    attachSession(rig.session)
    const utils = renderWithProviders(
      <ProductShell navItems={NAV_ITEMS} header="My App">
        <p>app content</p>
      </ProductShell>,
    )
    expect(utils.container).toBeEmptyDOMElement()
    expect(utils.queryByRole('banner')).not.toBeInTheDocument()
  })

  it('shows the session-ended screen after a logout, whose action returns to the sign-in view', async () => {
    const rig = makeJourneyRig()
    attachSession(rig.session)
    const utils = renderWithProviders(
      <ProductShell navItems={NAV_ITEMS} header="My App" signIn={<p>sign-in view</p>}>
        <p>app content</p>
      </ProductShell>,
    )
    await signInTo(rig.session)
    expect(utils.getByText('app content')).toBeInTheDocument()
    await signOutOf(rig.session)
    expect(utils.getByText(authUiZhCN.sessionEnded.title)).toBeInTheDocument()
    expect(utils.getByText(authUiZhCN.sessionEnded.description)).toBeInTheDocument()
    expect(utils.queryByRole('banner')).not.toBeInTheDocument()
    expect(utils.queryByText('sign-in view')).not.toBeInTheDocument()
    fireEvent.click(
      utils.getByRole('button', { name: authUiZhCN.sessionEnded.signInAction }),
    )
    expect(utils.getByText('sign-in view')).toBeInTheDocument()
    expect(utils.queryByText(authUiZhCN.sessionEnded.title)).not.toBeInTheDocument()
  })

  it('renders a host sessionEnded override as-is, and still returns to the frame on the next sign-in', async () => {
    const rig = makeJourneyRig()
    attachSession(rig.session)
    const utils = renderWithProviders(
      <ProductShell
        navItems={NAV_ITEMS}
        header="My App"
        signIn={<p>sign-in view</p>}
        sessionEnded={<p>custom ended view</p>}
      >
        <p>app content</p>
      </ProductShell>,
    )
    await signInTo(rig.session)
    await signOutOf(rig.session)
    expect(utils.getByText('custom ended view')).toBeInTheDocument()
    expect(utils.queryByText(authUiZhCN.sessionEnded.title)).not.toBeInTheDocument()
    expect(utils.queryByText('sign-in view')).not.toBeInTheDocument()
    await signInTo(rig.session)
    expect(utils.getByText('app content')).toBeInTheDocument()
    expect(utils.queryByText('custom ended view')).not.toBeInTheDocument()
  })

  it('fails closed to the sign-in view for an anonymous session and follows an authenticated attach', async () => {
    const anonymousRig = makeJourneyRig()
    act(() => {
      attachSession(anonymousRig.session)
    })
    const utils = renderWithProviders(
      <ProductShell navItems={NAV_ITEMS} header="My App" signIn={<p>sign-in view</p>}>
        <p>app content</p>
      </ProductShell>,
    )
    expect(utils.getByText('sign-in view')).toBeInTheDocument()
    expect(utils.queryByRole('banner')).not.toBeInTheDocument()
    expect(utils.queryByText(authUiZhCN.sessionEnded.title)).not.toBeInTheDocument()
    const rig = makeJourneyRig()
    await signInTo(rig.session)
    act(() => {
      attachSession(rig.session)
    })
    expect(utils.getByText('app content')).toBeInTheDocument()
    expect(utils.queryByText('sign-in view')).not.toBeInTheDocument()
    await signOutOf(rig.session)
    expect(utils.getByText(authUiZhCN.sessionEnded.title)).toBeInTheDocument()
  })

  it('has no axe violations on the authenticated frame', async () => {
    const rig = makeJourneyRig()
    attachSession(rig.session)
    const utils = renderWithProviders(
      <ProductShell
        navItems={NAV_ITEMS}
        header="My App"
        signIn={<p>sign-in view</p>}
        userMenu={<p>account menu</p>}
        headerActions={<p>actions</p>}
      >
        <h1>My app</h1>
        <p>app content</p>
      </ProductShell>,
    )
    await signInTo(rig.session)
    expect(utils.getByRole('main')).toBeInTheDocument()
    await expectNoAxeViolations()
  })

  it('passes the banner slot through to the frame below the app bar', async () => {
    const rig = makeJourneyRig()
    attachSession(rig.session)
    const utils = renderWithProviders(
      <ProductShell
        navItems={NAV_ITEMS}
        banner={<p>impersonation notice</p>}
        signIn={<p>sign-in view</p>}
      >
        <p>app content</p>
      </ProductShell>,
    )
    await signInTo(rig.session)
    const notice = utils.getByText('impersonation notice')
    expect(utils.getByRole('banner').compareDocumentPosition(notice) &
      Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('passes shellLayout through to the frame', async () => {
    const rig = makeJourneyRig()
    attachSession(rig.session)
    const utils = renderWithProviders(
      <ProductShell
        navItems={NAV_ITEMS}
        header="My App"
        shellLayout="sidebar"
        signIn={<p>sign-in view</p>}
      >
        <p>app content</p>
      </ProductShell>,
    )
    await signInTo(rig.session)
    expect(utils.queryByRole('banner')).not.toBeInTheDocument()
    const nav = utils.getByRole('navigation')
    const brand = utils.getByText('My App')
    expect(nav.contains(brand)).toBe(false)
    expect(
      brand.compareDocumentPosition(nav) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('keeps a host without a sign-in view on the ended screen when its action is activated', async () => {
    const rig = makeJourneyRig()
    attachSession(rig.session)
    const utils = renderWithProviders(
      <ProductShell navItems={NAV_ITEMS} header="My App">
        <p>app content</p>
      </ProductShell>,
    )
    await signInTo(rig.session)
    expect(utils.getByText('app content')).toBeInTheDocument()
    await signOutOf(rig.session)
    expect(utils.getByText(authUiZhCN.sessionEnded.title)).toBeInTheDocument()
    fireEvent.click(
      utils.getByRole('button', { name: authUiZhCN.sessionEnded.signInAction }),
    )
    expect(utils.getByText(authUiZhCN.sessionEnded.title)).toBeInTheDocument()
    expect(utils.getByText(authUiZhCN.sessionEnded.description)).toBeInTheDocument()
    expect(utils.queryByRole('banner')).not.toBeInTheDocument()
    expect(utils.container).not.toBeEmptyDOMElement()
    await signInTo(rig.session)
    expect(utils.getByText('app content')).toBeInTheDocument()
  })

  it('leaves focus untouched on the first render', () => {
    const rig = makeJourneyRig()
    attachSession(rig.session)
    expect(document.activeElement).toBe(document.body)
    const utils = renderWithProviders(
      <ProductShell navItems={NAV_ITEMS} header="My App" signIn={<p>sign-in view</p>}>
        <p>app content</p>
      </ProductShell>,
    )
    expect(utils.getByText('sign-in view')).toBeInTheDocument()
    expect(document.activeElement).toBe(document.body)

    const hostControl = document.createElement('button')
    hostControl.textContent = 'host control'
    document.body.append(hostControl)
    try {
      hostControl.focus()
      expect(document.activeElement).toBe(hostControl)
      const second = renderWithProviders(
        <ProductShell navItems={NAV_ITEMS} header="My App" signIn={<p>second sign-in view</p>}>
          <p>app content</p>
        </ProductShell>,
      )
      expect(second.getByText('second sign-in view')).toBeInTheDocument()
      expect(document.activeElement).toBe(hostControl)
    } finally {
      hostControl.remove()
    }
  })

  it('lands focus on the ended branch heading and announces the transition into it', async () => {
    const rig = makeJourneyRig()
    attachSession(rig.session)
    const utils = renderWithProviders(
      <ProductShell navItems={NAV_ITEMS} header="My App" signIn={<p>sign-in view</p>}>
        <p>app content</p>
      </ProductShell>,
    )
    await signInTo(rig.session)
    expect(utils.getByText('app content')).toBeInTheDocument()
    await signOutOf(rig.session)
    expect(utils.getByText(authUiZhCN.sessionEnded.title)).toBeInTheDocument()
    expect(document.activeElement).toBe(
      utils.getByRole('heading', {
        level: 1,
        name: authUiZhCN.sessionEnded.title,
      }),
    )
    const status = utils.getByRole('status')
    expect(status).toHaveTextContent('')
    await waitFor(() =>
      expect(status).toHaveTextContent(
        productShellZhCN.announcements.sessionEnded,
      ),
    )
    await expectNoAxeViolations({ disabledRules: ['region'] })
  })

  it('still lands focus on the ended branch heading when its namespace is not registered, and renders no announcement text', async () => {
    const trio = createI18n({
      supportedLanguages: TEST_LANGUAGES,
      defaultLanguage: 'zh-CN',
      storage: null,
      urlParameterName: null,
      navigatorLanguages: [],
    })
    registerNamespace(trio, UI_KIT_NAMESPACE, uiKitResources)
    registerNamespace(trio, LAYOUT_KIT_NAMESPACE, layoutKitResources)
    registerNamespace(trio, AUTH_UI_NAMESPACE, authUiResources)
    const rig = makeJourneyRig()
    attachSession(rig.session)
    const utils = renderWithProviders(
      <ProductShell navItems={NAV_ITEMS} header="My App" signIn={<p>sign-in view</p>}>
        <p>app content</p>
      </ProductShell>,
      { i18n: trio },
    )
    await signInTo(rig.session)
    await signOutOf(rig.session)
    expect(utils.getByText(authUiZhCN.sessionEnded.title)).toBeInTheDocument()
    expect(document.activeElement).toBe(
      utils.getByRole('heading', {
        level: 1,
        name: authUiZhCN.sessionEnded.title,
      }),
    )
    expect(utils.queryByRole('status')).not.toBeInTheDocument()
    expect(utils.queryByText(productShellZhCN.announcements.sessionEnded)).not.toBeInTheDocument()
  })

  it('lands focus on the branch container when a branch has no page heading to land on', async () => {
    const rig = makeJourneyRig()
    attachSession(rig.session)
    const utils = renderWithProviders(
      <ProductShell navItems={NAV_ITEMS} header="My App" signIn={<p>sign-in view</p>}>
        <p>app content</p>
      </ProductShell>,
    )
    expect(utils.getByText('sign-in view')).toBeInTheDocument()
    await signInTo(rig.session)
    expect(utils.getByText('app content')).toBeInTheDocument()
    expect(document.activeElement).toBe(utils.getByRole('main'))
    await signOutOf(rig.session)
    expect(utils.getByText(authUiZhCN.sessionEnded.title)).toBeInTheDocument()
    fireEvent.click(
      utils.getByRole('button', { name: authUiZhCN.sessionEnded.signInAction }),
    )
    expect(utils.getByText('sign-in view')).toBeInTheDocument()
    expect(document.activeElement).toBe(utils.container.firstElementChild)
    expect(document.activeElement?.textContent).toBe('sign-in view')
    await signInTo(rig.session)
    expect(utils.getByText('app content')).toBeInTheDocument()
    expect(document.activeElement).toBe(utils.getByRole('main'))
    expect(utils.queryByRole('status')).not.toBeInTheDocument()
  })

  it('lands focus on the new page heading when the session authenticates', async () => {
    const rig = makeJourneyRig()
    attachSession(rig.session)
    const utils = renderWithProviders(
      <ProductShell
        navItems={NAV_ITEMS}
        header="My App"
        signIn={<button type="button">sign in</button>}
      >
        <PageLayout title="Workspace home">
          <p>app content</p>
        </PageLayout>
      </ProductShell>,
    )
    utils.getByRole('button', { name: 'sign in' }).focus()
    await signInTo(rig.session)
    expect(utils.getByText('app content')).toBeInTheDocument()
    expect(document.activeElement).toBe(
      utils.getByRole('heading', { level: 1, name: 'Workspace home' }),
    )
  })

  it('lands focus on the session-ended heading when the session ends', async () => {
    const rig = makeJourneyRig()
    attachSession(rig.session)
    const utils = renderWithProviders(
      <ProductShell navItems={NAV_ITEMS} header="My App" signIn={<p>sign-in view</p>}>
        <PageLayout title="Workspace home">
          <p>app content</p>
        </PageLayout>
      </ProductShell>,
    )
    await signInTo(rig.session)
    await signOutOf(rig.session)
    expect(utils.getByText(authUiZhCN.sessionEnded.title)).toBeInTheDocument()
    expect(document.activeElement).toBe(
      utils.getByRole('heading', {
        level: 1,
        name: authUiZhCN.sessionEnded.title,
      }),
    )
  })

  it('leaves focus alone when the new branch already placed it inside itself', async () => {
    const rig = makeJourneyRig()
    attachSession(rig.session)
    const utils = renderWithProviders(
      <ProductShell navItems={NAV_ITEMS} header="My App" signIn={<p>sign-in view</p>}>
        <SelfFocusingChild label="verification code" />
      </ProductShell>,
    )
    await signInTo(rig.session)
    const input = utils.getByRole('textbox', { name: 'verification code' })
    expect(document.activeElement).toBe(input)
  })

  it('passes navLabel, navHeader and navFooter through to the frame', async () => {
    const rig = makeJourneyRig()
    attachSession(rig.session)
    const utils = renderWithProviders(
      <ProductShell
        navItems={NAV_ITEMS}
        header="My App"
        navLabel="Workspace navigation"
        navHeader={<p>drawer header content</p>}
        navFooter={<p>drawer footer content</p>}
        signIn={<p>sign-in view</p>}
      >
        <p>app content</p>
      </ProductShell>,
    )
    await signInTo(rig.session)
    expect(
      utils.queryByRole('navigation', { name: layoutKitZhCN.appShell.navLabel }),
    ).not.toBeInTheDocument()
    expect(
      utils.getByRole('navigation', { name: 'Workspace navigation' }),
    ).toBeInTheDocument()
    expect(utils.getAllByText('drawer header content').length).toBeGreaterThan(0)
    expect(utils.getAllByText('drawer footer content').length).toBeGreaterThan(0)
  })

  it('calls a sessionEnded render function with returnToSignIn, which returns to the sign-in branch', async () => {
    const rig = makeJourneyRig()
    attachSession(rig.session)
    const actions: Array<{ readonly returnToSignIn: () => void }> = []
    const utils = renderWithProviders(
      <ProductShell
        navItems={NAV_ITEMS}
        header="My App"
        signIn={<p>sign-in view</p>}
        sessionEnded={(received) => {
          actions.push(received)
          return (
            <button type="button" onClick={received.returnToSignIn}>
              back to sign-in
            </button>
          )
        }}
      >
        <p>app content</p>
      </ProductShell>,
    )
    await signInTo(rig.session)
    await signOutOf(rig.session)
    expect(
      utils.getByRole('button', { name: 'back to sign-in' }),
    ).toBeInTheDocument()
    expect(utils.queryByText(authUiZhCN.sessionEnded.title)).not.toBeInTheDocument()
    expect(actions.length).toBeGreaterThan(0)
    fireEvent.click(utils.getByRole('button', { name: 'back to sign-in' }))
    expect(utils.getByText('sign-in view')).toBeInTheDocument()
    expect(
      utils.queryByRole('button', { name: 'back to sign-in' }),
    ).not.toBeInTheDocument()
  })

  it('hands a no-op returnToSignIn to the function form when no sign-in view exists', async () => {
    const rig = makeJourneyRig()
    attachSession(rig.session)
    const utils = renderWithProviders(
      <ProductShell
        navItems={NAV_ITEMS}
        header="My App"
        sessionEnded={(actions) => (
          <button type="button" onClick={actions.returnToSignIn}>
            stay here
          </button>
        )}
      >
        <p>app content</p>
      </ProductShell>,
    )
    await signInTo(rig.session)
    await signOutOf(rig.session)
    fireEvent.click(utils.getByRole('button', { name: 'stay here' }))
    expect(utils.getByRole('button', { name: 'stay here' })).toBeInTheDocument()
    expect(utils.container).not.toBeEmptyDOMElement()
    await signInTo(rig.session)
    expect(utils.getByText('app content')).toBeInTheDocument()
  })
})
