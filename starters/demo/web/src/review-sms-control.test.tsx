/**
 * The review build's SMS verdict control.
 *
 * Three things are held here. The gating is run-time: the control is offered
 * exactly while the feature document carries its review-only name, which only
 * the review assembly declares, so a baseline deployment shows nothing. The
 * control sits beside the admin package's SMS page, not in it. The click
 * posts one verdict to the review-only route and reports the outcome through the
 * write-feedback region, with the copy read from the app's locale bundle rather
 * than a literal.
 */

import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import type { RequestOptions } from '@speed/api-client'
import { ADMIN_UI_NAMESPACE } from '@speed/admin-ui'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { REVIEW_SMS_VERDICT_PATH } from './app-api/review-sms.js'
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from './test-utils/session.js'

const CODE = 'review-notice-en'

beforeEach(() => {
  window.history.replaceState({}, '', '/admin/sms')
})

/** An authenticated session with the SMS admin permission and the given
 * feature document. */
function sessionWith(...names: string[]) {
  return createTestSession(
    authenticatedSnapshot({
      system: ['sms.template.read', 'sms.template.manage'],
      features: readyFeatures(...names),
    }),
  )
}

/**
 * The template list read the page makes on mount. The control does not depend
 * on it, and the suite must not reach a network, so it answers an empty page.
 */
function listStub<T>(path: string, options?: RequestOptions): Promise<T> {
  if (
    path.startsWith('/api/v1/system/sms/templates') &&
    (options?.method ?? 'GET') === 'GET'
  ) {
    return Promise.resolve({ templates: [] } as T)
  }
  return Promise.resolve(undefined as T)
}

/** The control's section, whose title is its accessible name. */
function control(): Promise<HTMLElement> {
  return screen.findByRole('region', { name: demoEnUS.smsReview.title })
}

describe('the review-only SMS verdict control', () => {
  it('is offered on the SMS page when the review assembly publishes its feature', async () => {
    bindRequestFn(listStub)
    renderDemoApp({ session: sessionWith('sms.admin', 'review.sms.control') })

    const section = await control()
    expect(
      within(section).getByText(demoEnUS.smsReview.description),
    ).toBeInTheDocument()
    expect(
      within(section).getByRole('textbox', {
        name: demoEnUS.smsReview.codeLabel,
      }),
    ).toBeInTheDocument()
    expect(
      within(section).getByRole('combobox', {
        name: demoEnUS.smsReview.stateLabel,
      }),
    ).toBeInTheDocument()
    expect(
      within(section).getByRole('button', {
        name: demoEnUS.smsReview.submit,
      }),
    ).toBeInTheDocument()
  })

  it('is absent when the deployment does not serve its feature', async () => {
    bindRequestFn(listStub)
    const { i18n } = renderDemoApp({ session: sessionWith('sms.admin') })

    // The page itself renders, so the absence below is the control's gate rather
    // than a page that never mounted. The page's title is the admin package's
    // copy, resolved through the same instance the tree renders with.
    const smsTitle = i18n.t('sms.title', { ns: ADMIN_UI_NAMESPACE })
    expect(
      await screen.findByRole('heading', { name: smsTitle }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('region', { name: demoEnUS.smsReview.title }),
    ).not.toBeInTheDocument()
  })

  it('posts one vendor verdict and confirms it', async () => {
    const calls: Array<{
      path: string
      method: string | undefined
      body: unknown
    }> = []
    bindRequestFn(<T,>(path: string, options?: RequestOptions): Promise<T> => {
      calls.push({ path, method: options?.method, body: options?.body })
      return listStub<T>(path, options)
    })
    renderDemoApp({ session: sessionWith('sms.admin', 'review.sms.control') })

    const section = await control()
    const user = userEvent.setup()
    await user.type(
      within(section).getByRole('textbox', {
        name: demoEnUS.smsReview.codeLabel,
      }),
      CODE,
    )
    await user.selectOptions(
      within(section).getByRole('combobox', {
        name: demoEnUS.smsReview.stateLabel,
      }),
      'approved',
    )
    await user.click(
      within(section).getByRole('button', {
        name: demoEnUS.smsReview.submit,
      }),
    )

    await waitFor(() => {
      expect(
        calls.filter((call) => call.path === REVIEW_SMS_VERDICT_PATH),
      ).toEqual([
        {
          path: REVIEW_SMS_VERDICT_PATH,
          method: 'POST',
          body: { code: CODE, state: 'approved' },
        },
      ])
    })
    const wanted = demoEnUS.smsReview.set
      .replace('{{code}}', CODE)
      .replace('{{state}}', demoEnUS.smsReview.state.approved)
    expect(await screen.findByText(wanted)).toBeInTheDocument()
  })
})
