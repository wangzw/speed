/**
 * The review build's tenant notice control.
 *
 * Two things are held here. The gating is run-time: the control is offered
 * exactly while the feature document carries its review-only name, which only
 * the review assembly declares, so a baseline deployment shows nothing. The
 * click posts one request to the review-only route and reports the outcome
 * through the write-feedback region, with the copy read from the app's locale
 * bundle rather than a literal.
 */

import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import type { RequestOptions } from '@speed/api-client'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { REVIEW_NOTICE_CONTROL_PATH } from './app-api/review-notice.js'
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from './test-utils/session.js'

beforeEach(() => {
  window.history.replaceState({}, '', '/')
})

/** An authenticated session whose feature document carries exactly `names`. */
function sessionWith(...names: string[]) {
  return createTestSession(
    authenticatedSnapshot({ features: readyFeatures(...names) }),
  )
}

/** The control's heading, which is the section's accessible name. */
function controlHeading(): HTMLElement | null {
  return screen.queryByRole('heading', {
    level: 2,
    name: demoEnUS.home.reviewNotice.title,
  })
}

describe('the review-only notice control', () => {
  it('is offered when the review assembly publishes its feature', async () => {
    renderDemoApp({ session: sessionWith('review.notification.control') })

    expect(
      await screen.findByRole('heading', {
        level: 2,
        name: demoEnUS.home.reviewNotice.title,
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByText(demoEnUS.home.reviewNotice.description),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', {
        name: demoEnUS.home.reviewNotice.action,
      }),
    ).toBeInTheDocument()
  })

  it('is absent when the deployment does not serve its feature', () => {
    renderDemoApp({ session: sessionWith('notification.inbox') })

    expect(controlHeading()).not.toBeInTheDocument()
  })

  it('posts the review-only route and confirms the dispatch', async () => {
    const calls: { path: string; method: string | undefined }[] = []
    bindRequestFn(<T,>(path: string, options?: RequestOptions): Promise<T> => {
      calls.push({ path, method: options?.method })
      return Promise.resolve(undefined as T)
    })
    renderDemoApp({ session: sessionWith('review.notification.control') })

    await userEvent.click(
      await screen.findByRole('button', {
        name: demoEnUS.home.reviewNotice.action,
      }),
    )

    await waitFor(() => {
      expect(
        calls.filter((call) => call.path === REVIEW_NOTICE_CONTROL_PATH),
      ).toEqual([{ path: REVIEW_NOTICE_CONTROL_PATH, method: 'POST' }])
    })
    expect(
      await screen.findByText(demoEnUS.home.reviewNotice.sent),
    ).toBeInTheDocument()
  })
})
