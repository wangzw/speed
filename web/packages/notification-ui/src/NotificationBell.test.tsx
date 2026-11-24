import { describe, expect, it, vi } from 'vitest'
import { useState } from 'react'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  jsonResponse,
  makeRealClientRigWithoutSession,
  type RealResponder,
} from '../test-utils/real-client.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import { renderWithProviders } from '../test-utils/render.js'
import {
  expectNoAxeViolations,
} from '../test-utils/axe.js'
import { NotificationBell } from './NotificationBell.js'

describe('NotificationBell', () => {
  it('shows the unread count on the accessible name and the badge, and passes axe', async () => {
    const respond: RealResponder = () => jsonResponse(200, { unread: 3 })
    const rig = makeRealClientRigWithoutSession(respond)
    rig.store.set('access-1')
    const onClick = vi.fn()
    renderWithProviders(<NotificationBell onClick={onClick} />)

    const button = await screen.findByRole('button', {
      name: zhCN.bell.labelWithCount.replace('{{n}}', '3'),
    })
    expect(button).toBeInTheDocument()
    expect(button).toHaveTextContent('3')
    await userEvent.click(button)
    expect(onClick).toHaveBeenCalledTimes(1)
    await expectNoAxeViolations({
      decidedInBrowser: 'notificationBellUnread',
    })
  })

  it('renders a host icon and hands the activation event to onClick', async () => {
    const respond: RealResponder = () => jsonResponse(200, { unread: 0 })
    const rig = makeRealClientRigWithoutSession(respond)
    rig.store.set('access-1')
    const onClick = vi.fn()
    renderWithProviders(
      <NotificationBell
        onClick={onClick}
        icon={<span data-testid="host-icon">*</span>}
      />,
    )
    const button = await screen.findByRole('button', { name: zhCN.bell.label })
    expect(within(button).getByTestId('host-icon')).toBeInTheDocument()
    await userEvent.click(button)
    expect(onClick).toHaveBeenCalledTimes(1)
    const event: unknown = onClick.mock.calls[0]?.[0]
    expect(event).toHaveProperty('type', 'click')
  })

  it('renders the bare label with no badge when nothing is unread', async () => {
    const respond: RealResponder = () => jsonResponse(200, { unread: 0 })
    const rig = makeRealClientRigWithoutSession(respond)
    rig.store.set('access-1')
    function Harness() {
      const [, bump] = useState(0)
      return (
        <>
          <button type="button" onClick={() => bump((value) => value + 1)}>
            bump
          </button>
          <NotificationBell onClick={() => undefined} />
        </>
      )
    }
    const user = userEvent.setup()
    renderWithProviders(<Harness />)
    const button = await screen.findByRole('button', { name: zhCN.bell.label })
    await user.click(screen.getByRole('button', { name: 'bump' }))
    expect(button.textContent).toBe('')
  })

  it('caps the printed count at 99+ and carries the same text in the name', async () => {
    const respond: RealResponder = () => jsonResponse(200, { unread: 150 })
    const rig = makeRealClientRigWithoutSession(respond)
    rig.store.set('access-1')
    renderWithProviders(<NotificationBell onClick={() => undefined} />)

    const button = await screen.findByRole('button', {
      name: zhCN.bell.labelWithCount.replace('{{n}}', '99+'),
    })
    expect(button).toHaveTextContent('99+')
  })

  it('stays at zero when the count read fails', async () => {
    const respond: RealResponder = () => {
      throw new Error('network down')
    }
    const rig = makeRealClientRigWithoutSession(respond)
    rig.store.set('access-1')
    renderWithProviders(<NotificationBell onClick={() => undefined} />)
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: zhCN.bell.label }),
      ).toBeInTheDocument(),
    )
  })

  it('raises the bell to the 44px touch-target floor under a coarse pointer', async () => {
    const respond: RealResponder = () => jsonResponse(200, { unread: 0 })
    const rig = makeRealClientRigWithoutSession(respond)
    rig.store.set('access-1')
    renderWithProviders(<NotificationBell onClick={() => undefined} />)
    const button = await screen.findByRole('button', { name: zhCN.bell.label })
    expect(hasCoarsePointerTouchTarget(button)).toBe(true)
  })
})
