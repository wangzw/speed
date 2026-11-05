/**
 * J8 -- a member of an organization works the notification surface end to end.
 *
 * The member signs in as an ordinary active member of the seeded tenant. A
 * system action -- the review build's notification worker, dispatching one
 * notice per tenant per ten-second window -- addresses the notice to the
 * member, the header bell reports it without a page reload, and the
 * notification surface lists it unread. The member marks it read, then marks
 * everything read and sees the count the server reported and a cleared bell.
 * The preference matrix shows the channels the member can be addressed on;
 * turning the email channel off succeeds, the matrix settles on the in-app
 * channel alone, and a later window's notice still reaches the inbox.
 *
 * Everything is driven through the rendered product with mouse and keyboard:
 * roles and accessible names, visible text, checked and disabled state, focus
 * and the live confirmation regions. The member is a seed membership the
 * harness's configuration declares, so the binary's own seed command creates
 * the account and the row that binds it to the tenant; no test writes a row
 * into the running database, and no test calls the API.
 *
 * The one thing the browser cannot show is the mailbox: the review mail
 * carrier reaches no relay, and the process log is not a journey's read (this
 * journey reads only the verification code). The note "without an email" is
 * therefore asserted as far as the product renders it -- the matrix's effective channel
 * set is the in-app channel alone after the write, and the next notice still
 * arrives in the inbox -- and no further. REQ-notifications-057 is left out
 * of `@covers` for that reason: a delivery that ignored the stored preference
 * would not change anything this spec observes.
 *
 * REQ-notifications-009 is left out for the same kind of reason: it asks the
 * effective channel set to say whether it came from a stored preference or
 * the type's defaults, but the preference matrix renders only the effective
 * set. The browser cannot tell the two apart, so the spec asserts what the
 * product shows -- the type's defaults before the write and the stored set
 * after it -- and not the explicit/default flag the matrix never displays.
 *
 * REQ-notifications-013 is also left out: its acceptance judges the push by
 * the prompt frame on the recipient's live connection and explicitly forbids
 * judging it by list or cache content. The browser-only tier can see the
 * bell's unread count move on its own, but a client that re-read the count on
 * a timer would show the same movement, so the assertion cannot tell push
 * from poll. The spec therefore asserts the rendered arrival and not the
 * push-to-the-connection property.
 *
 * REQ-notifications-030 is claimed for the fixed column order this assembly
 * shows, and not for the whole requirement: the review type declares its
 * optional channels in-app, email then sms, which is also the platform's order,
 * so a matrix that followed the host's declaration order would render the same
 * sequence. The reversed-declaration case is not driven here.
 *
 * REQ-notifications-001 is claimed narrowly: the spec asserts the notice's
 * title, body and unread state in the signed-in member's own inbox. The
 * other row fields (type, group, parameters, link, received and read times)
 * and a second recipient's isolation are not asserted.
 *
 * The worker dispatches one notice per tenant per ten-second window, so a
 * count read and the write that follows it must not straddle a window or the
 * new notice makes an exact count wrong for reasons the journey did not
 * cause. The steps act just after a window lands, which leaves the next
 * window a full interval away; the counts they read therefore describe the
 * messages already rendered, not a dispatch still in flight. Where an arrival
 * cannot be ruled out, the assertion is stated against the rendered list
 * rather than a cached total.
 *
 * Expected copy is read from the shipped en-US bundles through
 * helpers/i18n.ts -- the interface strings from notification-ui, and the
 * notice's title and body from the review assembly's own notification catalog,
 * whose keys are literal dotted strings. The notice is read in en-US because the
 * worker renders it in the recipient's language: this journey's seeded member
 * stores no language preference, so the dispatch resolves to the platform
 * default, en-US.
 */
// @journey journey-member-organization.md
// @covers REQ-notifications-001, REQ-notifications-003, REQ-notifications-004, REQ-notifications-010, REQ-notifications-028, REQ-notifications-029, REQ-notifications-030, REQ-notifications-051, REQ-tenancy-022, REQ-tenancy-036
import type { Locator, Page } from '@playwright/test'

