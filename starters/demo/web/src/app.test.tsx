/**
 * The app composition at the root path: a fresh visitor (no session attached)
 * meets the tenant shell's sign-in surface, not a business page. The tenant
 * shell fail-closes to its anonymous branch, which renders the host's sign-in
 * slot -- auth-ui's sign-in family over the assembled session -- in the active
 * language.
 */

import { screen } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import authUiEnUS from '../../../../web/packages/auth-ui/src/locales/en-US.json' with { type: 'json' }
import authUiZhCN from '../../../../web/packages/auth-ui/src/locales/zh-CN.json' with { type: 'json' }
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { renderDemoApp } from './test-utils/render.js'

beforeEach(() => {
  window.history.replaceState({}, '', '/')
})

describe('the app at the root path', () => {
  it('shows the sign-in surface to a visitor without a session', () => {
    renderDemoApp()

    // The sign-in family's channel tabs render from the auth-ui namespace.
    expect(
      screen.getByRole('tab', { name: authUiEnUS.passwordSignIn.title }),
    ).toBeInTheDocument()
    // The authenticated frame's business page is not rendered.
    expect(
      screen.queryByText(demoEnUS.home.heading),
    ).not.toBeInTheDocument()
  })

  it('renders the sign-in surface in the active language', () => {
    renderDemoApp({ language: 'zh-CN' })

    expect(
      screen.getByRole('tab', { name: authUiZhCN.passwordSignIn.title }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('tab', { name: authUiEnUS.passwordSignIn.title }),
    ).toBeNull()
  })
})
