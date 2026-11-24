// @journey journey-member-organization.md
// @covers REQ-notifications-001, REQ-notifications-003, REQ-notifications-004, REQ-notifications-010, REQ-notifications-028, REQ-notifications-029, REQ-notifications-030, REQ-notifications-051, REQ-tenancy-022, REQ-tenancy-036
import type { Locator, Page } from '@playwright/test'

import { e2eMember } from '../fixtures/config'
import { expect, test } from '../fixtures/demo'
import {
  closeAccountMenu,
  openAccountMenu,
  organizationRow,
} from '../helpers/account-menu'
import { copy, copyKey, copyPattern, type CopyLocale } from '../helpers/i18n'
import { expectClippedAnnouncement } from '../helpers/tenant-switch'

test.use({ seedMember: true })

const TYPE_KEY = 'demo.notice'

const TENANT_NAME = e2eMember.tenant

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

async function switchToTenant(page: Page): Promise<void> {
  const menu = await openAccountMenu(page)
  const current = menu.getByRole('menuitem', { expanded: false }).first()
  await expect(current).toBeVisible()
  await current.click()
  const others = menu.getByRole('group', {
    name: copy('demo', 'userMenu.organizations'),
    exact: true,
  })
  await expect(others).toBeVisible()
  const row = others.getByRole('menuitem', { name: TENANT_NAME, exact: true })
  if ((await row.count()) === 0) {
    await closeAccountMenu(page)
    return
  }
  await row.click()
  await expectClippedAnnouncement(
    page,
    copy('tenancyUi', 'tenantSwitcher.switchedTo', { tenant: TENANT_NAME }),
  )
  // The pick closes the menu; wait for it to leave so the next open matches exactly one menu.
  await closeAccountMenu(page)
  await openAccountMenu(page)
  await expect(organizationRow(page, TENANT_NAME)).toBeVisible()
  await closeAccountMenu(page)
}

const NOTICE_LOCALE: CopyLocale = 'en-US'

const NOTICE_TITLE = copyKey(
  'demoNotice',
  'demo.notice.in_app.title',
  NOTICE_LOCALE,
)
const NOTICE_BODY = copyKey(
  'demoNotice',
  'demo.notice.in_app.body',
  NOTICE_LOCALE,
)

function accountTrigger(page: Page): Locator {
  return page.getByTestId('account-entry-trigger')
}

function countPhrase(): RegExp {
  const template = copy('notificationUi', 'bell.labelWithCount', {
    n: '\u0000',
  })
  return new RegExp(template.split('\u0000').map(escapeRegExp).join('(\\d+)'))
}

async function unreadCount(page: Page): Promise<number> {
  const label = await accountTrigger(page).getAttribute('aria-label')
  const match = label === null ? null : countPhrase().exec(label)
  return match?.[1] === undefined ? 0 : Number(match[1])
}

function inbox(page: Page): Locator {
  return page.getByRole('list', {
    name: copy('notificationUi', 'inbox.title'),
  })
}

function matrix(page: Page): Locator {
  return page.getByRole('table', {
    name: copy('notificationUi', 'preferences.title'),
  })
}

function preferenceRow(page: Page): Locator {
  return matrix(page)
    .getByRole('row')
    .filter({ hasText: TYPE_KEY })
}

function channelToggle(
  page: Page,
  channel: 'in_app' | 'email' | 'sms',
): Locator {
  return preferenceRow(page).getByRole('checkbox', {
    name: copy('notificationUi', 'preferences.channelToggleAria', {
      type: TYPE_KEY,
      channel: copy('notificationUi', `preferences.channel.${channel}`),
    }),
  })
}

function rowsWithMark(
  page: Page,
  key: 'inbox.unread' | 'inbox.read',
): Locator {
  return inbox(page)
    .getByRole('listitem')
    .filter({ has: page.getByText(copy('notificationUi', key), { exact: true }) })
}

async function afterNextNotice(page: Page): Promise<number> {
  const previous = await unreadCount(page)
  await expect
    .poll(() => unreadCount(page), {
      message: 'the worker never dispatched another notice',
    })
    .toBeGreaterThan(previous)
  return unreadCount(page)
}

