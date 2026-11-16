/**
 * OrganizationSecurityPage in a real browser.
 *
 * The page is a flexible row per policy: the identity block on the left and
 * the switch and its feedback on the right, stacked below the sm breakpoint.
 * A browser is the only tier that resolves the actual text metrics and the
 * layout, so this suite holds the page inside the sm/md/lg floor widths, proves
 * the theme's coarse-pointer touch floor reaches the switch, and renders the
 * whole page in the dark theme. The read-only state drops the switch entirely
 * and renders the standing notice; that shape is measured at the narrowest
 * floor too.
 *
 * The touch floor is a `@media (pointer: coarse)` rule, which a headless
 * browser's fine pointer never activates; `hasCoarsePointerTouchTarget` reads
 * the rule emotion emitted for the rendered element, the same proof the
 * theme's own suite uses. The rendered box is then asserted to have area, so a
 * rule that matched an element the page no longer paints cannot pass.
 */
import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { useTheme } from '@mui/material/styles'
import { page } from 'vitest/browser'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import { OrganizationSecurityPage } from './OrganizationSecurityPage.js'
import {
  OrganizationAdminOperationsProvider,
  type AdminTenantSetting,
  type AdminTenantSettings,
} from './organization-operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createAdminOperations,
  makeAdminBackend,
} from '../test-utils/operations.js'
import enUS from './locales/en-US.json' with { type: 'json' }

const FLOOR_WIDTHS = [600, 900, 1200] as const
const VIEWPORT_HEIGHT = 900
const TENANT = 'acme'

const MFA: AdminTenantSetting = {
  key: 'tenant.admission.require-mfa',
  title: enUS.organization.security.mfa.title,
  description: enUS.organization.security.mfa.description,
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
  title: enUS.organization.security.sso.title,
  description: enUS.organization.security.sso.description,
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
  defaultMode,
}: {
  readonly settings?: AdminTenantSettings
  readonly defaultMode?: 'light' | 'dark' | 'system'
} = {}): void {
  const operations = createAdminOperations(
    makeAdminBackend({
      listTenantSettings: () => Promise.resolve(settings),
    }),
  )
  renderWithProviders(
    <>
      <ModeProbe />
      <OrganizationAdminOperationsProvider operations={operations}>
        <OrganizationSecurityPage tenant={TENANT} />
      </OrganizationAdminOperationsProvider>
    </>,
    { operations, language: 'en-US', defaultMode },
  )
}

/**
 * The theme's own mode and body surface, so the dark case compares the page
 * against the theme it was given rather than against a color written here.
 */
function ModeProbe() {
  const theme = useTheme()
  return (
    <div
      data-testid="mode-probe"
      data-mode={theme.palette.mode}
      style={{ backgroundColor: theme.palette.background.default }}
    />
  )
}

/** Wait for the page's read to settle into its two policy rows. */
async function ready(): Promise<HTMLElement> {
  await screen.findByRole('heading', {
    level: 2,
    name: enUS.organization.security.section.title,
  })
  await document.fonts.ready
  return screen.getByRole('region', {
    name: enUS.organization.security.section.title,
  })
}

/** Assert the document and one block keep their content inside their box. */
function expectInsideBox(block: HTMLElement): void {
  expect(
    document.documentElement.scrollWidth,
    'the page overflows the viewport horizontally',
  ).toBeLessThanOrEqual(document.documentElement.clientWidth + 1)
  expect(
    block.scrollWidth,
    'the policy section overflows its own width',
  ).toBeLessThanOrEqual(block.clientWidth + 1)
}

describe('OrganizationSecurityPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the policy rows inside a ${width}px viewport`, async () => {
      await page.viewport(width, VIEWPORT_HEIGHT)
      renderPage()
      const section = await ready()
      expectInsideBox(section)
    })
  }

  it('keeps the read-only notice inside a 600px viewport', async () => {
    await page.viewport(600, VIEWPORT_HEIGHT)
    renderPage({ settings: envelope([MFA, SSO], false) })
    const section = await ready()
    expect(
      await screen.findByText(
        enUS.organization.security.readOnly.protected.title,
        { exact: true },
      ),
    ).toBeVisible()
    expect(screen.queryAllByRole('switch')).toHaveLength(0)
    expectInsideBox(section)
  })
})

describe('OrganizationSecurityPage touch targets in Chromium', () => {
  it('carries the coarse-pointer floor on the switch', async () => {
    await page.viewport(900, VIEWPORT_HEIGHT)
    renderPage()
    await ready()

    const switchInputs = screen.getAllByRole('switch')
    expect(switchInputs).toHaveLength(2)

    for (const switchInput of switchInputs) {
      const switchRoot = switchInput.closest('.MuiSwitch-root')
      expect(switchRoot, 'the switch renders its MuiSwitch root').not.toBeNull()

      const control = switchRoot as Element
      expect(
        hasCoarsePointerTouchTarget(control),
        'the control lacks the theme 44px coarse-pointer floor',
      ).toBe(true)
      const box = control.getBoundingClientRect()
      expect(box.width).toBeGreaterThan(0)
      expect(box.height).toBeGreaterThan(0)
    }
  })
})

describe('OrganizationSecurityPage dark theme in Chromium', () => {
  it('renders the policies on the dark theme surface', async () => {
    await page.viewport(1200, VIEWPORT_HEIGHT)
    renderPage({ defaultMode: 'dark' })
    const section = await ready()

    const probe = screen.getByTestId('mode-probe')
    await waitFor(() => expect(probe.dataset.mode).toBe('dark'))
    expect(
      getComputedStyle(document.body).backgroundColor,
      'the dark theme background is not painted onto the document',
    ).toBe(getComputedStyle(probe).backgroundColor)
    expectInsideBox(section)
  })
})
