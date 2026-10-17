/**
 * The README usage example, compiled and executed by the suite.
 *
 * The README's Quick start composes the whole tenant-facing shell: the
 * namespaces registered once on one i18n instance, one session attached
 * before render, and a ProductShell whose signIn slot is auth-ui's
 * SignInScreen, whose userMenu slot carries the tenant switcher
 * (@speed/tenancy-ui, the README's "multi-tenant userMenu" section)
 * beside auth-ui's SignOutButton, and whose frame wraps the host's own
 * app content. This file renders that exact composition and drives one
 * minimal journey through it -- a password sign-in, the frame appearing,
 * a tenant switch through the userMenu (the tenant is the host's own
 * state, mounted on auth-core's TenantProvider; selecting a row reports
 * the pick to the host, which switches the tenant it renders), a sign-out
 * through the same menu, the default session-ended screen, and a return
 * to the sign-in view -- over the real-client rig (a genuine
 * @speed/api-client bound through the same bindRequestFn seam a host's
 * real client binds), pinning every request in order so the documented
 * usage cannot drift from the API. This example stays ungated by design;
 * the host's permission-gating composition over the same frame is the
 * gated-journey suite's (gated-journey.test.tsx). Host-content strings
 * (nav labels, the app title and content, tenant names) are English
 * fixtures on purpose: they stand in for a host's own content and are
 * data in a test file (exempt from the no-literal-text rule), not
 * rendered product text. Assertions derive every built-in string from
 * the shipped sibling locale bundles, never inline translations.
 */

import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import { Box } from '@mui/material'
import { createI18n, registerNamespace } from '@speed/i18n'
import { UI_KIT_NAMESPACE, uiKitResources } from '@speed/ui-kit'
import {
  LAYOUT_KIT_NAMESPACE,
  layoutKitResources,
} from '@speed/layout-kit'
import {
  AUTH_UI_NAMESPACE,
  authUiResources,
  SignInScreen,
  SignOutButton,
} from '@speed/auth-ui'
import {
  TENANCY_UI_NAMESPACE,
  tenancyUiResources,
  TenantSwitcher,
} from '@speed/tenancy-ui'
import type { TenantOption } from '@speed/tenancy-ui'
import {
  attachSession,
  TenantProvider,
  useCurrentTenant,
} from '@speed/auth-core'
import authUiZhCN from '../../auth-ui/src/locales/zh-CN.json' with { type: 'json' }
import layoutKitZhCN from '../../layout-kit/src/locales/zh-CN.json' with { type: 'json' }
import {
  PRODUCT_SHELL_NAMESPACE,
  productShellResources,
} from './resources.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  authenticatedCompletion,
  jsonResponse,
  makeRealClientRig,
  methodList,
  openFlow,
  principal,
} from '../test-utils/real-client.js'
import { ProductShell } from './components/ProductShell.js'

const FLOWS_START = 'POST /auth/flows'
const FLOWS_ANSWER = 'POST /auth/flows/answer'
const METHODS = 'GET /auth/methods'
const PROFILE = 'GET /auth/me'
const FEATURES = 'GET /features'
const LOGOUT = 'POST /auth/logout'
const IDENTIFIER = 'alice@example.com'
const PASSWORD = 'password-1'

/** The deployment's method document: both form channels, so the surface
 * renders the tab strip the quick start documents. The sign-in surface
 * reads it on mount. */
const METHODS_BODY = methodList([
  { name: 'password', kind: 'form' },
  { name: 'sms', kind: 'form' },
])

/** The host's tenant roster -- host data, the same fixture the tenancy-ui
 * README demo uses. The switcher renders these names as host content. */
const TENANTS: readonly TenantOption[] = [
  { id: 'tenant-1', name: 'Sunshine Dental' },
  { id: 'tenant-2', name: 'Bright Smile Clinic' },
  { id: 'tenant-3', name: 'Harbor View Orthodontics' },
]

