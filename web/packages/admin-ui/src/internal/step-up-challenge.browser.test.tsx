import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import { ApiError } from '@speed/api-client'
import type { AuthSession, AuthnFlowState } from '@speed/auth-core'
import { renderWithProviders } from '../../test-utils/render.js'
import { bindAdminRequest } from '../../test-utils/request-rig.js'
import { createStepUpSession } from '../../test-utils/session.js'
import { StepUpChallenge } from './step-up-challenge.js'
import enUS from '../locales/en-US.json' with { type: 'json' }
import zhCN from '../locales/zh-CN.json' with { type: 'json' }

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

      await screen.findByRole('alert')
      const cancel = screen.getByRole('button', { name: enUS.common.cancel })
      const restart = screen.getByRole('button', { name: enUS.stepUp.restart })
      const cancelBox = cancel.getBoundingClientRect()
      const restartBox = restart.getBoundingClientRect()

      expect(Math.abs(cancelBox.top - restartBox.top)).toBeLessThanOrEqual(1)
      expect(restartBox.left - cancelBox.right).toBeCloseTo(ACTION_GAP, 0)

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

    await user.type(await screen.findByLabelText(/code/i), '123456')
    await user.click(screen.getByRole('button', { name: enUS.stepUp.submit }))

    const alert = await screen.findByRole('alert')
    const description = screen.getByText(enUS.stepUp.description)
    const field = screen.getByLabelText(/code/i)
    const alertBox = alert.getBoundingClientRect()
    const descriptionBox = description.getBoundingClientRect()
    const fieldBox = field.getBoundingClientRect()
    // jsdom lays nothing out, so only this tier can measure the gap.
    expect(alertBox.height).toBeGreaterThan(0)
    expect(alertBox.top - descriptionBox.bottom).toBeGreaterThanOrEqual(16)
    expect(fieldBox.top - alertBox.bottom).toBeGreaterThanOrEqual(16)
  })
})

function isUnavailable(control: HTMLElement): boolean {
  return (
    control.hasAttribute('disabled') ||
    control.getAttribute('aria-disabled') === 'true'
  )
}

describe('StepUpChallenge focus in Chromium', () => {
  it('keeps focus on the chosen factor while its answer is in flight', async () => {
    await page.viewport(900, 900)
    bindAdminRequest((call) => {
      if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
        return { factors: [ACTIVE_FACTOR] }
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    })
    let answering = false
    const session: AuthSession = {
      ...createStepUpSession(),
      startAuthenticatedFlow: () =>
        Promise.resolve<AuthnFlowState>({
          seq: 1,
          state: 'second',
          next: { kind: 'factor', factors: [{ id: 'f1', kind: 'totp' }] },
        }),
      answerFlow: () => {
        answering = true
        return new Promise<AuthnFlowState>(() => {})
      },
    }
    const user = userEvent.setup()
    renderWithProviders(
      <StepUpChallenge
        open
        session={session}
        onSuccess={vi.fn()}
        onCancel={vi.fn()}
      />,
      { language: 'zh-CN' },
    )

    const totp = await screen.findByRole('button', {
      name: zhCN.stepUp.factorKind.totp,
    })
    await user.click(totp)
    await waitFor(() => expect(answering).toBe(true))
    await waitFor(() => expect(isUnavailable(totp)).toBe(true))

    expect(document.activeElement).toBe(totp)
  })

  it('keeps focus on the submit while a throttled answer’s wait runs', async () => {
    await page.viewport(900, 900)
    bindAdminRequest((call) => {
      if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
        return { factors: [ACTIVE_FACTOR] }
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    })
    let answered = 0
    const session: AuthSession = {
      ...createStepUpSession(),
      startAuthenticatedFlow: () =>
        Promise.resolve<AuthnFlowState>({
          seq: 1,
          state: 'second',
          next: { kind: 'factor', factors: [{ id: 'f1', kind: 'totp' }] },
        }),
      answerFlow: () => {
        answered += 1
        if (answered === 1) {
          return Promise.resolve<AuthnFlowState>({
            seq: 2,
            state: 'second',
            next: {
              kind: 'challenge',
              fields: [{ name: 'code', kind: 'code', required: true }],
            },
          })
        }
        return Promise.reject(
          new ApiError({
            status: 429,
            code: 'ratelimit.throttled',
            attempts: 1,
            retryAfterMs: 30_000,
          }),
        )
      },
    }
    const user = userEvent.setup()
    renderWithProviders(
      <StepUpChallenge
        open
        session={session}
        onSuccess={vi.fn()}
        onCancel={vi.fn()}
      />,
      { language: 'zh-CN' },
    )

    await user.click(
      await screen.findByRole('button', { name: zhCN.stepUp.factorKind.totp }),
    )
    await user.type(await screen.findByLabelText(/code/i), '123456')
    const submit = screen.getByRole('button', { name: zhCN.stepUp.submit })
    await user.click(submit)
    await screen.findByText(
      new RegExp(zhCN.throttle.countdown.replace('{{seconds}}', '\\d+')),
    )
    await waitFor(() => expect(isUnavailable(submit)).toBe(true))

    expect(document.activeElement).toBe(submit)
  })
})
