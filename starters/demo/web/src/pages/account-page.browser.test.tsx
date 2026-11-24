import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { page } from 'vitest/browser'
import { accountUiResources } from '@speed/account-ui'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { chipClasses } from '@mui/material/Chip'
import { avatarInitials } from '@speed/layout-kit'
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

interface RailEdges {
  readonly title: number
  readonly item: number
}

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

function expectSameRail(preset: string, block: string, edges: RailEdges): void {
  expect(
    Math.abs(edges.title - edges.item),
    `${preset} / ${block}: title at ${edges.title}px, first item at ${edges.item}px`,
  ).toBeLessThanOrEqual(0.5)
}

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

function profileWith(displayName: string) {
  return {
    account_id: 'account-1',
    kind: 'user',
    display_name: displayName,
    mfa_required: false,
    password_set: true,
    created_at: '2026-01-01T00:00:00Z',
    attributes: {},
    color_mode: '',
    locale: 'en-US',
    time_zone: 'UTC',
    identifiers: [],
    links: [],
    verification: { state: 'none', email: '', since: null },
  }
}

it('propagates a saved display name to the sidebar account entry', async () => {
  await page.viewport(1280, 900)
  const fallback = createBackend('data')
  let storedName = 'Test User'
  const request = (async (
    path: string,
    requestOptions?: RequestOptions,
  ) => {
    if (path === '/api/v1/auth/me') {
      if (requestOptions?.method === 'PATCH') {
        const body = requestOptions.body as { display_name?: string }
        if (body.display_name !== undefined) {
          storedName = body.display_name
        }
        return {
          attributes: {},
          color_mode: '',
          display_name: storedName,
          locale: 'en-US',
          time_zone: 'UTC',
        }
      }
      return profileWith(storedName)
    }
    return fallback(path, requestOptions)
  }) as RequestFn
  bindRequestFn(request)
  window.history.replaceState({}, '', ACCOUNT_SECTION_PATHS.profile)
  renderDemoApp({
    session: createTestSession(
      authenticatedSnapshot({
        features: readyFeatures('authn.identifiers.email'),
      }),
    ),
  })

  const trigger = await screen.findByTestId('account-entry-trigger')
  expect(trigger).toHaveTextContent('Test User')

  const savedName = 'Ada Lovelace'
  const savedInitials = avatarInitials(savedName)
  const user = userEvent.setup()
  const field = await screen.findByRole('textbox', {
    name: accountEnUS.profile.displayName.label,
  })
  await user.clear(field)
  await user.type(field, savedName)
  await user.tab()

  await waitFor(() => expect(trigger).toHaveTextContent(savedName))
  expect(trigger).not.toHaveTextContent('Test User')
  expect(trigger).toHaveAccessibleName(`${savedInitials} ${savedName}`)
})

it('puts the sessions bulk action on the block title row, above the list', async () => {
  await page.viewport(1000, 900)
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

  expect(bulkBox.top).toBeGreaterThanOrEqual(titleBox.top - 1)
  expect(bulkBox.bottom).toBeLessThanOrEqual(descriptionBox.bottom + 1)

  expect(bulkBox.bottom).toBeLessThanOrEqual(listBox.top + 1)
})

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