import { reviewMember } from '../fixtures/config'
import { expect, test } from '../fixtures/demo'
import { copy, copyKey, copyPattern, type CopyLocale } from '../helpers/i18n'
import { expectClippedAnnouncement } from '../helpers/tenant-switch'

// This journey signs in as the seeded member, so its server must carry the
// account and the membership the template does not seed.
test.use({ seedMember: true })

/** The notification type the review build declares. Its key is the matrix
 * row's label: the demo composes the matrix without a type-label override. */
const TYPE_KEY = 'review.notice'

/** The seeded tenant's name: the alias the switcher renders. */
const TENANT_NAME = reviewMember.tenant

/** The tenant switcher's trigger, naming the current organization. */
function tenantSwitcher(page: Page): Locator {
  return page
    .getByRole('banner')
    .getByRole('button', { name: TENANT_NAME, exact: true })
}

/**
 * Move the tenant area to the seeded organization. The member's account also
 * owns a personal organization, and the host settles that row as the current
 * organization for an ordinary member, so the trigger's current name is not
 * known up front. The menu's row is disabled when the organization is already
 * current, which is the only case this helper leaves the menu without a click.
 */
async function switchToTenant(page: Page): Promise<void> {
  const trigger = tenantSwitcher(page).or(
    page
      .getByRole('banner')
      .getByRole('button', {
        name: copy('demo', 'organization.personal'),
        exact: true,
      }),
  )
  await expect(trigger).toBeVisible()
  await trigger.click()
  const row = page.getByRole('menuitem', { name: TENANT_NAME, exact: true })
  await expect(row).toBeVisible()
  if (await row.isEnabled()) {
    await row.click()
    await expectClippedAnnouncement(
      page,
      copy('tenancyUi', 'tenantSwitcher.switchedTo', { tenant: TENANT_NAME }),
    )
  } else {
    await page.keyboard.press('Escape')
  }
  await expect(tenantSwitcher(page)).toBeVisible()
}

/**
 * The language the worker renders the notice in. The seeded member stores no
 * language preference, so the dispatch resolves to the platform default.
 */
const NOTICE_LOCALE: CopyLocale = 'en-US'

/**
 * The notice's rendered title and body, from the review catalog in the language
 * the dispatch resolved for this member.
 */
const NOTICE_TITLE = copyKey(
  'reviewNotice',
  'review.notice.in_app.title',
  NOTICE_LOCALE,
)
const NOTICE_BODY = copyKey(
  'reviewNotice',
  'review.notice.in_app.body',
  NOTICE_LOCALE,
)

/**
 * The header bell. Its accessible name is the bare label when the count is
 * zero and the labelled form otherwise, so a substring match on the bare label
 * finds it in both states.
 */
function bell(page: Page): Locator {
  return page
    .getByRole('banner')
    .getByRole('button', { name: copy('notificationUi', 'bell.label') })
}

/**
 * The count the bell currently reports, read from the accessible name the
 * product puts it in. A name that is only the bare label means zero.
 */
async function unreadCount(page: Page): Promise<number> {
  const label = await bell(page).getAttribute('aria-label')
  if (label === null || label === copy('notificationUi', 'bell.label')) {
    return 0
  }
  const match = copyPattern('notificationUi', 'bell.labelWithCount', {
    n: '(\\d+)',
  }).exec(label)
  return match?.[1] === undefined ? 0 : Number(match[1])
}

/** The inbox list on the notification surface. */
function inbox(page: Page): Locator {
  return page.getByRole('list', {
    name: copy('notificationUi', 'inbox.title'),
  })
}

/** The preference matrix on the notification surface. */
function matrix(page: Page): Locator {
  return page.getByRole('table', {
    name: copy('notificationUi', 'preferences.title'),
  })
}

/** The matrix row of the review type. */
function preferenceRow(page: Page): Locator {
  return matrix(page)
    .getByRole('row')
    .filter({ hasText: TYPE_KEY })
}

/** One channel checkbox in the review type's row. */
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

/**
 * The inbox rows currently carrying the given state chip. A row is scoped by
 * the chip's own text rather than by its position, so a notice the worker
 * prepends moves rows but not the set this matches.
 */