// The rig of the quick start: one session over one real client, whose
// script covers exactly the requests the journey makes -- a password
// login (the flow start carrying the credentials, its completion answer,
// and the /me profile a login completion reads), plus a 204 logout -- and
// fails loudly on anything else, so an unpinned request fails the test.
// A password login settles in the flow's start (the server validates the
// credentials the start carried and answers a ready step), so the answer
// the driver sends is the step:'complete' that takes the tokens.
const rig = makeRealClientRig((call) => {
  const key = `${call.method} ${call.path}`
  if (key === METHODS) {
    return jsonResponse(200, METHODS_BODY)
  }
  if (key === FEATURES) {
    return jsonResponse(200, { features: [] })
  }
  if (key === FLOWS_START) {
    return jsonResponse(200, openFlow({ state: 'ready', next: { kind: 'complete' } }))
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

// The quick start's bootstrap: the namespaces the composed views render
// under -- the host's shell quartet (ui-kit, layout-kit, auth-ui and
// product-shell's own, whose session-ended announcement the machine
// reads) plus tenancy-ui's, whose switcher sits in the userMenu --
// registered exactly once on the one instance (double registration
// throws), and the session attached before any render. Module scope
// matches the README; the single journey below is the only consumer of
// this instance, so nothing double-registers.
const i18n = createI18n({
  supportedLanguages: ['zh-CN', 'en-US'],
  defaultLanguage: 'zh-CN',
  storage: null,
  urlParameterName: null,
  navigatorLanguages: [],
})
registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)
registerNamespace(i18n, LAYOUT_KIT_NAMESPACE, layoutKitResources)
registerNamespace(i18n, AUTH_UI_NAMESPACE, authUiResources)
registerNamespace(i18n, PRODUCT_SHELL_NAMESPACE, productShellResources)
registerNamespace(i18n, TENANCY_UI_NAMESPACE, tenancyUiResources)
attachSession(rig.session)

/** The README multi-tenant section's userMenu: the host's own menu bar
 * (host content) -- the current tenant read through auth-core's
 * useCurrentTenant feeds tenancy-ui's switcher, with auth-ui's sign-out
 * button beside it. The switcher never knows a session and never
 * navigates: selecting a row reports the pick to the host. */
function UserMenu({ onSelect }: { readonly onSelect: (id: string) => void }) {
  const current = useCurrentTenant()
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
      <TenantSwitcher
        tenants={TENANTS}
        currentTenantId={current?.tenantId ?? null}
        onSelect={onSelect}
      />
      <SignOutButton session={rig.session} />
    </Box>
  )
}

/** The README's example tenant app: the host holds the current tenant
 * (its route stand-in here), mounts it on auth-core's TenantProvider so
 * the switcher's currentTenantId flows from useCurrentTenant, and treats
 * a pick as the moment to switch that state. */
function TenantApp({ onSelect }: { readonly onSelect: (id: string) => void }) {
  const [tenantId, setTenantId] = useState<string>('tenant-1')
  return (
    <TenantProvider tenantId={tenantId}>
      <ProductShell
        navItems={[{ id: 'home', label: 'Home', href: '/', selected: true }]}
        header="My App"
        signIn={<SignInScreen session={rig.session} />}
        userMenu={
          <UserMenu
            onSelect={(id) => {
              onSelect(id)
              setTenantId(id)
            }}
          />
        }
      >
        <p>App content</p>
      </ProductShell>
    </TenantProvider>
  )
}

describe('README usage example', () => {
  it('drives the documented journey: sign in, frame, tenant switch, sign out, session ended, sign in again', async () => {
    const user = userEvent.setup()
    const onSelect = vi.fn<(tenantId: string) => void>()
    renderWithProviders(<TenantApp onSelect={onSelect} />, { i18n })

    // The anonymous start: the host's sign-in surface, password channel
    // first by default -- and no app frame anywhere.
    const identifier = await screen.findByLabelText(
      authUiZhCN.passwordSignIn.identifierLabel,
    )
    expect(
      screen.getByRole('tab', { name: authUiZhCN.passwordSignIn.title }),
    ).toBeInTheDocument()
    expect(screen.queryByRole('banner')).not.toBeInTheDocument()
    expect(screen.queryByRole('main')).not.toBeInTheDocument()
    await user.type(identifier, IDENTIFIER)
    await user.type(
      screen.getByLabelText(authUiZhCN.passwordSignIn.passwordLabel),
      PASSWORD,
    )
    await user.click(
      screen.getByRole('button', { name: authUiZhCN.passwordSignIn.submit }),
    )

    // The sign-in committed: the frame appears around the app content,
    // with the host's chrome and the userMenu controls -- the tenant
    // switcher showing the host's current tenant, and the sign-out
    // button.
    const main = await screen.findByRole('main')
    expect(main).toBeInTheDocument()
    expect(screen.getByRole('banner')).toBeInTheDocument()
    expect(
      screen.getByRole('navigation', {
        name: layoutKitZhCN.appShell.navLabel,
      }),
    ).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Home' })).toHaveAttribute(
      'href',
      '/',
    )
    expect(screen.getByText('My App')).toBeInTheDocument()
    expect(screen.getByText('App content')).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Sunshine Dental' }),
    ).toBeEnabled()
    expect(
      screen.queryByLabelText(authUiZhCN.passwordSignIn.identifierLabel),
    ).not.toBeInTheDocument()

    // The multi-tenant turn: the userMenu's switcher shows the host's
    // roster, and selecting Bright Smile Clinic reports the pick to the
    // host exactly once, which switches the tenant it renders -- the
    // TenantProvider context flips and the trigger relabels itself. The
    // switcher sends no request of its own, so no switch leg appears.
    await user.click(screen.getByRole('button', { name: 'Sunshine Dental' }))
    await user.click(
      await screen.findByRole('menuitem', { name: 'Bright Smile Clinic' }),
    )
    expect(
      await screen.findByRole('button', { name: 'Bright Smile Clinic' }),
    ).toBeEnabled()
    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(onSelect).toHaveBeenCalledWith('tenant-2')

    // Sign out through the userMenu slot: the frame is gone and the
    // default session-ended screen stands in its place (the machine
    // remembers the app was reached).
    await user.click(
      screen.getByRole('button', { name: authUiZhCN.signOut.label }),
    )
    await screen.findByText(authUiZhCN.sessionEnded.title)
    expect(
      screen.getByText(authUiZhCN.sessionEnded.description),
    ).toBeInTheDocument()
    expect(screen.queryByRole('banner')).not.toBeInTheDocument()
    // The frame is gone (banner above), but the ended screen is the
    // whole page now: its container IS the page's main landmark
    // (ProductShell renders the ended branch as `main` -- the
    // landmark-one-main rule a page must answer), with the frame's own
    // main unmounted.
    expect(screen.getByRole('main')).toBeInTheDocument()

    // The ended screen's action returns to the host's sign-in view.
    await user.click(
      screen.getByRole('button', { name: authUiZhCN.sessionEnded.signInAction }),
    )
    await screen.findByLabelText(authUiZhCN.passwordSignIn.identifierLabel)

    // Every request the journey made, pinned in order: the first sign-in
    // surface's method read, the login's flow start, completion answer and
    // the profile the completion read, the logout, and the ended screen's
    // method read. The tenant switch is the host's own state change and
    // sends nothing.
    expect(rig.calls.map((call) => `${call.method} ${call.path}`)).toEqual([
      METHODS,
      FLOWS_START,
      FLOWS_ANSWER,
      PROFILE,
      FEATURES,
      LOGOUT,
      METHODS,
    ])
  })
})
