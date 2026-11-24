import type { ReactElement } from 'react'
import { describe, expect, it } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import { page } from 'vitest/browser'
import Button from '@mui/material/Button'
import IconButton from '@mui/material/IconButton'
import Typography from '@mui/material/Typography'
import { toolbarClasses } from '@mui/material/Toolbar'
import { attachSession } from '@speed/auth-core'
import { Avatar } from '@speed/layout-kit'
import layoutKitEnUS from '../../../layout-kit/src/locales/en-US.json' with { type: 'json' }
import layoutKitZhCN from '../../../layout-kit/src/locales/zh-CN.json' with { type: 'json' }
import productShellEnUS from '../locales/en-US.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import {
  authenticatedCompletion,
  jsonResponse,
  makeRealClientRig,
  openFlow,
  principal,
} from '../../test-utils/real-client.js'
import { ProductShell } from './ProductShell.js'

const VIEWPORT_HEIGHT = 900

const NAV_ITEMS = [
  { id: 'home', label: 'Home', href: '/home', selected: true },
] as const

type Language = 'en-US' | 'zh-CN'

interface HostCopy {
  readonly brand: string
  readonly notifications: string
  readonly language: string
  readonly colourMode: string
  readonly consoleEntry: string
  readonly tenant: string
}

const HOST_COPY: Readonly<Record<Language, HostCopy>> = {
  'en-US': {
    brand: 'Speed demo',
    notifications: 'Notifications',
    language: 'Change language',
    colourMode: 'Switch to dark theme',
    consoleEntry: 'Console',
    tenant: 'Acme Corporation',
  },
  'zh-CN': {
    brand: 'Speed 演示',
    notifications: '通知',
    language: '切换语言',
    colourMode: '切换到深色主题',
    consoleEntry: '控制台',
    tenant: '艾克米科技有限公司',
  },
}

const ACCOUNT = principal().display_name

function GlyphButton({
  label,
  glyph,
}: {
  readonly label: string
  readonly glyph: string
}): ReactElement {
  return (
    <IconButton color="inherit" aria-label={label}>
      <span aria-hidden="true">{glyph}</span>
    </IconButton>
  )
}

function tenantHeaderShell(language: Language): ReactElement {
  const copy = HOST_COPY[language]
  return (
    <ProductShell
      navItems={NAV_ITEMS}
      header={
        <Typography
          data-testid="header-brand"
          noWrap
          variant="h6"
          component="div"
          sx={{ color: 'inherit', fontWeight: 600 }}
        >
          {copy.brand}
        </Typography>
      }
      headerActions={
        <>
          <GlyphButton label={copy.notifications} glyph="N" />
          <GlyphButton label={copy.language} glyph="L" />
          <GlyphButton label={copy.colourMode} glyph="C" />
          <Button color="inherit" href="/console">
            {copy.consoleEntry}
          </Button>
        </>
      }
      userMenu={
        <>
          <Button color="inherit" variant="outlined">
            {copy.tenant}
          </Button>
          <IconButton color="inherit" aria-label={ACCOUNT}>
            <Avatar name={ACCOUNT} decorative size={32} />
          </IconButton>
        </>
      }
    >
      <div style={{ height: 240 }}>content</div>
    </ProductShell>
  )
}

async function signIn(): Promise<ReturnType<typeof makeRealClientRig>['session']> {
  const rig = makeRealClientRig((call) => {
    const key = `${call.method} ${call.path}`
    if (key === 'POST /api/v1/auth/flows') {
      return jsonResponse(200, openFlow())
    }
    if (key === 'POST /api/v1/auth/flows/answer') {
      return jsonResponse(200, authenticatedCompletion())
    }
    if (key === 'GET /api/v1/auth/me') {
      return jsonResponse(200, principal())
    }
    if (key === 'POST /api/v1/auth/logout') {
      return new Response(null, { status: 204 })
    }
    throw new Error(`unexpected request: ${key}`)
  })
  attachSession(rig.session)
  await act(async () => {
    const first = await rig.session.startFlow({ method: 'password' })
    if (!('next' in first)) {
      return
    }
    await rig.session.answerFlow({
      seq: first.seq,
      step: 'answer',
      input: { identifier: 'alice@example.com', password: 'password-1' },
    })
  })
  return rig.session
}

function shellCopy(language: Language): typeof layoutKitEnUS {
  return language === 'zh-CN' ? layoutKitZhCN : layoutKitEnUS
}

const BAND_TOLERANCE_PX = 4

