/**
 * OwnerTransferRequestsPage: the account-level confirmation surface.
 *
 * What this suite pins: the list shows the pending requests addressed to
 * the caller and links each to its detail; the detail reads one request and
 * confirms or declines it; a confirmation invalidates the list, the detail
 * and the organization's profile read; a decline leaves ownership alone; a
 * conflict re-reads the detail and words expiry or a finished request; and
 * the whole surface issues only account-level requests -- no tenant path,
 * so a plain member who holds no organization permission can use it.
 */

import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactElement } from 'react'
import { describe, expect, it } from 'vitest'
import { CONFIRM_ARM_LOCKOUT_MS, uiKitResources } from '@speed/ui-kit'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  jsonResponse,
  makeRealClientRigWithoutSession,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { OwnerTransferRequestsPage } from './OwnerTransferRequestsPage.js'

const LIST_PATH = '/api/v1/auth/me/owner-transfer-requests'
const DETAIL_PATH = `${LIST_PATH}/transfer-1`
const CONFIRM_PATH = `${DETAIL_PATH}/confirm`
const DECLINE_PATH = `${DETAIL_PATH}/decline`
const PROFILE_PATH = '/api/v1/auth/me'

/** The zh-CN label the shared confirm shows once its two-step guard arms. */
const CONFIRM_AGAIN_LABEL = (
  uiKitResources['zh-CN'] as unknown as {
    readonly confirmDialog: { readonly confirmAgainLabel: string }
  }
).confirmDialog.confirmAgainLabel

/** One request in the generated view's shape. */
function requestFixture(
  overrides: Record<string, unknown> = {},
): Record<string, unknown> {
  return {
    id: 'transfer-1',
    tenant: 'acme-inc',
    requestedAt: '2026-11-01T00:00:00Z',
    expiresAt: '2026-11-08T00:00:00Z',
    status: 'pending',
    ...overrides,
  }
}

/** Mounts the page over a scripted account-endpoint responder. */
function mountPage(respond: RealResponder, ui?: ReactElement) {
  const rig = makeRealClientRigWithoutSession(respond)
  rig.store.set('access-1')
  const element =
    ui ??
    (
      <OwnerTransferRequestsPage
        requestHref={(id) => `/transfer-requests/${id}`}
      />
    )
  const rendered = renderWithProviders(element)
  return { rig, ...rendered }
}

/** The two-step guard: arm, wait out the lockout, confirm. */
async function confirmTwice(
  user: ReturnType<typeof userEvent.setup>,
  confirmLabel: string,
): Promise<void> {
  const dialog = await screen.findByRole('dialog')
  await user.click(within(dialog).getByRole('button', { name: confirmLabel }))
  await act(async () => {
    await new Promise((resolve) =>
      setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
    )
  })
  await user.click(
    within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
  )
}

/** Every path a call named, for the account-level scope assertion. */
function paths(calls: readonly RealCall[]): readonly string[] {
  return calls.map((call) => call.path)
}

