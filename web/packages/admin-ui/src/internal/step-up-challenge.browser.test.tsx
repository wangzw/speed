/**
 * StepUpChallenge's dialog action row in a real browser: the cancel and
 * the primary exit (restart/relogin) are one group, one spacing token
 * apart, with the primary closing the row at its right edge. The dialog
 * used to lay its exits out through MUI's DialogActions; that band already
 * put 8px (one spacing token) between adjacent buttons, but it owned the
 * band's layout. The exits now render through the shared action row with
 * the same padding the band used, so the gap and the right edge are the
 * row's own. jsdom lays nothing out, so only this tier can measure them.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import { page } from 'vitest/browser'
import type { AuthSession } from '@speed/auth-core'
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
})