function headerBands(): number {
  const header = screen.getByRole('banner')
  const toolbar = header.querySelector<HTMLElement>(`.${toolbarClasses.root}`)
  if (toolbar === null) {
    throw new Error('ProductShell: the banner holds no Toolbar')
  }
  const centers = [
    ...toolbar.querySelectorAll<HTMLElement>(
      'button, a, [data-testid="header-brand"]',
    ),
  ]
    .map((element) => element.getBoundingClientRect())
    .filter((rect) => rect.width > 0 && rect.height > 0)
    .map((rect) => rect.top + rect.height / 2)
    .sort((left, right) => left - right)

  let bands = 0
  let bandCenter = Number.NEGATIVE_INFINITY
  for (const center of centers) {
    if (center - bandCenter > BAND_TOLERANCE_PX) {
      bands += 1
      bandCenter = center
    }
  }
  return bands
}

function expectEveryTenantControlReachable(
  language: Language,
  drawerTrigger: boolean,
): void {
  const copy = HOST_COPY[language]
  const shell = shellCopy(language)
  const controls: readonly { role: 'button' | 'link'; name: string }[] = [
    ...(drawerTrigger
      ? [{ role: 'button' as const, name: shell.appShell.openNav }]
      : []),
    { role: 'button', name: copy.notifications },
    { role: 'button', name: copy.language },
    { role: 'button', name: copy.colourMode },
    { role: 'link', name: copy.consoleEntry },
    { role: 'button', name: copy.tenant },
    { role: 'button', name: ACCOUNT },
  ]
  for (const { role, name } of controls) {
    const control = screen.getByRole(role, { name })
    expect(control).toBeVisible()
    expect(control).toHaveAccessibleName(name)
    expect(control).toBeEnabled()
    expect(control).not.toHaveAttribute('aria-hidden', 'true')
    const rect = control.getBoundingClientRect()
    expect(rect.width).toBeGreaterThan(0)
    expect(rect.height).toBeGreaterThan(0)
    expect(rect.left + rect.width / 2).toBeGreaterThan(0)
    expect(rect.left + rect.width / 2).toBeLessThan(window.innerWidth)
    expect(rect.top + rect.height / 2).toBeGreaterThan(0)
    expect(rect.top + rect.height / 2).toBeLessThan(window.innerHeight)
  }
}

async function renderTenantShell(language: Language): Promise<void> {
  await signIn()
  renderWithProviders(tenantHeaderShell(language), { language })
  await screen.findByRole('banner')
}

describe('ProductShell narrow header bands in Chromium', () => {
  for (const language of ['en-US', 'zh-CN'] as const) {
    for (const width of [375, 430] as const) {
      it(`keeps the tenant header within two bands at ${width}px in ${language} with every control reachable`, async () => {
        await page.viewport(width, VIEWPORT_HEIGHT)
        await renderTenantShell(language)

        expect(headerBands(), `bands at ${width}px in ${language}`).toBeLessThanOrEqual(2)
        expectEveryTenantControlReachable(language, true)
      })
    }
  }
})

describe('ProductShell md header band in Chromium', () => {
  for (const language of ['en-US', 'zh-CN'] as const) {
    it(`keeps the tenant header on a single band at md in ${language}`, async () => {
      await page.viewport(900, VIEWPORT_HEIGHT)
      await renderTenantShell(language)

      await waitFor(() => {
        expect(headerBands()).toBe(1)
      })
      expectEveryTenantControlReachable(language, false)
    })
  }
})

describe('ProductShell session-ended announcement in Chromium', () => {
  it('inserts the live region empty and fills it a frame later', async () => {
    const message = productShellEnUS.announcements.sessionEnded
    const snapshots: string[] = []
    const observer = new MutationObserver(() => {
      const region = document.querySelector('[role="status"]')
      if (region !== null) {
        snapshots.push(region.textContent ?? '')
      }
    })
    observer.observe(document.body, {
      childList: true,
      subtree: true,
      characterData: true,
    })
    try {
      await page.viewport(900, VIEWPORT_HEIGHT)
      const session = await signIn()
      renderWithProviders(tenantHeaderShell('en-US'), { language: 'en-US' })
      await screen.findByRole('banner')

      await act(async () => {
        await session.logout()
      })

      await waitFor(() =>
        expect(screen.getByRole('status')).toBeInTheDocument(),
      )
      await waitFor(() => expect(snapshots.length).toBeGreaterThan(0))
      expect(snapshots[0]).toBe('')

      await waitFor(() =>
        expect(screen.getByRole('status')).toHaveTextContent(message),
      )
      expect(snapshots).toContain(message)
    } finally {
      observer.disconnect()
    }
  })
})