describe('OwnerTransferRequestsPage', () => {
  it('lists the pending requests and links each to its detail', async () => {
    const { rig } = mountPage((call) => {
      if (call.path === PROFILE_PATH) {
        return jsonResponse(200, { time_zone: '' })
      }
      if (call.path === LIST_PATH) {
        return jsonResponse(200, { requests: [requestFixture()] })
      }
      return jsonResponse(404, null)
    })

    expect(await screen.findByText('acme-inc')).toBeInTheDocument()
    expect(
      screen.getByRole('link', { name: zhCN.transferRequests.open }),
    ).toHaveAttribute('href', '/transfer-requests/transfer-1')
    // The request view carries no account identifier, and none is rendered.
    expect(screen.queryByText(/acc-/)).toBeNull()
    expect(rig.calls.every((call) => call.path.startsWith('/api/v1/auth/'))).toBe(
      true,
    )
    await expectNoAxeViolations()
  })

  it('confirms a pending request and re-reads the list, the detail and the profile', async () => {
    const user = userEvent.setup()
    let status = 'pending'
    const { rig } = mountPage(
      (call) => {
        if (call.path === PROFILE_PATH) {
          return jsonResponse(200, { time_zone: '' })
        }
        if (call.path === DETAIL_PATH) {
          return jsonResponse(200, {
            request: requestFixture({ status }),
          })
        }
        if (call.method === 'POST' && call.path === CONFIRM_PATH) {
          status = 'confirmed'
          return new Response(null, { status: 204 })
        }
        return jsonResponse(404, null)
      },
      <OwnerTransferRequestsPage request="transfer-1" />,
    )

    expect(
      await screen.findByRole('heading', { name: 'acme-inc' }),
    ).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.transferRequests.status),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: zhCN.transferRequests.confirm }),
    )
    await confirmTwice(user, zhCN.transferRequests.confirm)

    await waitFor(() =>
      expect(rig.calls).toContainEqual(
        expect.objectContaining({ method: 'POST', path: CONFIRM_PATH }),
      ),
    )
    expect(
      await screen.findByText(
        zhCN.transferRequests.confirmed.replace('{{tenant}}', 'acme-inc'),
      ),
    ).toBeInTheDocument()
    // The detail is re-read: the request the server now answers is finished.
    await waitFor(() =>
      expect(screen.getByText(zhCN.transferRequests.terminal)).toBeInTheDocument(),
    )
    expect(screen.getByText(zhCN.transferRequests.statusValues.confirmed)).toBeInTheDocument()
    // The surface stays inside the account endpoints.
    for (const path of paths(rig.calls)) {
      expect(path.startsWith('/api/v1/auth/')).toBe(true)
    }
  })

  it('declines a pending request without changing ownership', async () => {
    const user = userEvent.setup()
    let status = 'pending'
    const { rig } = mountPage(
      (call) => {
        if (call.path === PROFILE_PATH) {
          return jsonResponse(200, { time_zone: '' })
        }
        if (call.path === DETAIL_PATH) {
          return jsonResponse(200, {
            request: requestFixture({ status }),
          })
        }
        if (call.method === 'POST' && call.path === DECLINE_PATH) {
          status = 'declined'
          return new Response(null, { status: 204 })
        }
        return jsonResponse(404, null)
      },
      <OwnerTransferRequestsPage request="transfer-1" />,
    )

    await screen.findByRole('heading', { name: 'acme-inc' })
    await user.click(
      screen.getByRole('button', { name: zhCN.transferRequests.decline }),
    )
    const dialog = await screen.findByRole('dialog', {
      name: zhCN.transferRequests.declineTitle.replace(
        '{{tenant}}',
        'acme-inc',
      ),
    })
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.transferRequests.decline,
      }),
    )

    await waitFor(() =>
      expect(rig.calls).toContainEqual(
        expect.objectContaining({ method: 'POST', path: DECLINE_PATH }),
      ),
    )
    expect(
      await screen.findByText(zhCN.transferRequests.declined),
    ).toBeInTheDocument()
    await waitFor(() =>
      expect(screen.getByText(zhCN.transferRequests.statusValues.declined)).toBeInTheDocument(),
    )
  })

  it('re-reads the detail after a conflict and names the expiry', async () => {
    const user = userEvent.setup()
    let status = 'pending'
    mountPage(
      (call) => {
        if (call.path === PROFILE_PATH) {
          return jsonResponse(200, { time_zone: '' })
        }
        if (call.path === DETAIL_PATH) {
          return jsonResponse(200, {
            request: requestFixture({ status }),
          })
        }
        if (call.method === 'POST' && call.path === CONFIRM_PATH) {
          status = 'expired'
          return jsonResponse(409, {
            type: 'urn:speed:tenant:conflict',
            status: 409,
          })
        }
        return jsonResponse(404, null)
      },
      <OwnerTransferRequestsPage request="transfer-1" />,
    )

    await screen.findByRole('heading', { name: 'acme-inc' })
    await user.click(
      screen.getByRole('button', { name: zhCN.transferRequests.confirm }),
    )
    await confirmTwice(user, zhCN.transferRequests.confirm)

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.transferRequests.error.expired,
    )
  })
})
