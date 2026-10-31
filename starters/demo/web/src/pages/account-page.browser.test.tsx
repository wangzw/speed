/**
 * The account page's block rail in a real browser: a block's title and the
 * first element of its content start on one vertical line under every shipped
 * theme preset.
 *
 * A block header is one icon column fixed at 2rem followed by one spacing-unit
 * gap before the title; the block's content skips the same column and gap. The
 * content inset therefore has to read the theme's spacing base, not a fixed
 * number of spacing steps: a fixed step count only meets the title at the
 * default unit, and drifts by the unit's difference under a preset that changes
 * it. These cases measure the two left edges in Chromium on the security pane,
 * which renders both block shapes -- a titled AccountSection and an expandable
 * SecurityRow.
 */
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { page } from 'vitest/browser'
import { accountUiResources } from '@speed/account-ui'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { themePresets } from '@speed/theme-presets'
import { createBackend } from '../a11y/backends.js'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { ACCOUNT_SECTION_PATHS } from '../paths.js'
import { renderDemoApp } from '../test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from '../test-utils/session.js'

const accountEnUS = accountUiResources['en-US'] as {
  readonly identifiers: { readonly kind: { readonly username: string } }
  readonly password: { readonly current: { readonly label: string } }
}

const LOGIN_WAYS_TITLE = demoEnUS.account.rows.label.loginWays
const PASSWORD_TITLE = demoEnUS.account.rows.label.password

/** The two left edges a block's first line is judged on, in CSS pixels. */
interface RailEdges {
  readonly title: number
  readonly item: number
}

/**
 * The block's title's left edge and the left edge of the first element in its
 * content. The header grid is the section's first element child; the content
 * container is the last one.
 */
function railEdges(section: HTMLElement, title: string): RailEdges {
  const header = section.firstElementChild as HTMLElement
  const heading = within(header).getByRole('heading', { name: title })
  const content = section.lastElementChild as HTMLElement
  const item = content.firstElementChild as HTMLElement
  return {
    title: heading.getBoundingClientRect().left,
    item: item.getBoundingClientRect().left,
  }
}

/** Asserts the two edges sit on one line, naming the preset and block. */
function expectSameRail(preset: string, block: string, edges: RailEdges): void {
  expect(
    Math.abs(edges.title - edges.item),
    `${preset} / ${block}: title at ${edges.title}px, first item at ${edges.item}px`,
  ).toBeLessThanOrEqual(0.5)
}

describe('account page block rail in Chromium', () => {
  for (const preset of themePresets) {
    it(`aligns a block title with its first item under the ${preset.id} preset`, async () => {
      await page.viewport(1280, 900)
      bindRequestFn(createBackend('data'))
      window.history.replaceState({}, '', ACCOUNT_SECTION_PATHS.security)
      renderDemoApp({
        session: createTestSession(
          authenticatedSnapshot({ features: readyFeatures() }),
        ),
        theme: { presets: themePresets, defaultPreset: preset.id },
      })

      // The titled block: its first item is the sign-in kind list's own
      // heading, so waiting on that heading measures settled content rather
      // than the loading branch.
      const loginWays = await screen.findByRole('region', {
        name: LOGIN_WAYS_TITLE,
      })
      await within(loginWays).findByRole('heading', {
        name: accountEnUS.identifiers.kind.username,
      })
      expectSameRail(
        preset.id,
        LOGIN_WAYS_TITLE,
        railEdges(loginWays, LOGIN_WAYS_TITLE),
      )

      // The expandable row: open the password row, wait for its form's first
      // field (the block is on a plain surface, so the row's own title is the
      // only heading in it), then measure the body against the row title.
      const passwordRow = await screen.findByRole('region', {
        name: PASSWORD_TITLE,
      })
      await userEvent
        .setup()
        .click(within(passwordRow).getByTestId('security-row-password'))
      const body = await screen.findByTestId('security-row-password-body')
      await within(body).findByLabelText(accountEnUS.password.current.label)
      expectSameRail(
        preset.id,
        PASSWORD_TITLE,
        railEdges(passwordRow, PASSWORD_TITLE),
      )
    })
  }
})
