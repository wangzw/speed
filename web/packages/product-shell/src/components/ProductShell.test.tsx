/**
 * ProductShell view-machine suite.
 *
 * ProductShell is one decision: which of its three branches renders for
 * the current snapshot (authenticated -> frame, anonymous after the
 * frame -> session-ended view, anonymous before it -> sign-in view).
 * These tests drive real sessions over the real-client rig (see
 * test-utils/real-client.ts) attached to the auth-core hooks exactly the
 * way a host attaches one, and assert what renders and what the machine
 * remembers -- never internal state of the component under test. Every
 * built-in string asserted here comes from the shipped sibling bundles
 * (auth-ui's for the default ended screen, layout-kit's for the frame,
 * product-shell's own for the flip announcement), imported relatively --
 * never inline translations. Slot and content strings are English
 * fixtures on purpose: they stand in for a host's own content and are
 * data in a test file, not rendered product text.
 *
 * The three branches are mutually exclusive by construction, in the
 * same branch order auth-ui's SessionGate pattern uses: the ended view
 * is checked before the sign-in view, so a signed-out user never falls
 * back to a fresh-visitor sign-in.
 */

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

/** The navItems fixture: host data in host order, with the selected
 * item pre-computed (AppShell never path-matches). */
const NAV_ITEMS = [
  { id: 'home', label: 'Home', href: '/home', selected: true },
] as const

/** A rig whose script covers the two happy paths the machine journeys
 * drive -- a token-issuing login (the flow start, its answer, and the
 * profile a login completion reads) and a 204 logout -- and fails loudly
 * on anything else, so an unpinned request fails the test. */
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

/** Drive a password login to completion on a rig session: the flow the
 * session starts, its first answer, and the profile the completion reads.
 * The session operations are called from test code, never from a
 * component, so the state flips they notify are wrapped in act
 * explicitly. */
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

