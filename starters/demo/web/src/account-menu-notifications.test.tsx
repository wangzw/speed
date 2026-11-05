/**
 * The account menu's unread notification surfaces: the avatar's badge and the
 * notification-centre row's count.
 *
 * Both read one unread count, both paint it as visible text, and both carry it
 * in their accessible name -- a visible count outside the name fails WCAG
 * 2.5.3, and a screen reader should hear the row as one sentence rather than
 * its label plus a stray number. At zero unread neither paints and the names
 * are the plain labels.
 */

import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { avatarInitials } from '@speed/layout-kit'
import {
  NOTIFICATION_UI_NAMESPACE,
  notificationUiResources,
} from '@speed/notification-ui'
import { createSpeedI18n } from '@speed/product-shell/bootstrap'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { createBackend } from './a11y/backends.js'
import { setCurrentTenantId, setTenantAliases } from './chrome.js'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from './test-utils/session.js'

/** The notification-ui copy, resolved from the shipped bundle. */
const textI18n = createSpeedI18n({
  supportedLanguages: ['zh-CN', 'en-US'],
  defaultLanguage: 'en-US',
  storage: null,
  urlParameterName: null,
  navigatorLanguages: [],
  namespaces: [
    { namespace: NOTIFICATION_UI_NAMESPACE, resources: notificationUiResources },
  ],
})
const tNotification = textI18n.getFixedT('en-US', NOTIFICATION_UI_NAMESPACE)

/** The avatar trigger's accessible name once the unread phrase is appended. */
function triggerName(count: string): string {
  const displayName = authenticatedSnapshot().principal?.display_name ?? ''
  const phrase = tNotification('bell.labelWithCount', { n: count })
  return demoEnUS.userMenu.triggerWithNotifications
    .replace('{{account}}', `${avatarInitials(displayName)} ${displayName}`)
    .replace('{{notifications}}', phrase)
}

beforeEach(() => {
  setCurrentTenantId('acme')
  setTenantAliases(new Map([['acme', 'acme']]))
})

describe('the account menu over one unread notification', () => {
  it('badges the avatar and carries the count in its name', async () => {
    bindRequestFn(createBackend('data'))
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    const trigger = await screen.findByTestId('user-menu-trigger')
    await waitFor(() =>
      expect(trigger).toHaveAccessibleName(triggerName('1')),
    )
    // The count is painted beside the trigger, not inside it: a digit inside
    // the control would be visible text its accessible name must contain.
    const wrapper = trigger.parentElement as HTMLElement
    expect(within(wrapper).getByText('1')).toBeInTheDocument()
    expect(trigger).not.toHaveTextContent('1')
  })

  it('counts the notification-centre row and carries the count in its name', async () => {
    bindRequestFn(createBackend('data'))
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    const trigger = await screen.findByTestId('user-menu-trigger')
    await waitFor(() =>
      expect(trigger).toHaveAccessibleName(triggerName('1')),
    )
    await userEvent.click(trigger)

    // The row is one sentence to a screen reader: the count is inside the
    // name, not a stray number after it.
    const row = await screen.findByRole('menuitem', {
      name: demoEnUS.userMenu.messagesWithCount.replace('{{n}}', '1'),
    })
    expect(row).toHaveTextContent('1')
  })
})
