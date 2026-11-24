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

const FLOWS_START = 'POST /api/v1/auth/flows'
const FLOWS_ANSWER = 'POST /api/v1/auth/flows/answer'
const METHODS = 'GET /api/v1/auth/methods'
const PROFILE = 'GET /api/v1/auth/me'
const FEATURES = 'GET /api/v1/features'
const LOGOUT = 'POST /api/v1/auth/logout'
const IDENTIFIER = 'alice@example.com'
const PASSWORD = 'password-1'

const METHODS_BODY = methodList([
  { name: 'password', kind: 'form' },
  { name: 'sms', kind: 'form' },
])

const TENANTS: readonly TenantOption[] = [
  { id: 'tenant-1', name: 'Sunshine Dental' },
  { id: 'tenant-2', name: 'Bright Smile Clinic' },
  { id: 'tenant-3', name: 'Harbor View Orthodontics' },
]

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

    await user.click(screen.getByRole('button', { name: 'Sunshine Dental' }))
    await user.click(
      await screen.findByRole('menuitem', { name: 'Bright Smile Clinic' }),
    )
    expect(
      await screen.findByRole('button', { name: 'Bright Smile Clinic' }),
    ).toBeEnabled()
    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(onSelect).toHaveBeenCalledWith('tenant-2')

    await user.click(
      screen.getByRole('button', { name: authUiZhCN.signOut.label }),
    )
    await screen.findByText(authUiZhCN.sessionEnded.title)
    expect(
      screen.getByText(authUiZhCN.sessionEnded.description),
    ).toBeInTheDocument()
    expect(screen.queryByRole('banner')).not.toBeInTheDocument()
    expect(screen.getByRole('main')).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: authUiZhCN.sessionEnded.signInAction }),
    )
    await screen.findByLabelText(authUiZhCN.passwordSignIn.identifierLabel)

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
