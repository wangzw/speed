/**
 * TenantProfilePage in a real browser.
 *
 * The page stacks the identity card and the four-field form; the form is one
 * column below `sm` and pairs the short fields above it. A browser is the
 * only tier that resolves the actual layout, so this suite holds the page
 * inside the floor widths, proves the theme's coarse-pointer touch floor
 * reaches the save and ownership controls, and renders the page in both the
 * light and the dark theme.
 */

import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { useTheme } from '@mui/material/styles'
import { page } from 'vitest/browser'
import { attachSession } from '@speed/auth-core'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import { TenantProfilePage } from './TenantProfilePage.js'
import { OrganizationAdminOperationsProvider } from './organization-operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createAdminOperations,
  makeAdminBackend,
  profileFixture,
} from '../test-utils/operations.js'
import {
  ANONYMOUS_SNAPSHOT,
  AUTHENTICATED_SNAPSHOT,
  createStepUpSession,
} from '../test-utils/session.js'
import enUS from './locales/en-US.json' with { type: 'json' }

const FLOOR_WIDTHS = [600, 900, 1200] as const
const VIEWPORT_HEIGHT = 900
const TENANT = 'acme'

function renderPage({
  defaultMode,
  owner = false,
}: {
  readonly defaultMode?: 'light' | 'dark' | 'system'
  /** Attach the session whose account is the fixture's owner. */
  readonly owner?: boolean
} = {}): void {
  attachSession(
    createStepUpSession(
      owner
        ? {
            ...AUTHENTICATED_SNAPSHOT,
            principal: {
              ...AUTHENTICATED_SNAPSHOT.principal!,
              account_id: 'acc-1',
            },
          }
        : ANONYMOUS_SNAPSHOT,
    ),
  )
  const operations = createAdminOperations(
    makeAdminBackend({
      getTenantProfile: () =>
        Promise.resolve(
          profileFixture({
            name: 'acme-inc',
            displayName: 'Acme Incorporated',
            description: 'A canned organization the fixture answers with.',
            contactEmail: 'ops@acme.example',
            website: 'https://acme.example',
          }),
        ),
    }),
  )
  renderWithProviders(
    <>
      <ModeProbe />
      <OrganizationAdminOperationsProvider operations={operations}>
        <TenantProfilePage tenant={TENANT} />
      </OrganizationAdminOperationsProvider>
    </>,
    { operations, language: 'en-US', defaultMode },
  )
}

/** The theme's own mode and body surface, so each case compares the page
 * against the theme it was given. */
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

/** Wait for the page's read to settle into its two cards. */
async function ready(): Promise<HTMLElement> {
  await screen.findByRole('heading', {
    level: 1,
    name: enUS.organization.nav.profile,
    exact: true,
  })
  await screen.findByRole('heading', {
    level: 2,
    name: enUS.tenantProfile.form.title,
    exact: true,
  })
  await document.fonts.ready
  return screen.getByRole('region', {
    name: enUS.tenantProfile.form.title,
    exact: true,
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
    'the profile section overflows its own width',
  ).toBeLessThanOrEqual(block.clientWidth + 1)
}

describe('TenantProfilePage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the profile card inside a ${width}px viewport`, async () => {
      await page.viewport(width, VIEWPORT_HEIGHT)
      renderPage()
      const section = await ready()
      expectInsideBox(section)
    })
  }
})

describe('TenantProfilePage touch targets in Chromium', () => {
  it('carries the coarse-pointer floor on the save control', async () => {
    await page.viewport(900, VIEWPORT_HEIGHT)
    renderPage()
    await ready()

    const save = screen.getByRole('button', {
      name: enUS.tenantProfile.form.save,
      exact: true,
    })
    expect(
      hasCoarsePointerTouchTarget(save),
      'the control lacks the theme 44px coarse-pointer floor',
    ).toBe(true)
    const box = save.getBoundingClientRect()
    expect(box.width).toBeGreaterThan(0)
    expect(box.height).toBeGreaterThan(0)
  })
})

describe('TenantProfilePage themes in Chromium', () => {
  for (const mode of ['light', 'dark'] as const) {
    it(`renders the profile on the ${mode} theme surface`, async () => {
      await page.viewport(1200, VIEWPORT_HEIGHT)
      renderPage({ defaultMode: mode })
      const section = await ready()

      const probe = screen.getByTestId('mode-probe')
      await waitFor(() => expect(probe.dataset.mode).toBe(mode))
      expect(
        getComputedStyle(document.body).backgroundColor,
        `the ${mode} theme background is not painted onto the document`,
      ).toBe(getComputedStyle(probe).backgroundColor)
      expectInsideBox(section)
    })
  }
})

describe('TenantProfilePage ownership controls in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the ownership section inside a ${width}px viewport`, async () => {
      await page.viewport(width, VIEWPORT_HEIGHT)
      renderPage({ owner: true })
      await ready()
      const section = screen.getByRole('region', {
        name: enUS.tenantProfile.ownership.title,
        exact: true,
      })
      expect(section).toBeVisible()
      expectInsideBox(section)
    })
  }

  it('carries the coarse-pointer floor on the transfer and delete controls', async () => {
    await page.viewport(900, VIEWPORT_HEIGHT)
    renderPage({ owner: true })
    await ready()

    for (const control of [
      screen.getByRole('button', {
        name: enUS.tenantProfile.ownership.transfer.button,
        exact: true,
      }),
      screen.getByRole('button', {
        name: enUS.tenantProfile.ownership.delete.button,
        exact: true,
      }),
    ]) {
      expect(
        hasCoarsePointerTouchTarget(control),
        'the control lacks the theme 44px coarse-pointer floor',
      ).toBe(true)
      const box = control.getBoundingClientRect()
      expect(box.width).toBeGreaterThan(0)
      expect(box.height).toBeGreaterThan(0)
    }
  })

  for (const mode of ['light', 'dark'] as const) {
    it(`renders the ownership controls on the ${mode} theme surface`, async () => {
      await page.viewport(1200, VIEWPORT_HEIGHT)
      renderPage({ owner: true, defaultMode: mode })
      await ready()

      const probe = screen.getByTestId('mode-probe')
      await waitFor(() => expect(probe.dataset.mode).toBe(mode))
      expect(
        getComputedStyle(document.body).backgroundColor,
        `the ${mode} theme background is not painted onto the document`,
      ).toBe(getComputedStyle(probe).backgroundColor)
      const section = screen.getByRole('region', {
        name: enUS.tenantProfile.ownership.title,
        exact: true,
      })
      expectInsideBox(section)
    })
  }
})