test('member-organization: a member receives, reads and configures notifications', async ({
  demo,
}) => {
  const { page, accounts } = demo

  await test.step('the member signs in and the tenant area confirms the current organization', async () => {
    await accounts.signIn({
      identifier: e2eMember.username,
      password: e2eMember.password,
    })
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'home.heading'),
        exact: true,
      }),
    ).toBeVisible()

    await switchToTenant(page)

    const menu = await openAccountMenu(page)
    const current = organizationRow(page, TENANT_NAME)
    await expect(current).toBeVisible()
    await expect(current).toBeEnabled()
    await current.click()
    const others = menu.getByRole('group', {
      name: copy('demo', 'userMenu.organizations'),
      exact: true,
    })
    await expect(others).toBeVisible()
    await expect(
      others.getByRole('menuitem', { name: TENANT_NAME, exact: true }),
    ).toHaveCount(0)
    await closeAccountMenu(page)
  })

  await test.step("the inbox's precondition: it already holds an unread notice", async () => {
    await expect
      .poll(() => unreadCount(page), {
        message: 'the member never received the first notice',
      })
      .toBeGreaterThanOrEqual(1)
  })

  await test.step('a notice arrives on another page and the bell counts it without a reload', async () => {
    await afterNextNotice(page)

    await openAccountMenu(page)
    await page
      .getByRole('menu')
      .getByRole('menuitem', {
        name: copyPattern('demo', 'userMenu.messagesWithCount', {
          n: '\\d+',
        }),
      })
      .click()
    const list = inbox(page)
    const newest = list.getByRole('listitem').first()
    await expect(newest).toBeVisible()
    await expect(newest.getByText(NOTICE_TITLE, { exact: true })).toBeVisible()
    await expect(newest.getByText(NOTICE_BODY, { exact: true })).toBeVisible()
    await expect(
      newest.getByText(copy('notificationUi', 'inbox.unread'), {
        exact: true,
      }),
    ).toBeVisible()
    await expect(accountTrigger(page)).toHaveAccessibleName(countPhrase())
  })

  await test.step('marking the message read clears its mark and decrements the bell', async () => {
    const list = inbox(page)
    await expect(rowsWithMark(page, 'inbox.read')).toHaveCount(0)
    const before = await unreadCount(page)
    const newest = list.getByRole('listitem').first()
    const markRead = newest.getByRole('button', {
      name: copy('notificationUi', 'inbox.markReadAria', {
        title: NOTICE_TITLE,
      }),
    })

    await markRead.click()

    const read = rowsWithMark(page, 'inbox.read')
    await expect(read).toHaveCount(1)
    await expect(
      read.getByRole('button', {
        name: copyPattern('notificationUi', 'inbox.markReadAria', {
          title: '[^"]*',
        }),
      }),
    ).toHaveCount(0)

    await expect
      .poll(
        async () =>
          (await unreadCount(page)) -
          (await rowsWithMark(page, 'inbox.unread').count()),
        {
          message: 'the bell and the inbox disagree on the unread total',
        },
      )
      .toBe(0)
    await expect
      .poll(() => unreadCount(page), {
        message: 'the bell did not fall after the message was marked read',
      })
      .toBeLessThan(before)
  })

  await test.step('marking everything read reports the count and empties the bell', async () => {
    const list = inbox(page)
    const rows = list.getByRole('listitem')
    const rowsBefore = await rows.count()
    const unreadBefore = await rowsWithMark(page, 'inbox.unread').count()
    const before = await unreadCount(page)

    await page
      .getByRole('button', {
        name: copy('notificationUi', 'inbox.markAllRead'),
        exact: true,
      })
      .click()

    const done = page.getByText(
      copyPattern('notificationUi', 'inbox.markAllReadDone', { n: '(\\d+)' }),
    )
    await expect(done).toBeVisible()
    const matched = copyPattern('notificationUi', 'inbox.markAllReadDone', {
      n: '(\\d+)',
    }).exec(((await done.textContent()) ?? '').trim())
    const marked = matched?.[1] === undefined ? Number.NaN : Number(matched[1])
    expect(marked).toBeGreaterThanOrEqual(before)
    expect(marked).toBeGreaterThanOrEqual(unreadBefore)

    await expect
      .poll(() => rowsWithMark(page, 'inbox.read').count(), {
        message: 'a row the mark-all click saw is still unread',
      })
      .toBeGreaterThanOrEqual(rowsBefore)

    await expect(accountTrigger(page)).not.toHaveAccessibleName(countPhrase())
  })

  await test.step('the preference matrix shows the type with its default channels', async () => {
    const table = matrix(page)
    await expect(table.getByRole('row').filter({ hasText: TYPE_KEY }))
      .toHaveCount(1)

    await expect(table.getByRole('columnheader')).toHaveText([
      copy('notificationUi', 'preferences.column.type'),
      copy('notificationUi', 'preferences.channel.in_app'),
      copy('notificationUi', 'preferences.channel.email'),
      copy('notificationUi', 'preferences.channel.sms'),
    ])

    await expect(channelToggle(page, 'in_app')).toBeChecked()
    await expect(channelToggle(page, 'email')).toBeChecked()
    await expect(channelToggle(page, 'sms')).not.toBeChecked()
  })

  await test.step('turning the email channel off confirms the change and leaves in-app alone', async () => {
    await afterNextNotice(page)
    await channelToggle(page, 'email').click()

    await expect(
      page.getByText(
        copy('notificationUi', 'preferences.saved', {
          type: TYPE_KEY,
          channel: copy('notificationUi', 'preferences.channel.email'),
          state: copy('notificationUi', 'preferences.state.disabled'),
        }),
        { exact: true },
      ),
    ).toBeVisible()

    await expect(channelToggle(page, 'email')).not.toBeChecked()
    await expect(channelToggle(page, 'in_app')).toBeChecked()
  })

  await test.step('a later notice still arrives in-app with the email channel off', async () => {
    const list = inbox(page)
    const rows = list.getByRole('listitem')
    const rowsBefore = await rows.count()
    const before = await unreadCount(page)

    await expect
      .poll(() => unreadCount(page), {
        message: 'no notice reached the bell after the email channel was turned off',
      })
      .toBeGreaterThan(before)

    await expect
      .poll(() => rows.count(), {
        message: 'the inbox did not gain the notice the bell reported',
      })
      .toBeGreaterThan(rowsBefore)

    const newest = rows.first()
    await expect(newest.getByText(NOTICE_TITLE, { exact: true })).toBeVisible()
    await expect(
      newest.getByText(copy('notificationUi', 'inbox.unread'), {
        exact: true,
      }),
    ).toBeVisible()

    await expect(channelToggle(page, 'email')).not.toBeChecked()
    await expect(channelToggle(page, 'in_app')).toBeChecked()
  })
})
