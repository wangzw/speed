/**
 * The host's sign-in surface.
 *
 * Beyond framing auth-ui's screen, it answers the skeleton's one dead end: a
 * fresh deployment is born with no account and no registration path, so the
 * surface says where the first account comes from. The suite renders an
 * anonymous viewer and reads that guidance.
 */

import { beforeEach, describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import demoZhCN from '../locales/zh-CN.json' with { type: 'json' }
import { renderDemoApp } from '../test-utils/render.js'

beforeEach(() => {
  window.history.replaceState({}, '', '/')
})

describe('the sign-in surface', () => {
  it('guides a fresh deployment to its first account', async () => {
    renderDemoApp()

    // The guidance sits under the form, naming the way a fresh deployment
    // gets its first account (the server's seed command).
    expect(
      await screen.findByText(demoEnUS.signIn.firstAccount.title),
    ).toBeInTheDocument()
    expect(
      screen.getByText(demoEnUS.signIn.firstAccount.body),
    ).toBeInTheDocument()
  })

  it('renders the guidance in the active language', () => {
    renderDemoApp({ language: 'zh-CN' })

    expect(
      screen.getByText(demoZhCN.signIn.firstAccount.title),
    ).toBeInTheDocument()
  })
})
