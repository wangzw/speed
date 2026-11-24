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
import { setCurrentTenant, setTenantNames } from './chrome.js'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from './test-utils/session.js'

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

function triggerName(count: string): string {
  const displayName = authenticatedSnapshot().principal?.display_name ?? ''
  const phrase = tNotification('bell.labelWithCount', { n: count })
  return demoEnUS.userMenu.triggerWithNotifications
    .replace('{{account}}', `${avatarInitials(displayName)} ${displayName}`)
    .replace('{{notifications}}', phrase)
}

beforeEach(() => {
  setCurrentTenant('acme')
  setTenantNames(new Map([['acme', 'acme']]))
})

describe('the account menu over one unread notification', () => {
  it('badges the avatar and carries the count in its name', async () => {
    bindRequestFn(createBackend('data'))
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    const trigger = await screen.findByTestId('user-menu-trigger')
    await waitFor(() =>
      expect(trigger).toHaveAccessibleName(triggerName('1')),
    )
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

    const row = await screen.findByRole('menuitem', {
      name: demoEnUS.userMenu.messagesWithCount.replace('{{n}}', '1'),
    })
    expect(row).toHaveTextContent('1')
  })
})
