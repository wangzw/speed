/**
 * The account page's layout in a real browser. Two rules need the browser
 * tier: a block's title and the first element of its content start on one
 * vertical line under every shipped theme preset, and the sessions block's
 * bulk action rides the block title's row -- inside the band the heading and
 * its description occupy -- above the session list rather than a standalone
 * action row. jsdom proves node ancestry; only the browser tier lays the page
 * out, so only here can the placements be told apart by their boxes.
 *
 * The panes' status chips are swept here too: every chip the four account
 * panes render is read from the live DOM, so one chip that carries primary
 * away from the current session is named beside the panes that are correct.
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
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { page } from 'vitest/browser'
import { accountUiResources } from '@speed/account-ui'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { chipClasses } from '@mui/material/Chip'
import { themePresets } from '@speed/theme-presets'
import { createBackend } from '../a11y/backends.js'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { ACCOUNT_SECTIONS, ACCOUNT_SECTION_PATHS } from '../paths.js'
import { renderDemoApp } from '../test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from '../test-utils/session.js'

const accountEnUS = accountUiResources['en-US'] as {
  readonly identifiers: {
    readonly kind: { readonly username: string }
    readonly verified: string
  }
  readonly password: { readonly current: { readonly label: string } }
  readonly preferences: { readonly timezone: { readonly label: string } }
  readonly profile: {
    readonly displayName: { readonly label: string }
  }
  readonly verification: { readonly codeLabel: string }
  readonly sessions: {
    readonly title: string
    readonly current: string
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

/** The rendered width of a field's outlined input box, in CSS pixels. */
function outlinedFieldWidth(field: HTMLElement): number {
  const root = field.closest('.MuiOutlinedInput-root')
  if (root === null) {
    throw new Error('the field renders no outlined input box')
  }
  return root.getBoundingClientRect().width
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

it('gives the identity row fields the contact form field width', async () => {
  // The identity rows and the contact forms are form fields on one pane, so
  // they share one reading width and stop at the same cap; the identity rows
  // used to stop at half that while the forms beside them ran to 600px.
  await page.viewport(1280, 900)
  bindRequestFn(createBackend('data'))
  window.history.replaceState({}, '', ACCOUNT_SECTION_PATHS.profile)
  renderDemoApp({
    session: createTestSession(
      authenticatedSnapshot({
        features: readyFeatures('authn.identifiers.email'),
      }),
    ),
  })

  const username = await screen.findByPlaceholderText(
    demoEnUS.account.rows.value.notSet,
  )
  const displayName = await screen.findByLabelText(
    accountEnUS.profile.displayName.label,
  )
  const code = await screen.findByLabelText(accountEnUS.verification.codeLabel)

  const formWidth = outlinedFieldWidth(code)
  const usernameWidth = outlinedFieldWidth(username)
  const displayNameWidth = outlinedFieldWidth(displayName)
  expect(
    Math.abs(usernameWidth - formWidth),
    `username field ${usernameWidth}px, form field ${formWidth}px`,
  ).toBeLessThanOrEqual(0.5)
  expect(
    Math.abs(displayNameWidth - formWidth),
    `display name field ${displayNameWidth}px, form field ${formWidth}px`,
  ).toBeLessThanOrEqual(0.5)
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

/**
 * Waits for the pane's own data-driven content, so the chip sweep below runs
 * on settled rows rather than on a loading skeleton. Each pane names the
 * element its reads fill; preferences carries no chip, so its time zone
 * select is the readiness signal there.
 */
async function waitForSettledPane(
  section: (typeof ACCOUNT_SECTIONS)[number],
): Promise<void> {
  switch (section) {
    case 'profile':
      await screen.findByText(accountEnUS.identifiers.verified, {
        selector: `.${chipClasses.label}`,
      })
      return
    case 'security':
      await screen.findByText(demoEnUS.account.rows.value.passwordSet, {
        selector: `.${chipClasses.label}`,
      })
      return
    case 'sessions':
      await screen.findByText(accountEnUS.sessions.current, {
        selector: `.${chipClasses.label}`,
      })
      return
    case 'preferences':
      await waitFor(() => {
        expect(
          screen.getByRole('combobox', {
            name: accountEnUS.preferences.timezone.label,
          }),
        ).toHaveTextContent('UTC')
      })
  }
}

describe('account pane status chip colors', () => {
  for (const section of ACCOUNT_SECTIONS) {
    it(`uses success or default for status chips on the ${section} pane, with primary only on the current session`, async () => {
      // A status chip states a completed positive state (success) or a
      // neutral/unfinished one (default). Primary is not a status: it marks
      // the session the viewer is using, and nowhere else.
      await page.viewport(1280, 900)
      bindRequestFn(createBackend('data'))
      const path = ACCOUNT_SECTION_PATHS[section]
      window.history.replaceState({}, '', path)
      renderDemoApp({
        session: createTestSession(
          authenticatedSnapshot({ features: readyFeatures() }),
        ),
      })
      await waitForSettledPane(section)

      const chips = Array.from(
        document.querySelectorAll<HTMLElement>(`.${chipClasses.root}`),
      )
      const violations: string[] = []
      for (const chip of chips) {
        const text = (chip.textContent ?? '').trim()
        const colorClasses = Array.from(chip.classList).filter((name) =>
          name.startsWith(`${chipClasses.root}-color`),
        )
        if (chip.classList.contains(chipClasses.colorPrimary)) {
          // Primary is allowed on exactly one chip: the current session's.
          const currentSession =
            section === 'sessions' && text === accountEnUS.sessions.current
          if (!currentSession) {
            violations.push(
              `${path}: "${text}" uses primary, which belongs only to "${accountEnUS.sessions.current}"`,
            )
          }
          continue
        }
        const status =
          chip.classList.contains(chipClasses.colorSuccess) ||
          chip.classList.contains(chipClasses.colorDefault)
        if (!status) {
          violations.push(
            `${path}: "${text}" uses ${
              colorClasses.length === 0
                ? 'no color class'
                : colorClasses.join(', ')
            }, but a status chip must use success or default`,
          )
        }
      }

      expect(violations, `off-palette status chips on ${path}`).toEqual([])
    })
  }
})
