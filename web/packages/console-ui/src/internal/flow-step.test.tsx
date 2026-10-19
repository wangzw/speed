/**
 * FlowStep behaviour: the guarded factor-kind vocabulary of a flow step.
 *
 * What this suite pins: a factor option renders its server-supplied label
 * when one arrives, otherwise a known kind's translated name, and an
 * unknown kind shows the kind as given; an enrollment kind follows the
 * same known/unknown rule. The package keeps its vocabulary locally
 * (auth-ui, account-ui and console-ui keep their own copies), so this
 * suite is the console-ui side of the registered sync obligation.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import type { AuthnStep } from '@speed/auth-core'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import { FlowStep } from './flow-step.js'

function render(step: AuthnStep) {
  return renderWithProviders(
    <FlowStep
      step={step}
      answerStep="answer"
      submitLabel="Continue"
      onSubmit={vi.fn()}
    />,
  )
}

describe('FlowStep labels', () => {
  it('lets a server label win and translates a known factor kind', () => {
    render({
      kind: 'factor',
      factors: [
        { id: 'f1', kind: 'totp', label: 'My app' },
        { id: 'f2', kind: 'sms' },
      ],
    })

    expect(screen.getByRole('button', { name: 'My app' })).toBeTruthy()
    expect(
      screen.getByRole('button', { name: zhCN.stepUp.factorKind.sms }),
    ).toBeTruthy()
  })

  it('translates known enroll kinds and shows an unknown kind as given', () => {
    render({ kind: 'enroll', kinds: ['webauthn', 'magic'] })

    expect(
      screen.getByRole('button', { name: zhCN.stepUp.factorKind.webauthn }),
    ).toBeTruthy()
    expect(screen.getByRole('button', { name: 'magic' })).toBeTruthy()
  })
})
