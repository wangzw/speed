import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import { SmsPage } from './SmsPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import { createAdminOperations, makeAdminBackend } from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import type { AdminSmsTemplate } from './operations.js'
import enUS from './locales/en-US.json' with { type: 'json' }

const FLOOR_WIDTHS = [600, 900, 1200] as const

function smsTemplate(name: string): AdminSmsTemplate {
  return {
    name,
    carrier: 'log',
    kind: 'notification',
    source: 'runtime',
    params: ['code'],
    locales: ['zh-CN', 'en-US'],
    bodies: {},
    attributes: {},
    approvals: {
      'zh-CN': { state: 'approved' },
      'en-US': { state: 'approved' },
    },
    enabled: true,
    version: 1,
    created_at: '2026-10-15T12:00:00Z',
    updated_at: '2026-10-15T12:00:00Z',
  }
}

const TEMPLATES: readonly AdminSmsTemplate[] = [smsTemplate('authn-login-code')]

function removeName(name: string): string {
  return enUS.sms.removeAria.replace('{{name}}', name)
}

async function removeRow(
  names: readonly string[],
  target: string,
): Promise<void> {
  let rows = names.map(smsTemplate)
  const operations = createAdminOperations(
    makeAdminBackend({
      listSmsTemplates: () =>
        Promise.resolve({ templates: rows, next_cursor: '' }),
      removeSmsTemplate: (name) => {
        rows = rows.filter((row) => row.name !== name)
        return Promise.resolve()
      },
    }),
  )
  renderWithProviders(<SmsPage />, { operations, language: 'en-US' })
  await screen.findByText(target)
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: removeName(target) }))
  const dialog = await screen.findByRole('dialog')
  await user.click(
    within(dialog).getByRole('button', { name: enUS.common.confirm }),
  )
}

describe('SmsPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the SMS template table inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)

      const operations = createAdminOperations(
        makeAdminBackend({
          listSmsTemplates: () =>
            Promise.resolve({ templates: [...TEMPLATES], next_cursor: '' }),
        }),
      )
      renderWithProviders(<SmsPage />, { operations, language: 'en-US' })

      await screen.findByText('authn-login-code')
      await expectNoHorizontalOverflow()
    })
  }
})

describe('SmsPage removal focus in Chromium', () => {
  it('moves focus to the next row after a middle row is removed', async () => {
    await page.viewport(1200, 900)
    await removeRow(['alpha', 'bravo', 'charlie'], 'bravo')

    await waitFor(() => expect(screen.queryByText('bravo')).toBeNull())
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: removeName('charlie') }),
      ).toHaveFocus(),
    )
    expect(document.activeElement).not.toBe(document.body)
  })

  it('moves focus to the previous row after the last row is removed', async () => {
    await page.viewport(1200, 900)
    await removeRow(['alpha', 'bravo', 'charlie'], 'charlie')

    await waitFor(() => expect(screen.queryByText('charlie')).toBeNull())
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: removeName('bravo') }),
      ).toHaveFocus(),
    )
    expect(document.activeElement).not.toBe(document.body)
  })

  it('moves focus to the template table container when the only row is removed', async () => {
    await page.viewport(1200, 900)
    await removeRow(['alpha'], 'alpha')

    await waitFor(() => expect(screen.queryByText('alpha')).toBeNull())
    await waitFor(() =>
      expect(
        screen.getByRole('group', { name: enUS.sms.tableLabel }),
      ).toHaveFocus(),
    )
    expect(document.activeElement).not.toBe(document.body)
  })
})