function rowsWithMark(
  page: Page,
  key: 'inbox.unread' | 'inbox.read',
): Locator {
  return inbox(page)
    .getByRole('listitem')
    .filter({ has: page.getByText(copy('notificationUi', key), { exact: true }) })
}

/**
 * Wait for the worker's next dispatch window to land, then answer the bell's
 * reading. The interval is ten seconds, so returning as soon as an arrival is
 * visible leaves the next window a full interval away: a count read and the
 * write after it then describe the messages already on screen, and no notice
 * arrives between them to falsify an exact count.
 */
async function afterNextNotice(page: Page): Promise<number> {
  const previous = await unreadCount(page)
  await expect
    .poll(() => unreadCount(page), {
      timeout: 25_000,
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
      identifier: reviewMember.username,
      password: reviewMember.password,
    })
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'home.heading'),
        exact: true,
      }),
    ).toBeVisible()

    // The member's account owns a personal organization, which the host settles
    // as the current organization for an ordinary member. Move to the seeded
    // organization through the switcher so the journey runs where it says.
    await switchToTenant(page)

    // Step 1: the switcher names the seeded tenant as the current organization,
    // and the row that stands for it is the disabled one once the list opens.
    const switcher = tenantSwitcher(page)
    await expect(switcher).toBeVisible()
    await expect(switcher).toBeEnabled()
    await switcher.click()
    await expect(
      page.getByRole('menuitem', { name: TENANT_NAME, exact: true }),
    ).toBeDisabled()
    await page.keyboard.press('Escape')
    await expect(
      page.getByRole('menuitem', { name: TENANT_NAME, exact: true }),
    ).toBeHidden()
  })

  await test.step("the inbox's precondition: it already holds an unread notice", async () => {
    // The journey's precondition: the inbox already holds an unread message. The
    // worker's window is ten seconds, so the first notice may still be in flight
    // when the member signs in. Wait through the bell for it -- never a bare
    // sleep -- before taking the baseline the arrival step measures against.
    await expect
      .poll(() => unreadCount(page), {
        timeout: 25_000,
        message: 'the member never received the first notice',
      })
      .toBeGreaterThanOrEqual(1)
  })

  await test.step('a notice arrives on another page and the bell counts it without a reload', async () => {
    // The member stays on the home page and the count moves on its own: no
    // reload and no interaction with the list is what carries the change. The
    // next window is ten seconds away plus the delivery job's poll, so wait
    // for the bell to change rather than for a fixed delay. Whether the move
    // came from a pushed frame or a re-read on a timer is not discriminated
    // here (see the file header on REQ-notifications-013).
    await afterNextNotice(page)

    // Opening the surface from the bell lists the notice, unread, with its
    // rendered title and body, and the bell's accessible name carries the
    // total.
    await bell(page).click()
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
    await expect(bell(page)).toHaveAccessibleName(
      copyPattern('notificationUi', 'bell.labelWithCount', { n: '\\d+' }),
    )
  })

  await test.step('marking the message read clears its mark and decrements the bell', async () => {
    const list = inbox(page)
    // The inbox holds the worker's notices only and none is read yet, so a row
    // turning read identifies the one this step marked even if the next window
    // prepends another notice.
    await expect(rowsWithMark(page, 'inbox.read')).toHaveCount(0)
    const before = await unreadCount(page)
    const newest = list.getByRole('listitem').first()
    const markRead = newest.getByRole('button', {
      name: copy('notificationUi', 'inbox.markReadAria', {
        title: NOTICE_TITLE,
      }),
    })

    await markRead.click()

    // The read is written and the row is re-read from the server: exactly one
    // row now shows the read state (the one marked) and offers no further
    // mark-read action.
    const read = rowsWithMark(page, 'inbox.read')
    await expect(read).toHaveCount(1)
    await expect(
      read.getByRole('button', {
        name: copyPattern('notificationUi', 'inbox.markReadAria', {
          title: '[^"]*',
        }),
      }),
    ).toHaveCount(0)

    // The marked row is unread no longer, so the bell's total is the number of
    // unread rows however many windows landed meanwhile. Comparing the two
    // live readings states the decrement without the exact before-1, which an
    // arrival between the count and the write would falsify.
    await expect
      .poll(
        async () =>
          (await unreadCount(page)) -
          (await rowsWithMark(page, 'inbox.unread').count()),
        {
          timeout: 10_000,
          message: 'the bell and the inbox disagree on the unread total',
        },
      )
      .toBe(0)
    await expect
      .poll(() => unreadCount(page), {
        timeout: 10_000,
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

    // The confirmation reports the server's read_count, which is authoritative;
    // the bell the step read is a client cache. A notice that landed between
    // that read and the write makes the server's count one higher, so the
    // assertion is a lower bound rather than an exact match.
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

    // Every row the click saw is now read. A notice that lands meanwhile is a
    // new unread row at the top and does not undo that; counting the read rows
    // rather than walking indices keeps the prepend from shifting which row is
    // checked.
    await expect
      .poll(() => rowsWithMark(page, 'inbox.read').count(), {
        timeout: 10_000,
        message: 'a row the mark-all click saw is still unread',
      })
      .toBeGreaterThanOrEqual(rowsBefore)

    // No unread remains, so the bell falls back to its bare label.
    await expect(bell(page)).toHaveAccessibleName(
      copy('notificationUi', 'bell.label'),
    )
  })

  await test.step('the preference matrix shows the type with its default channels', async () => {
    const table = matrix(page)
    await expect(table.getByRole('row').filter({ hasText: TYPE_KEY }))
      .toHaveCount(1)

    // The review assembly serves both optional channels, so the matrix carries
    // the type column and the platform's fixed channel order: in-app, email,
    // sms. Reading the whole ordered set from the bundle proves the set and its
    // order without depending on a column index. The claim is narrow: this
    // type declares its optional channels in the platform's own order, so the
    // reversed-declaration case of REQ-notifications-030 is not driven (see the
    // file header).
    await expect(table.getByRole('columnheader')).toHaveText([
      copy('notificationUi', 'preferences.column.type'),
      copy('notificationUi', 'preferences.channel.in_app'),
      copy('notificationUi', 'preferences.channel.email'),
      copy('notificationUi', 'preferences.channel.sms'),
    ])

    // The seeded member holds an email address and no phone number, so the
    // fixture addresses them by email and leaves sms out of the effective set:
    // in-app and email are checked, and the sms cell is not. The matrix renders
    // the effective set, not whether it came from the type's declaration.
    await expect(channelToggle(page, 'in_app')).toBeChecked()
    await expect(channelToggle(page, 'email')).toBeChecked()
    await expect(channelToggle(page, 'sms')).not.toBeChecked()
  })

  await test.step('turning the email channel off confirms the change and leaves in-app alone', async () => {
    // Write at the start of a quiet window: an in-flight notice dispatched
    // before the write would otherwise let the next step count a pre-write
    // dispatch as the post-write arrival it claims to prove. Waiting for the
    // next window first leaves no dispatch in flight when the write lands.
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

    // The matrix re-reads the server's answer: only the in-app cell remains
    // checked, and the cell that was turned off is unchecked.
    await expect(channelToggle(page, 'email')).not.toBeChecked()
    await expect(channelToggle(page, 'in_app')).toBeChecked()
  })

  await test.step('a later notice still arrives in-app with the email channel off', async () => {
    const list = inbox(page)
    const rows = list.getByRole('listitem')
    const rowsBefore = await rows.count()
    // The email write landed at the start of a quiet window, so this reading is
    // post-write and the increase the step waits for is a notice dispatched
    // after it -- not an earlier dispatch whose delivery job ran late. Delivery
    // resolves the stored preference at execution time, so that notice writes
    // the in-app row only.
    const before = await unreadCount(page)

    // The next window dispatches one more notice. The rendered proof is a
    // higher count and a new row at the top of the list. (The mailbox side has
    // no browser surface -- see the file header.)
    await expect
      .poll(() => unreadCount(page), {
        timeout: 25_000,
        message: 'no notice reached the bell after the email channel was turned off',
      })
      .toBeGreaterThan(before)

    await expect
      .poll(() => rows.count(), {
        timeout: 15_000,
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

    // The effective set is still the in-app channel alone.
    await expect(channelToggle(page, 'email')).not.toBeChecked()
    await expect(channelToggle(page, 'in_app')).toBeChecked()
  })
})
