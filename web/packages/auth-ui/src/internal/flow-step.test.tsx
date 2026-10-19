/**
 * FlowStepFields behaviour: the factor and enroll label vocabulary.
 *
 * What this suite pins: a known factor or enroll kind renders its
 * translated name, a server-supplied factor label wins over it, and a
 * kind the family has no wording for shows the kind as given -- the
 * deployment's own vocabulary, never a generic stand-in. That last rule
 * is the one the three flow-step copies share: account-ui and console-ui
 * render an unknown kind verbatim, so this package does too, and this
 * suite is the auth-ui side of the registered sync obligation.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import type { AuthnStep } from '@speed/auth-core'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import { FlowStepFields } from './flow-step.js'
import type { OpenFlowState } from './flow.js'

function step(next: AuthnStep): OpenFlowState {
  return { seq: 1, state: 'enroll', next }
}

function render(next: AuthnStep) {
  return renderWithProviders(
    <FlowStepFields step={step(next)} busy={false} onAnswer={vi.fn()} />,
  )
}

describe('FlowStepFields labels', () => {
  it('translates known factor kinds and shows an unknown kind as given', () => {
    render({
      kind: 'factor',
      factors: [
        { id: 'f1', kind: 'totp' },
        { id: 'f2', kind: 'magic' },
      ],
    })

    expect(
      screen.getByRole('button', { name: zhCN.signInFlow.factorKind.totp }),
    ).toBeTruthy()
    // The unknown kind is the deployment's own name, not the generic
    // "other" label.
    expect(screen.getByRole('button', { name: 'magic' })).toBeTruthy()
    expect(
      screen.queryByRole('button', { name: zhCN.signInFlow.factorKind.other }),
    ).toBeNull()
  })

  it('lets a server-supplied factor label win over the vocabulary', () => {
    render({
      kind: 'factor',
      factors: [{ id: 'f1', kind: 'totp', label: 'My app' }],
    })

    expect(screen.getByRole('button', { name: 'My app' })).toBeTruthy()
    expect(
      screen.queryByRole('button', { name: zhCN.signInFlow.factorKind.totp }),
    ).toBeNull()
  })

  it('translates a known enroll kind and shows an unknown kind as given', () => {
    render({ kind: 'enroll', kinds: ['webauthn', 'magic'] })

    expect(
      screen.getByRole('button', { name: zhCN.signInFlow.factorKind.webauthn }),
    ).toBeTruthy()
    expect(screen.getByRole('button', { name: 'magic' })).toBeTruthy()
  })
})
