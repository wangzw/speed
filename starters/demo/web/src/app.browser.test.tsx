/**
 * The header admin entry's surface in a real browser.
 *
 * The entry is an action, not a label: its full form wears the outlined
 * action pill, and the collapsed rail keeps the icon-only button. jsdom
 * carries the class names too, but the class is the whole subject here --
 * the two tiers differ only in the material they are built from -- so the
 * browser tier owns the case beside the demo's other geometry cases.
 */

import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import { LAYOUT_KIT_NAMESPACE } from '@speed/layout-kit'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from './test-utils/session.js'

describe('the admin entry surface', () => {
  it('is the outlined action pill, and the collapsed rail keeps the icon form', async () => {
    await page.viewport(1100, 900)
    const session = createTestSession(
      authenticatedSnapshot({ system: ['tenant.read'] }),
    )
    const { i18n } = renderDemoApp({ session, shellLayout: 'sidebar' })
    const user = userEvent.setup()

    const entry = await screen.findByRole('link', {
      name: demoEnUS.nav.admin,
    })
    // The entry still links, and its full form is the outlined pill.
    expect(entry).toHaveClass('MuiButton-outlined')

    const tLayout = i18n.getFixedT('en-US', LAYOUT_KIT_NAMESPACE)
    await user.click(
      screen.getByRole('button', {
        name: tLayout('appShell.collapseNav'),
      }),
    )

    await waitFor(() => {
      const links = screen.queryAllByRole('link', {
        name: demoEnUS.nav.admin,
      })
      expect(links).toHaveLength(1)
      expect(links[0]).not.toHaveClass('MuiButton-outlined')
      expect(links[0]?.closest('.MuiIconButton-root')).not.toBeNull()
    })
  })
})