/** A host child that focuses its own control on mount, the way the
 * account page's verification section focuses the code input right
 * after a registration navigate. Its effect runs before ProductShell's
 * flip effect, so the flip sees focus already inside the new branch. */
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
    // The default ended screen is unreachable: the frame was never
    // reached, so the anonymous branch is the sign-in one.
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
    // The frame: chrome landmarks, the host nav item, header content
    // and the app children inside the main landmark -- and the sign-in
    // view unmounted with the anonymous branch.
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
    // Anonymous and never-in-the-app with no signIn slot: the branch
    // renders nothing -- a blank page the host pairs its own sign-in
    // surface with, deliberately (see the package README).
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
    // The frame is gone and the ended screen stands in the sign-in
    // view's place: the machine remembers the app was reached.
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
    // The override renders where the default ended screen would have,
    // and the default is nowhere to be seen.
    expect(utils.getByText('custom ended view')).toBeInTheDocument()
    expect(utils.queryByText(authUiZhCN.sessionEnded.title)).not.toBeInTheDocument()
    expect(utils.queryByText('sign-in view')).not.toBeInTheDocument()
    // The override owns its own way back: signing in again flips the
    // snapshot and the frame returns, machine state untouched.
    await signInTo(rig.session)
    expect(utils.getByText('app content')).toBeInTheDocument()
    expect(utils.queryByText('custom ended view')).not.toBeInTheDocument()
  })

  it('fails closed to the sign-in view for an anonymous session and follows an authenticated attach', async () => {
    // An anonymous binding -- the same snapshot the hooks report before
    // any attach, by auth-core's own fail-closed contract (its suite
    // pins that half; attachSession is module-level and last-bind-wins,
    // so this test starts from a fresh anonymous session rather than
    // whatever an earlier test left attached). Whichever way the
    // session is anonymous, the machine can never reach the frame or
    // the ended screen, whatever the host slots say.
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
    // A session that authenticates while unattached, then attaches:
    // the machine follows the bind immediately (last bind wins), and
    // the ended screen is reachable afterwards like any other session.
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
        {/* The host page's h1 lives in the content inside the frame's
            main landmark, the way every real host composes it: the
            chrome renders no page heading of its own, and
            page-has-heading-one is determinate in jsdom now (see the
            axe helper header). */}
        <h1>My app</h1>
        <p>app content</p>
      </ProductShell>,
    )
    await signInTo(rig.session)
    expect(utils.getByRole('main')).toBeInTheDocument()
    // Page-chrome scan: this helper keeps the axe `region` rule enabled
    // (all content inside a landmark) because the authenticated branch
    // is a full app page, unlike the widget-shaped pre-auth branches.
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
    // The frame renders it below the AppBar, exactly where the AppShell
    // slot places it: ProductShell adds no interpretation of its own.
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
    // The frame is in its sidebar form: no AppBar banner landmark, and
    // the brand renders outside the navigation landmark, ahead of it in
    // document order (the drawer paper's top row).
    expect(utils.queryByRole('banner')).not.toBeInTheDocument()
    const nav = utils.getByRole('navigation')
    const brand = utils.getByText('My App')
    expect(nav.contains(brand)).toBe(false)
    expect(
      brand.compareDocumentPosition(nav) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('keeps a host without a sign-in view on the ended screen when its action is activated', async () => {
    // The no-signIn composition is legal (the README and the suite above
    // pin its blank fresh-visitor branch): such a host pairs its own
    // sign-in surface with that blank. The default ended screen's action
    // promises to "return the viewer to the sign-in view" -- a view that
    // does not exist in this composition -- so the machine must not
    // reset into a branch that would render nothing (a whitescreen dead
    // end); the action keeps the viewer on the ended screen, which stays
    // reachable and still hands the frame back the moment a session
    // authenticates again.
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
    // Not a whitescreen: the viewer stays on a rendered screen -- the
    // ended screen, whose title and action remain in the document.
    expect(utils.getByText(authUiZhCN.sessionEnded.title)).toBeInTheDocument()
    expect(utils.getByText(authUiZhCN.sessionEnded.description)).toBeInTheDocument()
    expect(utils.queryByRole('banner')).not.toBeInTheDocument()
    expect(utils.container).not.toBeEmptyDOMElement()
    // Nor a dead end: the next authentication still brings the frame
    // back, exactly as it would for a fresh visitor.
    await signInTo(rig.session)
    expect(utils.getByText('app content')).toBeInTheDocument()
  })

  it('leaves focus untouched on the first render', () => {
    // A fresh page load's focus belongs to the host: the first commit is
    // not a branch flip, so the landing rule must not claim it. With
    // nothing focused the document's active element stays the body ...
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

    // ... and a host control focused before the mount keeps focus through
    // it, even though the control sits outside the branch the shell
    // mounted (the case the flip hand-off exists for, one commit later).
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
    // The session ends mid-use (an explicit sign-out here; a server-side
    // death is the same snapshot flip, driven by the gated-journey
    // suite): the machine hands focus to the ended branch's page heading
    // -- focus never falls back to the body -- and a
    // role="status" element announces the transition from the
    // product-shell namespace (the family convention, registered by this
    // suite's harness).
    await signOutOf(rig.session)
    expect(utils.getByText(authUiZhCN.sessionEnded.title)).toBeInTheDocument()
    expect(document.activeElement).toBe(
      utils.getByRole('heading', {
        level: 1,
        name: authUiZhCN.sessionEnded.title,
      }),
    )
    const status = utils.getByRole('status')
    // The region is inserted empty and fills on a later frame.
    expect(status).toHaveTextContent('')
    await waitFor(() =>
      expect(status).toHaveTextContent(
        productShellZhCN.announcements.sessionEnded,
      ),
    )
    // The shell-owned parts of the ended branch (the focusable container
    // and the sr-only live region) carry no axe violations of their own;
    // the branch is a whole-page placeholder by design, so the `region`
    // rule (content inside a landmark) is disabled exactly as it is for
    // auth-ui's own scan of the screen.
    await expectNoAxeViolations({ disabledRules: ['region'] })
  })

  it('still lands focus on the ended branch heading when its namespace is not registered, and renders no announcement text', async () => {
    // The machine's announcement renders from the product-shell
    // namespace; a host that has not registered it must get
    // neither raw key text nor missing-key warnings: the flip still
    // lands focus on the page heading, the ended screen renders, and no
    // status element exists. The pre-registration harness below registers
    // exactly the three sibling namespaces every shell host registers
    // anyway.
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
    // Mounting into the sign-in branch never moves focus (a fresh page
    // load keeps the host's own focus); the first flip -- the login --
    // has no level-one heading to land on, so focus falls back to the
    // authenticated frame's main landmark.
    expect(utils.getByText('sign-in view')).toBeInTheDocument()
    await signInTo(rig.session)
    expect(utils.getByText('app content')).toBeInTheDocument()
    expect(document.activeElement).toBe(utils.getByRole('main'))
    // The ended action's reset is a flip too, and the sign-in slot has
    // neither a main landmark nor a heading: focus falls back to the
    // branch's own focusable wrapper rather than the body.
    await signOutOf(rig.session)
    expect(utils.getByText(authUiZhCN.sessionEnded.title)).toBeInTheDocument()
    fireEvent.click(
      utils.getByRole('button', { name: authUiZhCN.sessionEnded.signInAction }),
    )
    expect(utils.getByText('sign-in view')).toBeInTheDocument()
    expect(document.activeElement).toBe(utils.container.firstElementChild)
    expect(document.activeElement?.textContent).toBe('sign-in view')
    // A re-login lands focus back on the frame's main landmark.
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
    // The sign-in control holds focus when the snapshot flips; that
    // button unmounts with the branch, so the hand-off is the shell's
    // to make.
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
    // The child's own effect focuses its input in the same commit as the
    // branch flip; the shell's hand-off must not pull focus away.
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
    // The drawer's accessible name is the host's, not the shipped default
    // -- the reason a host mounting a second shell supplies one.
    expect(
      utils.queryByRole('navigation', { name: layoutKitZhCN.appShell.navLabel }),
    ).not.toBeInTheDocument()
    expect(
      utils.getByRole('navigation', { name: 'Workspace navigation' }),
    ).toBeInTheDocument()
    // Both slots render inside the drawer paper; the permanent drawer is
    // the one mounted at the suite's desktop matchMedia default.
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
    // The function's node replaced the default ended screen, and the
    // action it was handed returns the shell to the sign-in branch.
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
    // The same reset rule the default screen uses: without a sign-in view
    // there is no branch to return to, and resetting would land the
    // viewer on the deliberately blank fresh-visitor page.
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
    // Nor a dead end: the next authentication brings the frame back.
    await signInTo(rig.session)
    expect(utils.getByText('app content')).toBeInTheDocument()
  })
})
