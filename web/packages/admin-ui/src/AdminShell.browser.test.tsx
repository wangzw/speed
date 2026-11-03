/**
 * AdminShell's header in a real browser: the platform frame under the
 * same band invariant as the tenant shell -- 375px and 430px, in en-US
 * and zh-CN, plus the md single row.
 *
 * The console is the second shell, not a copy of the first: its pages
 * resolve no tenant, so it renders no tenant switcher, no notification
 * bell and no console entry (the viewer is already in the console; its
 * exit is a link back to the tenant area). The controls it does render
 * are held to the same rule -- every one stays in the accessibility tree
 * with its name, enabled and non-zero-sized -- so a layout fix cannot win
 * a band by folding a control away.
 *
 * Host content is data in this fixture, as the surrounding host-content
 * fixtures document; the zh-CN set is the wider real-language text the
 * invariant has to hold for too.
 */
import type { ReactElement } from 'react'
import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { page } from 'vitest/browser'
import IconButton from '@mui/material/IconButton'
import Link from '@mui/material/Link'
import Typography from '@mui/material/Typography'
import { toolbarClasses } from '@mui/material/Toolbar'
import { Avatar } from '@speed/layout-kit'
import layoutKitEnUS from '../../layout-kit/src/locales/en-US.json' with { type: 'json' }
import layoutKitZhCN from '../../layout-kit/src/locales/zh-CN.json' with { type: 'json' }
import { AdminShell } from './AdminShell.js'
import type { AdminRoute } from './route.js'
import { renderWithProviders } from '../test-utils/render.js'

const VIEWPORT_HEIGHT = 900

const ROUTES: readonly AdminRoute[] = [
  { path: '/tenants', view: () => null, label: 'Tenants' },
]

type Language = 'en-US' | 'zh-CN'

/**
 * The host's header labels, per language. Host content, not copy this
 * shell renders -- data for the slots, exactly as a real host's own
 * translation bundle supplies it.
 */
interface HostCopy {
  readonly brand: string
  readonly backToApp: string
  readonly language: string
  readonly colourMode: string
}

const HOST_COPY: Readonly<Record<Language, HostCopy>> = {
  'en-US': {
    brand: 'Speed demo',
    backToApp: 'Back to app',
    language: 'Change language',
    colourMode: 'Switch to dark theme',
  },
  'zh-CN': {
    brand: 'Speed 演示',
    backToApp: '返回应用',
    language: '切换语言',
    colourMode: '切换到深色主题',
  },
}

/** The operator's display name: the account avatar's accessible name. A
 * person's name is not translated, so both languages carry the same one. */
const ACCOUNT = 'Ada Lovelace'

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

/** The console's top bar: the brand plus the controls this shell actually
 * renders -- exit back to the tenant area, language switch, colour mode
 * and account avatar. No tenant switcher, no notification bell and no
 * console entry. */
function adminHeaderShell(language: Language): ReactElement {
  const copy = HOST_COPY[language]
  return (
    <AdminShell
      routes={ROUTES}
      hasFeature={() => true}
      title={
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
      headerActions={() => (
        <>
          <Link href="/home" underline="hover" sx={{ color: 'inherit' }}>
            {copy.backToApp}
          </Link>
          <GlyphButton label={copy.language} glyph="L" />
          <GlyphButton label={copy.colourMode} glyph="C" />
          <IconButton color="inherit" aria-label={ACCOUNT}>
            <Avatar name={ACCOUNT} decorative size={32} />
          </IconButton>
        </>
      )}
    >
      <div style={{ height: 240 }}>content</div>
    </AdminShell>
  )
}

/** The app-shell strings in the case's language: the drawer trigger's
 * accessible name is the shell's own copy, from layout-kit's bundle. */
function shellCopy(language: Language): typeof layoutKitEnUS {
  return language === 'zh-CN' ? layoutKitZhCN : layoutKitEnUS
}

/** The tolerance a pair of controls on one band may differ by; see the
 * tenant shell's browser suite for the reasoning. */
const BAND_TOLERANCE_PX = 4

/** The number of vertical bands the header's content occupies. */
function headerBands(): number {
  const header = screen.getByRole('banner')
  const toolbar = header.querySelector<HTMLElement>(`.${toolbarClasses.root}`)
  if (toolbar === null) {
    throw new Error('AdminShell: the banner holds no Toolbar')
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

/** Every control the console shell renders, in the case's language. The
 * drawer trigger is the shell's own and exists only below md. */
function expectEveryAdminControlReachable(
  language: Language,
  drawerTrigger: boolean,
): void {
  const copy = HOST_COPY[language]
  const shell = shellCopy(language)
  const controls: readonly { role: 'button' | 'link'; name: string }[] = [
    ...(drawerTrigger
      ? [{ role: 'button' as const, name: shell.appShell.openNav }]
      : []),
    { role: 'link', name: copy.backToApp },
    { role: 'button', name: copy.language },
    { role: 'button', name: copy.colourMode },
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

describe('AdminShell narrow header bands in Chromium', () => {
  for (const language of ['en-US', 'zh-CN'] as const) {
    for (const width of [375, 430] as const) {
      it(`keeps the console header within two bands at ${width}px in ${language} with every control reachable`, async () => {
        await page.viewport(width, VIEWPORT_HEIGHT)
        renderWithProviders(adminHeaderShell(language), { language })

        expect(headerBands(), `bands at ${width}px in ${language}`).toBeLessThanOrEqual(2)
        expectEveryAdminControlReachable(language, true)
      })
    }
  }
})

describe('AdminShell md header band in Chromium', () => {
  for (const language of ['en-US', 'zh-CN'] as const) {
    it(`keeps the console header on a single band at md in ${language}`, async () => {
      await page.viewport(900, VIEWPORT_HEIGHT)
      renderWithProviders(adminHeaderShell(language), { language })

      await waitFor(() => {
        expect(headerBands()).toBe(1)
      })
      expectEveryAdminControlReachable(language, false)
    })
  }
})
