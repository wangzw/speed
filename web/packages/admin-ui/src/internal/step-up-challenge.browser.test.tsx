/**
 * StepUpChallenge's dialog layout in a real browser: the cancel and the
 * primary exit (restart/relogin) are one group, one spacing token apart,
 * with the primary closing the row at its right edge, and the dialog body
 * is one vertical stack whose theme gap keeps a failure alert clear of the
 * description above it and the step's first field below. The exits used to
 * render through MUI's DialogActions; that band already put 8px (one
 * spacing token) between adjacent buttons, but it owned the band's layout.
 * The exits now render through the shared action row with the same padding
 * the band used, so the gap and the right edge are the row's own. jsdom
 * lays nothing out, so only this tier can measure either.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import { ApiError } from '@speed/api-client'
import type { AuthSession, AuthnFlowState } from '@speed/auth-core'
import { renderWithProviders } from '../../test-utils/render.js'
import { bindAdminRequest } from '../../test-utils/request-rig.js'
import { createStepUpSession } from '../../test-utils/session.js'
import { StepUpChallenge } from './step-up-challenge.js'
import enUS from '../locales/en-US.json' with { type: 'json' }

/** The one spacing token an action row puts between adjacent controls. */
const ACTION_GAP = 8

const ACTIVE_FACTOR = {
  id: 'factor-1',
  kind: 'totp',
  label: 'Authenticator app',
  status: 'active',
  created_at: '2026-01-01T00:00:00Z',
}

describe('StepUpChallenge actions in Chromium', () => {
  it('keeps cancel and the primary exit one token apart, primary rightmost', async () => {
    await page.viewport(900, 900)
    bindAdminRequest((call) => {
      if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
        return { factors: [ACTIVE_FACTOR] }
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    })
    {
      const base = createStepUpSession()
      const session: AuthSession = {
        ...base,
        startAuthenticatedFlow: vi.fn().mockRejectedValue({
          status: 503,
          code: 'authn.unavailable',
          message: 'down',
          auth: false,
        }),
      }
      renderWithProviders(
        <StepUpChallenge
          open
          session={session}
          onSuccess={vi.fn()}
          onCancel={vi.fn()}
        />,
        { language: 'en-US' },
      )

      // A failed start leaves no step: the failed code's banner, the
      // restart primary and the cancel are the whole surface.
      await screen.findByRole('alert')
      const cancel = screen.getByRole('button', { name: enUS.common.cancel })
      const restart = screen.getByRole('button', { name: enUS.stepUp.restart })
      const cancelBox = cancel.getBoundingClientRect()
      const restartBox = restart.getBoundingClientRect()

      expect(Math.abs(cancelBox.top - restartBox.top)).toBeLessThanOrEqual(1)
      expect(restartBox.left - cancelBox.right).toBeCloseTo(ACTION_GAP, 0)

      // The exits belong to the row the row itself marks, and the primary
      // closes that row at its right content edge.
      const row = restart.closest<HTMLElement>('[data-action-row]')
      expect(row).not.toBeNull()
      expect(cancel.closest('[data-action-row]')).toBe(row)
      const rowBox = (row as HTMLElement).getBoundingClientRect()
      const contentRight =
        rowBox.right -
        Number.parseFloat(getComputedStyle(row as HTMLElement).paddingRight)
      expect(Math.abs(restartBox.right - contentRight)).toBeLessThanOrEqual(1)
    }
  })

  it('leaves a visible gap between the failure alert and the first field', async () => {
    // The dialog body is one vertical stack with a theme.spacing(2) gap:
    // the failure alert must not sit flush against the description above
    // it or the step's first field below. jsdom lays nothing out, so only
    // this tier can measure the gap.
    await page.viewport(900, 900)
    bindAdminRequest((call) => {
      if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
        return { factors: [ACTIVE_FACTOR] }
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    })
    const base = createStepUpSession()
    const session: AuthSession = {
      ...base,
      startAuthenticatedFlow: () =>
        Promise.resolve<AuthnFlowState>({
          seq: 1,
          state: 'first',
          next: {
            kind: 'challenge',
            fields: [{ name: 'code', kind: 'code', required: true }],
          },
        }),
      // A refused answer keeps the step, so the alert and the field stand
      // together for the measurement.
      answerFlow: () =>
        Promise.reject(
          new ApiError({
            status: 400,
            code: 'authn.login_failed',
            attempts: 1,
          }),
        ),
    }
    const user = userEvent.setup()
    renderWithProviders(
      <StepUpChallenge
        open
        session={session}
        onSuccess={vi.fn()}
        onCancel={vi.fn()}
      />,
      { language: 'en-US' },
    )

    // A challenge field is labelled by its own name (`code`, marked
    // required).
    await user.type(await screen.findByLabelText(/code/i), '123456')
    await user.click(screen.getByRole('button', { name: enUS.stepUp.submit }))

    const alert = await screen.findByRole('alert')
    const description = screen.getByText(enUS.stepUp.description)
    const field = screen.getByLabelText(/code/i)
    const alertBox = alert.getBoundingClientRect()
    const descriptionBox = description.getBoundingClientRect()
    const fieldBox = field.getBoundingClientRect()
    // One theme spacing token (8px x 2) is the smallest intended gap; the
    // step form adds its own margin below the body stack.
    expect(alertBox.height).toBeGreaterThan(0)
    expect(alertBox.top - descriptionBox.bottom).toBeGreaterThanOrEqual(16)
    expect(fieldBox.top - alertBox.bottom).toBeGreaterThanOrEqual(16)
  })
})
