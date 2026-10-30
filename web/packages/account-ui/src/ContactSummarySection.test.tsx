/**
 * ContactSummarySection behaviour: the read-only contact summary over the
 * profile read (GET /api/v1/auth/me).
 *
 * What this suite pins: every email identifier renders its value with the
 * verified badge when the server marks it proven and the unverified badge
 * otherwise; a phone identifier renders only when the host says the
 * deployment serves phones, and a deployment without them renders no phone
 * row at all; a kind with no identifier renders the "not added yet" note
 * instead of disappearing; and the surface carries no button or form -- the
 * write surfaces live in the sibling sections.
 */

import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import { identifier, profileBody } from '../test-utils/fixtures.js'
import {
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { ContactSummarySection } from './ContactSummarySection.js'

const PROFILE_PATH = '/api/v1/auth/me'

function mount(respond: RealResponder, phoneServed?: boolean) {
  const rig = makeRealClientRig(respond)
  rig.store.set('access-1')
  return renderWithProviders(
    <ContactSummarySection
      {...(phoneServed === undefined ? {} : { phoneServed })}
    />,
  )
}

/** A responder whose profile read answers the given identifiers. */
function profileWith(identifiers: readonly Record<string, unknown>[]): RealResponder {
  return (call: RealCall) => {
    if (call.method === 'GET' && call.path === PROFILE_PATH) {
      return jsonResponse(200, profileBody({ identifiers }))
    }
    throw new Error(`unexpected ${call.method} ${call.path}`)
  }
}

describe('ContactSummarySection', () => {
  it('renders a verified email with its value and the verified badge, and no writes', async () => {
    mount(
      profileWith([
        identifier({ id: 'id-email', kind: 'email', value: 'ada@x.test' }),
      ]),
    )

    expect(
      await screen.findByRole('heading', { name: zhCN.contacts.title }),
    ).toBeTruthy()
    expect(await screen.findByText('ada@x.test')).toBeTruthy()
    expect(screen.getByText(zhCN.identifiers.verified)).toBeTruthy()
    expect(
      screen.getByRole('heading', { name: zhCN.identifiers.kind.email }),
    ).toBeTruthy()
    // Read-only: the write surfaces stay in the sibling sections.
    expect(screen.queryByRole('button')).toBeNull()
    expect(screen.queryByRole('form')).toBeNull()
  })

  it('renders an unverified email with the unverified badge', async () => {
    mount(
      profileWith([
        identifier({
          id: 'id-email',
          kind: 'email',
          value: 'unproved@x.test',
          proven: false,
          verified_at: '',
          verified_via: '',
        }),
      ]),
    )

    expect(await screen.findByText('unproved@x.test')).toBeTruthy()
    expect(screen.getByText(zhCN.identifiers.unverified)).toBeTruthy()
    expect(screen.queryByText(zhCN.identifiers.verified)).toBeNull()
  })

  it('renders a phone when the deployment serves phones', async () => {
    mount(
      profileWith([
        identifier({ id: 'id-phone', kind: 'phone', value: '+14155550123' }),
      ]),
    )

    expect(await screen.findByText('+14155550123')).toBeTruthy()
    expect(
      screen.getByRole('heading', { name: zhCN.identifiers.kind.phone }),
    ).toBeTruthy()
  })

  it('renders no phone rows when the deployment serves no phones', async () => {
    mount(
      profileWith([
        identifier({ id: 'id-email', kind: 'email', value: 'ada@x.test' }),
        identifier({ id: 'id-phone', kind: 'phone', value: '+14155550123' }),
      ]),
      false,
    )

    expect(await screen.findByText('ada@x.test')).toBeTruthy()
    expect(screen.queryByText('+14155550123')).toBeNull()
    expect(
      screen.queryByRole('heading', { name: zhCN.identifiers.kind.phone }),
    ).toBeNull()
  })

  it('shows the not-added note for a kind with no identifier', async () => {
    mount(profileWith([]), false)

    expect(await screen.findByText(zhCN.contacts.none)).toBeTruthy()
    expect(
      screen.getByRole('heading', { name: zhCN.identifiers.kind.email }),
    ).toBeTruthy()
  })
})
