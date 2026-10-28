/**
 * InlineError contract tests: the family's banner (ui-kit's shared
 * InlineError bound to this namespace's error-text resolver) renders
 * exactly when a code is present, resolves whitelisted and unlisted codes
 * through the resolver (dedicated text vs the unknown fallback, in this
 * namespace only), and re-renders in the switched language. The failure
 * classifier's own contract lives beside the classifier, in ui-kit's
 * InlineError suite. Text expectations read the zh-CN and en-US bundle
 * values, never inline language.
 */

import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { switchLanguage } from '@speed/i18n'
import { InlineError, useFlowErrorText } from './inline-error.js'
import type { OpenFlowState } from './flow.js'
import { renderWithProviders } from '../../test-utils/render.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import enUS from '../locales/en-US.json' with { type: 'json' }

describe('InlineError', () => {
  it('render nothing while the code is null (no prior failure)', () => {
    const { container } = renderWithProviders(<InlineError code={null} />)
    expect(container.firstChild).toBeNull()
  })

  it('render a whitelisted authn code with its own text in an alert', () => {
    renderWithProviders(<InlineError code="authn.login_failed" />)
    expect(screen.getByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.login_failed,
    )
  })

  // The login-flow problem set: each code resolves to its own bundle text,
  // so a whitelist or locale typo in any one fails here even when the
  // component tests only render representatives.
  const flowCodes = [
    ['authn.flow_invalid', zhCN.errors.authn.flow_invalid],
    ['authn.link_required', zhCN.errors.authn.link_required],
    ['authn.method_disabled', zhCN.errors.authn.method_disabled],
    ['authn.account_unavailable', zhCN.errors.authn.account_unavailable],
    ['authn.conflict', zhCN.errors.authn.conflict],
    ['authn.username_taken', zhCN.errors.authn.username_taken],
    ['authn.username_reserved', zhCN.errors.authn.username_reserved],
    ['authn.throttled', zhCN.errors.authn.throttled],
    ['authn.unavailable', zhCN.errors.authn.unavailable],
    ['authn.unsupported_region', zhCN.errors.authn.unsupported_region],
    ['authn.bad_request', zhCN.errors.authn.bad_request],
  ] as const

  it.each(flowCodes)(
    'render the %s flow code with its own text',
    (code, text) => {
      renderWithProviders(<InlineError code={code} />)
      expect(screen.getByRole('alert')).toHaveTextContent(text)
    },
  )

  it('render the unknown fallback for a code outside the whitelist', () => {
    renderWithProviders(<InlineError code="authn.future_code" />)
    expect(screen.getByRole('alert')).toHaveTextContent(zhCN.errors.unknown)
  })

  it('re-render in the switched language', async () => {
    const { i18n } = renderWithProviders(
      <InlineError code="authn.method_disabled" />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.method_disabled,
    )
    await switchLanguage(i18n, 'en-US')
    expect(screen.getByRole('alert')).toHaveTextContent(
      enUS.errors.authn.method_disabled,
    )
  })
})

/** The username step a first login parks at while it waits for a name. */
const SIGNUP_STEP: OpenFlowState = {
  seq: 3,
  state: 'signup',
  next: {
    kind: 'challenge',
    fields: [{ name: 'username', kind: 'text', required: true }],
  },
}

/** The flow's first step: the same codes mean something else there. */
const FIRST_STEP: OpenFlowState = {
  seq: 1,
  state: 'first',
  next: {
    kind: 'challenge',
    fields: [{ name: 'phone', kind: 'phone', required: true }],
  },
}

/** The banner with the step-aware resolver a channel binds. */
function FlowBanner({
  step,
  code,
}: {
  readonly step: OpenFlowState | null
  readonly code: string
}) {
  const resolve = useFlowErrorText(step)
  return <InlineError code={code} resolve={resolve} />
}

describe('useFlowErrorText', () => {
  it('names a taken username on the sign-up step', () => {
    renderWithProviders(
      <FlowBanner step={SIGNUP_STEP} code="authn.username_taken" />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent(
      zhCN.signInFlow.usernameTaken,
    )
    expect(enUS.signInFlow.usernameTaken).toMatch(/already taken/i)
  })

  it('names an unusable username on the sign-up step', () => {
    // A malformed name and a reserved one are the step's two 400s.
    for (const code of ['authn.bad_request', 'authn.username_reserved']) {
      const { unmount } = renderWithProviders(
        <FlowBanner step={SIGNUP_STEP} code={code} />,
      )
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.signInFlow.usernameUnusable,
      )
      unmount()
    }
  })

  it('keeps the namespace text off the username step', () => {
    renderWithProviders(
      <FlowBanner step={FIRST_STEP} code="authn.bad_request" />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.bad_request,
    )
    expect(screen.getByRole('alert')).not.toHaveTextContent(
      zhCN.signInFlow.usernameUnusable,
    )
  })

  it('keeps the namespace text for other codes on the username step', () => {
    renderWithProviders(
      <FlowBanner step={SIGNUP_STEP} code="authn.flow_invalid" />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.flow_invalid,
    )
  })

  it('falls back to the namespace text with no step parked', () => {
    renderWithProviders(
      <FlowBanner step={null} code="authn.bad_request" />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.bad_request,
    )
    expect(screen.getByRole('alert')).not.toHaveTextContent(
      zhCN.signInFlow.usernameUnusable,
    )
  })
})

