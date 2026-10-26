/**
 * ProductShell's header in a real browser: the band invariant across the
 * full matrix the acceptance names -- the tenant-facing shell at 375px and
 * 430px, in en-US and zh-CN.
 *
 * The narrow header is the frame's topbar shape: the drawer trigger, the
 * brand and the header actions share the first band, and the user menu
 * takes a full-width second band. At md and up the whole header is one
 * row. Both properties are geometry, so jsdom cannot answer them; the
 * cases measure the real Chromium layout.
 *
 * The second half of the invariant is the one a layout fix must not win
 * by cheating: every control the shell renders stays in the accessibility
 * tree with its accessible name, enabled and non-zero-sized. A control
 * folded away, aria-hidden, disabled or collapsed to nothing fails the
 * case even when the band count is two.
 *
 * Host content is data in this fixture, as the surrounding host-content
 * fixtures document: the strings stand in for the demo's own translated
 * labels, and the zh-CN set carries the wider real-language text (a
 * Chinese tenant name and the localized action labels) that the invariant
 * has to hold for too.
 */
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

/**
 * The host's header labels, per language. Host content, not copy this
 * shell renders -- data for the slots, exactly as a real host's own
 * translation bundle supplies it. The zh-CN set is deliberately the wider
 * one: the invariant exists to hold in the language whose labels measure
 * longer.
 */
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

/** The signed-in account's display name: the account avatar's accessible
 * name, as the demo's own user menu derives it from the principal. */
const ACCOUNT = principal().display_name

/** One icon-form header control: the glyph is decorative, and the
 * accessible name is the part the shell has to keep reachable. */
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

/** The tenant-facing top bar: the brand plus the six ruled controls the
 * tenant area renders -- notification bell, language switch, colour mode,
 * console entry, tenant switcher, account avatar. */
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

/** A login script over the real-client rig: the flow start, its first
 * answer and the profile the completion reads. */
async function signIn(): Promise<void> {
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
}

/** The app-shell strings in the case's language: the drawer trigger's
 * accessible name is the shell's own copy, from its shipped bundle. */
function shellCopy(language: Language): typeof layoutKitEnUS {
  return language === 'zh-CN' ? layoutKitZhCN : layoutKitEnUS
}

/** The tolerance a pair of controls on one band may differ by. The Toolbar
 * centers its items, so every control on a row shares a vertical center;
 * members of another row are a whole row height away. The tolerance only
 * absorbs subpixel rounding. */
const BAND_TOLERANCE_PX = 4

/**
 * The number of vertical bands the header's content occupies: the brand
 * and every control, clustered by their vertical center. The brand counts
 * because a brand alone on a band is still a band the header takes, which
 * is exactly the arrangement the invariant rules out.
 */
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

/** Every control the tenant shell renders, in the case's language. The
 * drawer trigger is the shell's own; the other six are the ruled host
 * controls. */
function expectEveryTenantControlReachable(
  language: Language,
  drawerTrigger: boolean,
): void {
  const copy = HOST_COPY[language]
  const shell = shellCopy(language)
  const controls: readonly { role: 'button' | 'link'; name: string }[] = [
    // The drawer trigger exists only below md: at md the permanent drawer
    // has no hamburger, so there is no control to reach.
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
    // In the accessibility tree with its name: role+name already skips
    // hidden elements, and the explicit check spells out the half the
    // invariant names.
    expect(control).toBeVisible()
    expect(control).toHaveAccessibleName(name)
    // Clickable: neither disabled nor aria-hidden.
    expect(control).toBeEnabled()
    expect(control).not.toHaveAttribute('aria-hidden', 'true')
    // Non-zero-sized and on screen: a control collapsed to nothing, or
    // pushed out of the viewport by the layout, is not reachable by
    // pointer even when it still has a role.
    const rect = control.getBoundingClientRect()
    expect(rect.width).toBeGreaterThan(0)
    expect(rect.height).toBeGreaterThan(0)
    expect(rect.left + rect.width / 2).toBeGreaterThan(0)
    expect(rect.left + rect.width / 2).toBeLessThan(window.innerWidth)
    expect(rect.top + rect.height / 2).toBeGreaterThan(0)
    expect(rect.top + rect.height / 2).toBeLessThan(window.innerHeight)
  }
}

/** Mount the tenant frame authenticated and wait for its banner. */
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
