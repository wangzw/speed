/**
 * OrganizationSecurityPage: the organization surface's security and access
 * page.
 *
 * The page carries exactly the two tenant policies, each as a switch with
 * its status, effect and impact. A platform-enforced policy is an enabled
 * switch the tenant cannot turn off; turning a policy off asks through the
 * two-step confirmation; a switch saves on its own and rolls back when the
 * server refuses. A protected tenant renders no switch at all and says why.
 */

import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { OrganizationSecurityPage } from './OrganizationSecurityPage.js'
import {
  OrganizationAdminOperationsProvider,
  type AdminTenantSetSettingInput,
  type AdminTenantSetting,
  type AdminTenantSettings,
} from './organization-operations.js'
import {
  CONFIRM_ARM_LOCKOUT_MS,
  uiKitResources,
} from '@speed/ui-kit'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createAdminOperations,
  makeAdminBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

const TENANT = 'acme'

const MFA: AdminTenantSetting = {
  key: 'tenant.admission.require-mfa',
  title: 'Require multi-factor authentication',
  description: 'Members must sign in with a second factor.',
  type: 'bool',
  control: 'switch',
  danger: 'critical',
  effect: 'immediate',
  tenant_access: 'read-write',
  effective_value: 'false',
  platform_value: 'false',
  tenant_value: 'false',
  locked_by_platform: false,
  has_row: false,
  source: 'default',
}

const SSO: AdminTenantSetting = {
  key: 'tenant.admission.require-sso',
  title: 'Require enterprise single sign-on',
  description: "Members must sign in through the organization's identity provider.",
  type: 'bool',
  control: 'switch',
  danger: 'critical',
  effect: 'immediate',
  tenant_access: 'read-write',
  effective_value: 'false',
  platform_value: 'false',
  tenant_value: 'false',
  locked_by_platform: false,
  has_row: false,
  source: 'default',
}

function envelope(
  settings: readonly AdminTenantSetting[],
  writable = true,
): AdminTenantSettings {
  return {
    settings: [...settings],
    writable,
    read_only_reason: writable ? '' : 'protected',
  }
}

function renderPage({
  settings = envelope([MFA, SSO]),
  writeTenantSetting,
  language = 'zh-CN',
}: {
  readonly settings?: AdminTenantSettings
  readonly writeTenantSetting?: (
    input: AdminTenantSetSettingInput,
  ) => Promise<void>
  readonly language?: string
} = {}) {
  const operations = createAdminOperations(
    makeAdminBackend({
      listTenantSettings: () => Promise.resolve(settings),
      ...(writeTenantSetting === undefined ? {} : { writeTenantSetting }),
    }),
  )
  return renderWithProviders(
    <OrganizationAdminOperationsProvider operations={operations}>
      <OrganizationSecurityPage tenant={TENANT} />
    </OrganizationAdminOperationsProvider>,
    { language },
  )
}

