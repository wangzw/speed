/**
 * The account page's layout in a real browser. Two rules need the browser
 * tier: a block's title and the first element of its content start on one
 * vertical line under every shipped theme preset, and the sessions block's
 * bulk action rides the block title's row -- inside the band the heading and
 * its description occupy -- above the session list rather than a standalone
 * action row. jsdom proves node ancestry; only the browser tier lays the page
 * out, so only here can the placements be told apart by their boxes.
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
import { beforeEach, describe, expect, it } from 'vitest'
import { page } from 'vitest/browser'
import { accountUiResources } from '@speed/account-ui'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import type { RequestFn, RequestOptions } from '@speed/api-client'
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
  readonly sessions: {
    readonly title: string
    readonly revokeOthers: { readonly label: string }
  }
}

const LOGIN_WAYS_TITLE = demoEnUS.account.rows.label.loginWays
const PASSWORD_TITLE = demoEnUS.account.rows.label.password

beforeEach(() => {
  window.history.replaceState({}, '', '/')
})

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

it('puts the sessions bulk action on the block title row, above the list', async () => {
  // A wide viewport keeps the block header from wrapping, so the action and
  // the title are measured on the one line the rule describes.
  await page.viewport(1000, 900)
  // The data profile signs in on one device only; a second live session is
  // the state that renders the bulk action at all.
  const backend = createBackend('data')
  const twoSessions = (async (
    path: string,
    requestOptions?: RequestOptions,
  ) => {
    if (
      path === '/api/v1/auth/sessions' &&
      (requestOptions?.method ?? 'GET') === 'GET'
    ) {
      const one = {
        kind: 'browser',
        created_at: '2026-01-01T00:00:00Z',
        last_seen_at: '2026-01-01T00:00:00Z',
        expires_at: '2026-02-01T00:00:00Z',
        client_ip: '127.0.0.1',
        user_agent: 'test',
      }
      return {
        sessions: [
          { ...one, id: 'session-1', current: true },
          { ...one, id: 'session-2', current: false },
        ],
      }
    }
    return backend(path, requestOptions)
  }) as RequestFn
  bindRequestFn(twoSessions)
  window.history.replaceState({}, '', ACCOUNT_SECTION_PATHS.sessions)
  renderDemoApp({
    session: createTestSession(authenticatedSnapshot({ features: readyFeatures() })),
  })

  const bulk = await screen.findByRole('button', {
    name: accountEnUS.sessions.revokeOthers.label,
  })
  const title = screen.getByRole('heading', {
    level: 2,
    name: demoEnUS.account.blocks.devices,
  })
  const description = screen.getByText(
    demoEnUS.account.blockDescriptions.devices,
  )
  const list = screen.getByRole('region', {
    name: accountEnUS.sessions.title,
  })

  const bulkBox = bulk.getBoundingClientRect()
  const titleBox = title.getBoundingClientRect()
  const descriptionBox = description.getBoundingClientRect()
  const listBox = list.getBoundingClientRect()

  // The action shares the title's row: it lies within the band the heading
  // and its description occupy, never on a row of its own below them.
  expect(bulkBox.top).toBeGreaterThanOrEqual(titleBox.top - 1)
  expect(bulkBox.bottom).toBeLessThanOrEqual(descriptionBox.bottom + 1)

  // It is lifted out of the list: the control ends above the list's own top
  // edge, where the section's standalone action row used to sit.
  expect(bulkBox.bottom).toBeLessThanOrEqual(listBox.top + 1)
})
