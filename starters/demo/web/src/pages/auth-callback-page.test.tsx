/**
 * The auth callback route's landmark structure.
 *
 * The route renders outside the tenant shell, so the page itself is the
 * document's main landmark -- except when the host's absent view shows: that
 * view is the tenant frame's 404, and the frame renders the main landmark
 * itself. Wrapping it in the page's own main would put two `main` landmarks on
 * one document, a defect a screen reader's landmark list surfaces as a
 * duplicate. The sweep's axe gate runs landmark-no-duplicate-main, but this
 * case pins the structure directly so a regression is named at the route that
 * carries it rather than only in a scan report.
 */

import { act, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { createBackend } from '../a11y/backends.js'
import {
  setCurrentTenantId,
  setTenantAliases,
} from '../chrome.js'
import { AUTH_CALLBACK_PATH } from '../routes.js'
import { renderDemoApp } from '../test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from '../test-utils/session.js'

/** The tenant the callback route renders over, with the alias the host has
 * recorded for it; the host holds no default roster, so a current tenant must
 * be nameable. */
const TENANT_ID = 'acme'

beforeEach(() => {
  window.history.replaceState({}, '', AUTH_CALLBACK_PATH)
  setCurrentTenantId(TENANT_ID)
  setTenantAliases(new Map([[TENANT_ID, TENANT_ID]]))
})

describe('the auth callback route', () => {
  it("renders exactly one main landmark when the host's absent view shows", async () => {
    bindRequestFn(createBackend('data'))
    // An authenticated session answers the discovery read with the skeleton's
    // password-only document -- no redirect or enterprise method -- so the
    // handler settles on the host's absent view: the tenant frame's 404.
    const { container } = renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
    })

    expect(
      await screen.findByRole('heading', { level: 1, name: 'Page not found' }),
    ).toBeInTheDocument()
    // Let the frame's own post-mount reads settle inside act, so the count
    // below is the structure the route rests at, not a transient commit.
    await act(async () => {
      await Promise.resolve()
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(container.querySelectorAll('main')).toHaveLength(1)
  })
})