describe('OrganizationSecurityPage', () => {
  it('renders the two sign-in policies and no key/value table', async () => {
    renderPage()

    expect(
      await screen.findByRole('heading', {
        level: 3,
        name: MFA.title,
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('heading', { level: 3, name: SSO.title }),
    ).toBeInTheDocument()
    expect(screen.getAllByRole('switch')).toHaveLength(2)
    expect(screen.queryByRole('table')).toBeNull()
    expect(
      screen.queryByRole('navigation', { name: zhCN.settings.nav.label }),
    ).toBeNull()
    expect(
      screen.queryByRole('button', { name: zhCN.settings.saveBar.save }),
    ).toBeNull()
    // The internal keys never reach the page.
    expect(screen.queryByText(MFA.key)).toBeNull()
    expect(screen.queryByText(SSO.key)).toBeNull()
    expect(
      screen.getByText(zhCN.organization.security.auditNote),
    ).toBeInTheDocument()
  })

  it('draws a platform-enforced policy as an enabled switch the tenant cannot move', async () => {
    renderPage({
      settings: envelope([
        {
          ...MFA,
          effective_value: 'true',
          platform_value: 'true',
          locked_by_platform: true,
          source: 'global',
        },
        SSO,
      ]),
    })

    const mfaSwitch = await screen.findByRole('switch', {
      name: zhCN.organization.security.switchAria.replace(
        '{{title}}',
        MFA.title,
      ),
    })
    expect(mfaSwitch).toBeChecked()
    expect(mfaSwitch).toBeDisabled()
    expect(
      screen.getByText(zhCN.organization.security.platformLocked),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        zhCN.organization.security.status.platformEnforced,
      ),
    ).toBeInTheDocument()

    // A platform-enforced policy has no confirmation entry: the switch is
    // inert, so no activation can open the dialog.
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('asks for the two-step confirmation before turning a policy off', async () => {
    const user = userEvent.setup()
    const writes: unknown[] = []
    renderPage({
      settings: envelope([
        {
          ...MFA,
          effective_value: 'true',
          tenant_value: 'true',
          has_row: true,
          source: 'tenant',
        },
      ]),
      writeTenantSetting: (input) => {
        writes.push(input)
        return Promise.resolve()
      },
    })

    const mfaSwitch = await screen.findByRole('switch', {
      name: zhCN.organization.security.switchAria.replace(
        '{{title}}',
        MFA.title,
      ),
    })
    expect(mfaSwitch).toBeChecked()
    await user.click(mfaSwitch)

    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent(zhCN.organization.security.confirm.title.replace('{{title}}', MFA.title))
    expect(writes).toHaveLength(0)

    // The first click only arms the guard.
    await user.click(
      screen.getByRole('button', { name: zhCN.common.confirm }),
    )
    expect(writes).toHaveLength(0)
    await act(async () => {
      await new Promise((resolve) =>
        setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
      )
    })
    const confirmAgain = (
      uiKitResources['zh-CN'] as unknown as {
        readonly confirmDialog: { readonly confirmAgainLabel: string }
      }
    ).confirmDialog.confirmAgainLabel
    await user.click(
      screen.getByRole('button', { name: confirmAgain }),
    )

    await waitFor(() => expect(writes).toHaveLength(1))
    expect(writes[0]).toEqual({
      tenant: TENANT,
      namespace: 'tenant.admission',
      key: 'require-mfa',
      value: 'false',
    })
    expect(
      await screen.findByText(zhCN.organization.security.saved),
    ).toBeInTheDocument()
    expect(mfaSwitch).not.toBeChecked()
  })

  it('turns a policy off without a dialog when it declares no danger', async () => {
    const user = userEvent.setup()
    const writes: unknown[] = []
    renderPage({
      settings: envelope([
        {
          ...MFA,
          danger: '',
          effective_value: 'true',
          tenant_value: 'true',
          has_row: true,
          source: 'tenant',
        },
      ]),
      writeTenantSetting: (input) => {
        writes.push(input)
        return Promise.resolve()
      },
    })

    const mfaSwitch = await screen.findByRole('switch', {
      name: zhCN.organization.security.switchAria.replace(
        '{{title}}',
        MFA.title,
      ),
    })
    expect(mfaSwitch).toBeChecked()
    await user.click(mfaSwitch)

    // No danger means no confirmation: the switch saves on its own.
    expect(screen.queryByRole('dialog')).toBeNull()
    await waitFor(() => expect(writes).toHaveLength(1))
    expect(writes[0]).toEqual({
      tenant: TENANT,
      namespace: 'tenant.admission',
      key: 'require-mfa',
      value: 'false',
    })
  })

  // Forward-compatible: the server's declaration validation admits only the
  // empty value and "critical" today, so this value is not a reachable
  // declaration. The case pins what the page does if that closed set grows: a
  // danger that is not "critical" gets the ordinary single-step confirmation,
  // not the two-step guard and not a silent save.
  it('asks once for a danger outside the current closed set (forward-compatible)', async () => {
    const user = userEvent.setup()
    const writes: unknown[] = []
    renderPage({
      settings: envelope([
        {
          ...MFA,
          danger: 'warning',
          effective_value: 'true',
          tenant_value: 'true',
          has_row: true,
          source: 'tenant',
        },
      ]),
      writeTenantSetting: (input) => {
        writes.push(input)
        return Promise.resolve()
      },
    })

    const mfaSwitch = await screen.findByRole('switch', {
      name: zhCN.organization.security.switchAria.replace(
        '{{title}}',
        MFA.title,
      ),
    })
    await user.click(mfaSwitch)

    // A declared danger that is not "critical" would get the ordinary
    // single-step confirmation, not the two-step guard. This server declares
    // none today; the assertion is the page's forward-compatible branch.
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
    expect(writes).toHaveLength(0)
    await user.click(
      screen.getByRole('button', { name: zhCN.common.confirm }),
    )
    await waitFor(() => expect(writes).toHaveLength(1))
  })

  it('saves optimistically, marks the request busy, and rolls back on refusal', async () => {
    const user = userEvent.setup()
    let rejectWrite: (error: unknown) => void = () => undefined
    const pending = new Promise<void>((_resolve, reject) => {
      rejectWrite = reject
    })
    renderPage({
      settings: envelope([MFA]),
      writeTenantSetting: () => pending,
    })

    const mfaSwitch = await screen.findByRole('switch', {
      name: zhCN.organization.security.switchAria.replace(
        '{{title}}',
        MFA.title,
      ),
    })
    expect(mfaSwitch).not.toBeChecked()
    await user.click(mfaSwitch)

    // The switch follows the click before the server answers, and the
    // request marks the control busy and inert.
    expect(mfaSwitch).toBeChecked()
    expect(mfaSwitch).toHaveAttribute('aria-busy', 'true')
    expect(mfaSwitch).toBeDisabled()

    await act(async () => {
      rejectWrite({ code: 'settings.tenant_admission_enforced' })
    })

    await waitFor(() => expect(mfaSwitch).not.toBeChecked())
    expect(mfaSwitch).not.toHaveAttribute('aria-busy')
    expect(mfaSwitch).toBeEnabled()
    expect(screen.getByRole('alert')).toHaveTextContent(
      zhCN.errors.settings.tenant_admission_enforced,
    )
  })

  it('keeps an in-flight row disabled while another row saves', async () => {
    const user = userEvent.setup()
    const resolvers = new Map<string, () => void>()
    renderPage({
      settings: envelope([MFA, SSO]),
      writeTenantSetting: (input) =>
        new Promise<void>((resolve) => {
          resolvers.set(`${input.namespace}.${input.key}`, resolve)
        }),
    })

    const mfaSwitch = await screen.findByRole('switch', {
      name: zhCN.organization.security.switchAria.replace(
        '{{title}}',
        MFA.title,
      ),
    })
    const ssoSwitch = screen.getByRole('switch', {
      name: zhCN.organization.security.switchAria.replace(
        '{{title}}',
        SSO.title,
      ),
    })

    await user.click(mfaSwitch)
    expect(mfaSwitch).toBeDisabled()
    expect(mfaSwitch).toHaveAttribute('aria-busy', 'true')

    // The second row saves on its own while the first write is still in
    // flight.
    await user.click(ssoSwitch)
    expect(ssoSwitch).toBeDisabled()
    expect(ssoSwitch).toHaveAttribute('aria-busy', 'true')
    // The first row stays inert: its request has not settled, and accepting
    // another click would put two writes of one key in flight.
    expect(mfaSwitch).toBeDisabled()
    expect(mfaSwitch).toHaveAttribute('aria-busy', 'true')

    await act(async () => {
      resolvers.get('tenant.admission.require-mfa')?.()
      resolvers.get('tenant.admission.require-sso')?.()
    })
    await waitFor(() => expect(mfaSwitch).toBeEnabled())
    expect(ssoSwitch).toBeEnabled()
  })

  it('renders a protected tenant read-only with no switches', async () => {
    const user = userEvent.setup()
    renderPage({ settings: envelope([MFA, SSO], false) })

    expect(
      await screen.findByText(zhCN.organization.security.readOnly.title),
    ).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.organization.security.readOnly.description),
    ).toBeInTheDocument()
    expect(screen.queryAllByRole('switch')).toHaveLength(0)
    // The rows and the audit note still render.
    expect(
      screen.getByRole('heading', { level: 3, name: MFA.title }),
    ).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.organization.security.auditNote),
    ).toBeInTheDocument()
    // Reading is still allowed, so refresh stays available.
    const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
    expect(refresh).toBeEnabled()
    await user.click(refresh)
    expect(refresh).toBeInTheDocument()
  })

  it('falls back to the bundled policy copy when the server omits it', async () => {
    renderPage({
      settings: envelope([{ ...MFA, title: '', description: '' }]),
    })

    expect(
      await screen.findByRole('heading', {
        level: 3,
        name: zhCN.organization.security.mfa.title,
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.organization.security.mfa.description),
    ).toBeInTheDocument()
  })

  it('speaks English when the host language is en-US', async () => {
    renderPage({ language: 'en-US' })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: enUS.organization.nav.settings,
      }),
    ).toBeInTheDocument()
    expect(
      await screen.findByRole('heading', {
        level: 2,
        name: enUS.organization.security.section.title,
      }),
    ).toBeInTheDocument()
    expect(
      screen.getAllByText(
        new RegExp(enUS.organization.security.field.effect),
      ),
    ).toHaveLength(2)
    expect(
      screen.getAllByText(enUS.organization.security.status.disabled),
    ).toHaveLength(2)
    expect(
      screen.getByText(enUS.organization.security.auditNote),
    ).toBeInTheDocument()
  })

  it('has no axe violations', async () => {
    renderPage()
    await screen.findAllByRole('switch')
    await expectNoAxeViolations()
  })
})
