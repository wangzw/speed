/**
 * PhoneSection checks the two-step phone surface, the host hand-over and its refusals.
 */

import { describe, expect, it, vi } from 'vitest'
import { restoreRealTimers } from '@speed/test-utils/timers'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactElement } from 'react'
import { useState } from 'react'
import { PhoneCountryDefaultProvider, uiKitResources } from '@speed/ui-kit'
import { installFakeTurnstile } from '@speed/test-utils/turnstile'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  errorResponse,
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { profileBody } from '../test-utils/fixtures.js'
import { renderWithProviders } from '../test-utils/render.js'
import { captureRefreshWarning } from '../test-utils/refresh-warning.js'
import {
  AUTHENTICATED_SESSION,
  EMPTY_FEATURES,
  FEATURES_PATH,
  ProfileQueryObserver,
  SESSION_PATH,
  makeRotationRig,
} from '../test-utils/rotation.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { PhoneSection } from './PhoneSection.js'

const US_NATIONAL = '4155552671'
const US_E164 = '+14155552671'
const UI_KIT_ZH = uiKitResources['zh-CN'] as {
  readonly phoneNumber: { readonly incomplete: string }
}

const PHONE_PATH = '/api/v1/auth/phone'
const VERIFY_PATH = '/api/v1/auth/phone/verify'
const PROFILE_PATH = '/api/v1/auth/me'
const FLOW_START = '/api/v1/auth/flows/authenticated'
const FLOW_ANSWER = '/api/v1/auth/flows/answer'

function mount(
  respond: RealResponder,
  wrap: (ui: ReactElement) => ReactElement = (ui) => ui,
) {
  const rig = makeRealClientRig(respond)
  rig.store.set('access-1')
  const rendered = renderWithProviders(wrap(<PhoneSection session={rig.session} />))
  return { rig, ...rendered }
}

describe('PhoneSection', () => {
  it('sends a code, advances to the code step and verifies it, and passes axe', async () => {
    let verified = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        verified = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    expect(
      await screen.findByRole('heading', { name: zhCN.phone.title }),
    ).toBeTruthy()

    await user.type(
      screen.getByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))

    expect(
      await screen.findByText(
        zhCN.phone.sent.replace('{{phone}}', US_E164),
      ),
    ).toBeTruthy()
    expect(screen.getByLabelText(zhCN.phone.code.label)).toBeTruthy()
    expect(rig.calls.find((call) => call.path === PHONE_PATH)?.body).toEqual({
      phone: US_E164,
    })
    expect(screen.queryByRole('alert')).toBeNull()
    expect(
      screen
        .getByText(zhCN.phone.sent.replace('{{phone}}', US_E164))
        .closest('[role="status"]'),
    ).not.toBeNull()
    expect(screen.getByLabelText(zhCN.phone.code.label)).toHaveFocus()

    await expectNoAxeViolations()

    await user.type(screen.getByLabelText(zhCN.phone.code.label), '123456')
    await user.click(screen.getByRole('button', { name: zhCN.phone.verify }))

    const added = zhCN.phone.added.replace('{{phone}}', US_E164)
    await waitFor(() => expect(screen.getByText(added)).toBeTruthy())
    expect(zhCN.phone.added).toContain(zhCN.phone.title)
    expect(enUS.phone.added).toContain(enUS.phone.title)
    const successRegion = screen
      .getByText(added)
      .closest('[role="status"]') as HTMLElement
    expect(
      screen
        .getByRole('button', { name: zhCN.phone.send })
        .compareDocumentPosition(successRegion) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(verified).toBe(true)
    expect(rig.calls.find((call) => call.path === VERIFY_PATH)?.body).toEqual({
      code: '123456',
    })
  })

  it('hands the bound number to the host once on success and keeps its own confirmation', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const onAdded = vi.fn()
    renderWithProviders(
      <PhoneSection
        session={rig.session}
        onCancel={() => undefined}
        onAdded={onAdded}
      />,
    )
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    await user.type(
      await screen.findByLabelText(zhCN.phone.code.label),
      '123456',
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.verify }))

    expect(
      await screen.findByText(zhCN.phone.added.replace('{{phone}}', US_E164)),
    ).toBeTruthy()
    await waitFor(() => expect(onAdded).toHaveBeenCalledTimes(1))
    expect(onAdded).toHaveBeenCalledWith(US_E164)
    await act(async () => {})
    expect(onAdded).toHaveBeenCalledTimes(1)
  })

  it('waits for the profile refresh before handing the number to the host', async () => {
    let profileReads = 0
    let releaseHeldRead: (() => void) | undefined
    const heldRead = new Promise<void>((resolve) => {
      releaseHeldRead = resolve
    })
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return new Response(null, { status: 204 })
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        profileReads += 1
        return profileReads === 1
          ? jsonResponse(200, profileBody())
          : heldRead.then(() => jsonResponse(200, profileBody()))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const onAdded = vi.fn()
    renderWithProviders(
      <>
        <PhoneSection
          session={rig.session}
          onCancel={() => undefined}
          onAdded={onAdded}
        />
        <ProfileQueryObserver />
      </>,
    )
    const user = userEvent.setup()

    await waitFor(() => expect(profileReads).toBe(1))
    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    await user.type(
      await screen.findByLabelText(zhCN.phone.code.label),
      '123456',
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.verify }))

    await waitFor(() => expect(profileReads).toBe(2))
    expect(onAdded).not.toHaveBeenCalled()
    const added = zhCN.phone.added.replace('{{phone}}', US_E164)
    await waitFor(() => expect(screen.getByText(added)).toBeTruthy())
    expect(screen.queryByRole('alert')).toBeNull()
    expect(
      screen.queryByRole('button', { name: zhCN.phone.send }),
    ).toBeNull()
    expect(
      screen.queryByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
    ).toBeNull()

    await act(async () => {
      releaseHeldRead?.()
    })
    await waitFor(() => expect(onAdded).toHaveBeenCalledTimes(1))
    expect(onAdded).toHaveBeenCalledWith(US_E164)
    await act(async () => {})
    expect(onAdded).toHaveBeenCalledTimes(1)
  })

  it('does not hand the number to the host when the profile read settles after the section unmounts', async () => {
    let profileReads = 0
    let releaseHeldRead: (() => void) | undefined
    const heldRead = new Promise<void>((resolve) => {
      releaseHeldRead = resolve
    })
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return new Response(null, { status: 204 })
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        profileReads += 1
        return profileReads === 1
          ? jsonResponse(200, profileBody())
          : heldRead.then(() => jsonResponse(200, profileBody()))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const onAdded = vi.fn()
    const { unmount } = renderWithProviders(
      <>
        <PhoneSection
          session={rig.session}
          onCancel={() => undefined}
          onAdded={onAdded}
        />
        <ProfileQueryObserver />
      </>,
    )
    const user = userEvent.setup()

    await waitFor(() => expect(profileReads).toBe(1))
    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    await user.type(
      await screen.findByLabelText(zhCN.phone.code.label),
      '123456',
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.verify }))

    await waitFor(() => expect(profileReads).toBe(2))
    expect(onAdded).not.toHaveBeenCalled()

    unmount()
    await act(async () => {
      releaseHeldRead?.()
    })
    await act(async () => {})

    expect(onAdded).not.toHaveBeenCalled()
  })

  it('offers Close while the profile read is held, and closes without handing the number over', async () => {
    let profileReads = 0
    let releaseHeldRead: (() => void) | undefined
    const heldRead = new Promise<void>((resolve) => {
      releaseHeldRead = resolve
    })
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return new Response(null, { status: 204 })
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        profileReads += 1
        return profileReads === 1
          ? jsonResponse(200, profileBody())
          : heldRead.then(() => jsonResponse(200, profileBody()))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const onAdded = vi.fn()
    const onCancelSpy = vi.fn()
    function Host() {
      const [visible, setVisible] = useState(true)
      return (
        <>
          {visible ? (
            <PhoneSection
              session={rig.session}
              onCancel={() => {
                setVisible(false)
                onCancelSpy()
              }}
              onAdded={onAdded}
            />
          ) : null}
          <ProfileQueryObserver />
        </>
      )
    }
    renderWithProviders(<Host />)
    const user = userEvent.setup()

    await waitFor(() => expect(profileReads).toBe(1))
    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    await user.type(
      await screen.findByLabelText(zhCN.phone.code.label),
      '123456',
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.verify }))

    await waitFor(() => expect(profileReads).toBe(2))
    expect(
      screen.queryByRole('button', { name: zhCN.phone.send }),
    ).toBeNull()
    const close = screen.getByRole('button', { name: zhCN.phone.close })
    expect(close).not.toHaveAttribute('aria-disabled', 'true')
    await user.click(close)
    expect(onCancelSpy).toHaveBeenCalledTimes(1)
    expect(onAdded).not.toHaveBeenCalled()

    await act(async () => {
      releaseHeldRead?.()
    })
    await act(async () => {})
    expect(onAdded).not.toHaveBeenCalled()
  })

  it('keeps the confirmation when the profile re-read fails', async () => {
    let profileReads = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return new Response(null, { status: 204 })
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        profileReads += 1
        return profileReads === 1
          ? jsonResponse(200, profileBody())
          : errorResponse(500, 'common.internal')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const onAdded = vi.fn()
    renderWithProviders(
      <>
        <PhoneSection
          session={rig.session}
          onCancel={() => undefined}
          onAdded={onAdded}
        />
        <ProfileQueryObserver />
      </>,
    )
    const user = userEvent.setup()

    await waitFor(() => expect(profileReads).toBe(1))
    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    await user.type(
      await screen.findByLabelText(zhCN.phone.code.label),
      '123456',
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.verify }))

    await waitFor(() => expect(profileReads).toBe(2))
    const added = zhCN.phone.added.replace('{{phone}}', US_E164)
    await waitFor(() => expect(screen.getByText(added)).toBeTruthy())
    expect(screen.queryByRole('alert')).toBeNull()
    await waitFor(() => expect(onAdded).toHaveBeenCalledTimes(1))
    expect(onAdded).toHaveBeenCalledWith(US_E164)
    await act(async () => {})
    expect(onAdded).toHaveBeenCalledTimes(1)
  })

  it('does not report a landed write as failed when the host callback throws', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const onAdded = vi.fn(() => {
      throw new Error('host exploded')
    })
    renderWithProviders(
      <PhoneSection
        session={rig.session}
        onCancel={() => undefined}
        onAdded={onAdded}
      />,
    )
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    await user.type(
      await screen.findByLabelText(zhCN.phone.code.label),
      '123456',
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.verify }))

    expect(
      await screen.findByText(zhCN.phone.added.replace('{{phone}}', US_E164)),
    ).toBeTruthy()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(onAdded).toHaveBeenCalledTimes(1)
    expect(onAdded).toHaveBeenCalledWith(US_E164)
    await act(async () => {})
    expect(onAdded).toHaveBeenCalledTimes(1)
  })

  it('starts on the host\'s preferred country and assembles the number under it', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond, (ui) => (
      <PhoneCountryDefaultProvider country="GB">{ui}</PhoneCountryDefaultProvider>
    ))
    const user = userEvent.setup()

    const country = await screen.findByLabelText(zhCN.phone.country.label)
    expect((country as HTMLInputElement).value).toContain('+44')

    await user.type(screen.getByLabelText(zhCN.phone.phone.label, { selector: 'input' }), '7911123456')
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))

    await waitFor(() =>
      expect(rig.calls.find((call) => call.path === PHONE_PATH)?.body).toEqual({
        phone: '+447911123456',
      }),
    )
  })

  it('holds the send control unavailable with its reason until a number is entered, and sends nothing', async () => {
    const respond: RealResponder = () => {
      throw new Error('no request expected')
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    const send = await screen.findByRole('button', { name: zhCN.phone.send })
    const phone = screen.getByLabelText(zhCN.phone.phone.label, {
      selector: 'input',
    })
    expect(send).toHaveAttribute('aria-disabled', 'true')
    expect(send).not.toHaveAttribute('disabled')
    expect(send).not.toHaveAttribute('aria-busy')
    expect(send).toHaveAccessibleDescription(zhCN.phone.sendUnavailable)
    send.focus()
    expect(send).toHaveFocus()

    await user.click(send)
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    fireEvent.submit(send.closest('form') as HTMLFormElement)
    expect(rig.calls.filter((call) => call.path === PHONE_PATH)).toHaveLength(0)

    await user.type(phone, US_NATIONAL)
    expect(send).not.toHaveAttribute('aria-disabled', 'true')
    expect(send).not.toHaveAccessibleDescription()
  })

  it('holds the verify control unavailable with its reason until a code is entered', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    const code = await screen.findByLabelText(zhCN.phone.code.label)
    const verify = screen.getByRole('button', { name: zhCN.phone.verify })

    expect(verify).toHaveAttribute('aria-disabled', 'true')
    expect(verify).not.toHaveAttribute('disabled')
    expect(verify).not.toHaveAttribute('aria-busy')
    expect(verify).toHaveAccessibleDescription(zhCN.phone.verifyUnavailable)
    await user.click(verify)
    fireEvent.submit(verify.closest('form') as HTMLFormElement)
    expect(rig.calls.filter((call) => call.path === VERIFY_PATH)).toHaveLength(0)

    await user.type(code, '123456')
    expect(verify).not.toHaveAttribute('aria-disabled', 'true')
    expect(verify).not.toHaveAccessibleDescription()
  })

  it('keeps the send control busy and the close sibling unavailable while the send is in flight', async () => {
    let finish: (() => void) | undefined
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return new Promise<Response>((resolve) => {
          finish = () =>
            resolve(jsonResponse(200, { expires_in: 300, resend_after: 60 }))
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const onCancel = vi.fn()
    renderWithProviders(
      <PhoneSection
        session={rig.session}
        humanGate={{
          phone: { required: false, provider: 'off' },
          other: { required: false, provider: 'off' },
        }}
        onCancel={onCancel}
        onAdded={() => undefined}
      />,
    )
    const user = userEvent.setup()

    const phone = await screen.findByLabelText(zhCN.phone.phone.label, {
      selector: 'input',
    })
    await user.type(phone, US_NATIONAL)
    const send = screen.getByRole('button', { name: zhCN.phone.send })
    const close = screen.getByRole('button', { name: zhCN.phone.close })

    await user.click(send)
    await waitFor(() => expect(send).toHaveAttribute('aria-busy', 'true'))
    expect(send).toHaveFocus()
    expect(send).toHaveAttribute('aria-disabled', 'true')
    expect(close).toHaveAttribute('aria-disabled', 'true')
    expect(close).not.toHaveAttribute('aria-busy')
    expect(close).not.toHaveAccessibleDescription()
    await user.click(close)
    close.focus()
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(onCancel).not.toHaveBeenCalled()

    expect(phone).toHaveAttribute('readonly')
    expect(phone).not.toHaveAttribute('disabled')
    const before = (phone as HTMLInputElement).value
    phone.focus()
    expect(phone).toHaveFocus()
    await user.keyboard('9')
    expect((phone as HTMLInputElement).value).toBe(before)

    await act(async () => {
      finish?.()
    })
    await screen.findByLabelText(zhCN.phone.code.label)
    expect(rig.calls.filter((call) => call.path === PHONE_PATH)).toHaveLength(1)
  })

  it('keeps the verify control busy with its cancel sibling unavailable while the verify is in flight', async () => {
    let finish: (() => void) | undefined
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return new Promise<Response>((resolve) => {
          finish = () => resolve(new Response(null, { status: 204 }))
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    const code = await screen.findByLabelText(zhCN.phone.code.label)
    await user.type(code, '123456')
    const verify = screen.getByRole('button', { name: zhCN.phone.verify })
    const cancel = screen.getByRole('button', { name: zhCN.phone.cancel })

    await user.click(verify)
    await waitFor(() => expect(verify).toHaveAttribute('aria-busy', 'true'))
    expect(verify).toHaveFocus()
    expect(cancel).toHaveAttribute('aria-disabled', 'true')
    expect(cancel).not.toHaveAttribute('aria-busy')
    expect(cancel).not.toHaveAccessibleDescription()
    await user.click(cancel)
    expect(screen.getByLabelText(zhCN.phone.code.label)).toBeTruthy()
    expect(code).toHaveAttribute('readonly')
    expect(code).not.toHaveAttribute('disabled')

    await act(async () => {
      finish?.()
    })
    await waitFor(() =>
      expect(screen.queryByLabelText(zhCN.phone.code.label)).toBeNull(),
    )
    expect(screen.getByRole('button', { name: zhCN.phone.send })).toBeTruthy()
    expect(rig.calls.filter((call) => call.path === VERIFY_PATH)).toHaveLength(1)
  })

  it('keeps the returning send control out of the progress state while the profile refresh is in flight', async () => {
    let releaseHeldRead: (() => void) | undefined
    const heldRead = new Promise<void>((resolve) => {
      releaseHeldRead = resolve
    })
    let profileReads = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return new Response(null, { status: 204 })
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        profileReads += 1
        return profileReads === 1
          ? jsonResponse(200, profileBody())
          : heldRead.then(() => jsonResponse(200, profileBody()))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <>
        <PhoneSection session={rig.session} />
        <ProfileQueryObserver />
      </>,
    )
    const user = userEvent.setup()

    await waitFor(() => expect(profileReads).toBe(1))
    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    await user.type(
      await screen.findByLabelText(zhCN.phone.code.label),
      '123456',
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.verify }))

    await waitFor(() => expect(profileReads).toBe(2))
    const send = await screen.findByRole('button', { name: zhCN.phone.send })
    expect(send).toHaveAttribute('aria-disabled', 'true')
    expect(send).not.toHaveAttribute('aria-busy')
    await user.click(send)
    expect(rig.calls.filter((call) => call.path === PHONE_PATH)).toHaveLength(1)

    await act(async () => {
      releaseHeldRead?.()
    })
  })

  it('hands focus to the control group when a successful verify removes its control', async () => {
    let finish: (() => void) | undefined
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return new Promise<Response>((resolve) => {
          finish = () => resolve(new Response(null, { status: 204 }))
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <PhoneSection
        session={rig.session}
        onCancel={() => undefined}
        onAdded={() => undefined}
      />,
    )
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    await user.type(
      await screen.findByLabelText(zhCN.phone.code.label),
      '123456',
    )
    const verify = screen.getByRole('button', { name: zhCN.phone.verify })
    await user.click(verify)
    await waitFor(() =>
      expect(
        rig.calls.filter((call) => call.path === VERIFY_PATH),
      ).toHaveLength(1),
    )

    await act(async () => {
      finish?.()
    })

    await waitFor(() => expect(verify).not.toBeInTheDocument())
    expect(document.activeElement).not.toBe(document.body)
    const group = screen.getByRole('form', { name: zhCN.phone.title })
    await waitFor(() => expect(group).toHaveFocus())
  })

  it('leaves a focus the operator moved during the verify alone', async () => {
    let finish: (() => void) | undefined
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return new Promise<Response>((resolve) => {
          finish = () => resolve(new Response(null, { status: 204 }))
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <>
        <PhoneSection
          session={rig.session}
          onCancel={() => undefined}
          onAdded={() => undefined}
        />
        <button type="button">Elsewhere</button>
      </>,
    )
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    await user.type(
      await screen.findByLabelText(zhCN.phone.code.label),
      '123456',
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.verify }))
    const verify = screen.getByRole('button', { name: zhCN.phone.verify })
    await waitFor(() => expect(verify).toHaveAttribute('aria-busy', 'true'))

    const elsewhere = screen.getByRole('button', { name: 'Elsewhere' })
    elsewhere.focus()
    expect(elsewhere).toHaveFocus()

    await act(async () => {
      finish?.()
    })
    await waitFor(() => expect(verify).not.toBeInTheDocument())
    expect(elsewhere).toHaveFocus()
  })

  it('refuses a too-short number locally, naming the calling code, and sends nothing', async () => {
    const respond: RealResponder = () => {
      throw new Error('no request expected')
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    const phone = await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' })
    await user.type(phone, '415555')
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))

    expect(
      await screen.findByText(
        UI_KIT_ZH.phoneNumber.incomplete.replace('{{code}}', '1'),
      ),
    ).toBeTruthy()
    expect(phone).toHaveAttribute('aria-invalid', 'true')
    expect(rig.calls.filter((call) => call.path === PHONE_PATH)).toHaveLength(0)
  })

  it('returns to the number step on cancel', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    await screen.findByLabelText(zhCN.phone.code.label)

    await user.click(screen.getByRole('button', { name: zhCN.phone.cancel }))
    expect(screen.getByLabelText(zhCN.phone.phone.label, { selector: 'input' })).toBeTruthy()
    expect(screen.queryByLabelText(zhCN.phone.code.label)).toBeNull()
  })

  it('renders a refused send and a wrong code as their code text', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return errorResponse(409, 'authn.conflict')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.conflict)
    expect(
      screen
        .getByRole('button', { name: zhCN.phone.send })
        .compareDocumentPosition(alert) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('names a throttled send\'s wait in the shared banner and counts it down live', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return new Response(
          JSON.stringify({ code: 'authn.throttled', traceId: 'trace-1' }),
          {
            status: 429,
            headers: {
              'content-type': 'application/json',
              'retry-after': '3',
            },
          },
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <PhoneSection
        session={rig.session}
        humanGate={{
          phone: { required: false, provider: 'off' },
          other: { required: false, provider: 'off' },
        }}
      />,
    )
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )

    vi.useFakeTimers()
    try {
      await act(async () => {
        fireEvent.click(screen.getByRole('button', { name: zhCN.phone.send }))
        for (let turn = 0; turn < 10; turn += 1) {
          await Promise.resolve()
        }
      })

      const sentence = (seconds: number) =>
        zhCN.throttle.retryAfter.replace('{{seconds}}', String(seconds))
      const alert = screen.getByRole('alert')
      expect(alert.textContent).toContain(sentence(3))
      const visible = Array.from(
        alert.querySelectorAll('[aria-hidden="true"]'),
      ).find((node) => (node.textContent ?? '').includes('3'))
      expect(visible).toBeDefined()
      expect(visible?.textContent).toContain(sentence(3))

      act(() => {
        vi.advanceTimersByTime(1000)
      })
      expect(alert.textContent).toContain(sentence(3))
      expect(visible?.textContent).toContain(sentence(2))

      act(() => {
        vi.advanceTimersByTime(2000)
      })
      expect(visible?.textContent).toBe(zhCN.errors.authn.throttled)
      expect(screen.getByRole('alert')).toBeTruthy()
    } finally {
      await restoreRealTimers()
    }
  })

  it('renders a refused verification beneath the code controls', async () => {
    captureRefreshWarning({ expected: true })
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return errorResponse(401, 'authn.login_failed')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    await screen.findByLabelText(zhCN.phone.code.label)
    await user.type(screen.getByLabelText(zhCN.phone.code.label), '000000')
    await user.click(screen.getByRole('button', { name: zhCN.phone.verify }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.login_failed)
    expect(
      screen
        .getByRole('button', { name: zhCN.phone.verify })
        .compareDocumentPosition(alert) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(
      screen.queryByText(zhCN.phone.added.replace('{{phone}}', US_E164)),
    ).toBeNull()
  })

  it('renders a step-up refusal of the send as an ordinary failure, with no dialog', async () => {
    captureRefreshWarning({ expected: true })
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return errorResponse(401, 'authn.insufficient_user_authentication')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(
      zhCN.errors.authn.insufficient_user_authentication,
    )
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(
      rig.calls.filter((call) => call.path === PHONE_PATH),
    ).toHaveLength(1)
  })

  it('arms the step-up dialog on a refused verify and replays the same code', async () => {
    captureRefreshWarning()
    let elevated = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        if (!elevated) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        return new Response(null, { status: 204 })
      }
      if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
        return jsonResponse(200, {
          factors: [
            {
              id: 'factor-1',
              kind: 'totp',
              label: 'App',
              status: 'active',
              created_at: '2026-01-01T00:00:00Z',
            },
          ],
        })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'second',
          next: {
            kind: 'factor',
            factors: [{ id: 'factor-1', kind: 'totp', label: 'App' }],
          },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        const body = call.body as { seq: number }
        if (body.seq === 1) {
          return jsonResponse(200, {
            seq: 2,
            state: 'second',
            next: { kind: 'challenge', fields: [{ name: 'code', kind: 'text' }] },
          })
        }
        elevated = true
        return jsonResponse(200, {
          state: 'elevated',
          token_type: 'Bearer',
          access_token: 'access-2',
          expires_in: 3600,
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const onAdded = vi.fn()
    renderWithProviders(
      <PhoneSection
        session={rig.session}
        onCancel={() => undefined}
        onAdded={onAdded}
      />,
    )
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    await user.type(
      await screen.findByLabelText(zhCN.phone.code.label),
      '123456',
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.verify }))

    await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp })
    expect(
      screen.queryByText(zhCN.phone.added.replace('{{phone}}', US_E164)),
    ).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
    await user.click(screen.getByRole('button', { name: zhCN.mfa.factor.kind.totp }))
    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await waitFor(() =>
      expect(
        screen.getByText(zhCN.phone.added.replace('{{phone}}', US_E164)),
      ).toBeTruthy(),
    )
    const verifies = rig.calls.filter(
      (call) => call.method === 'POST' && call.path === VERIFY_PATH,
    )
    expect(verifies).toHaveLength(2)
    expect(verifies[0]?.body).toEqual({ code: '123456' })
    expect(verifies[1]?.body).toEqual({ code: '123456' })
    await waitFor(() => expect(onAdded).toHaveBeenCalledTimes(1))
    expect(onAdded).toHaveBeenCalledWith(US_E164)
    await act(async () => {})
    expect(onAdded).toHaveBeenCalledTimes(1)
  })

  it('proves a number without opening an identity-transition window', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody())
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(
      <>
        <PhoneSection session={rig.session} />
        <ProfileQueryObserver />
      </>,
    )
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    await user.type(
      await screen.findByLabelText(zhCN.phone.code.label),
      '123456',
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.verify }))

    await waitFor(() =>
      expect(
        screen.getByText(zhCN.phone.added.replace('{{phone}}', US_E164)),
      ).toBeTruthy(),
    )
    expect(rig.transitionBegins()).toBe(0)
    expect(rig.session.getSnapshot().state).toBe('authenticated')
    expect(rig.endedEdges()).toBe(0)
  })

  it('opens no identity-transition window for a refused verification', async () => {
    captureRefreshWarning({ expected: true })
    let refused = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return refused
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return refused
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, profileBody())
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        refused = true
        return errorResponse(401, 'authn.login_failed')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(<PhoneSection session={rig.session} />)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      US_NATIONAL,
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
    await user.type(
      await screen.findByLabelText(zhCN.phone.code.label),
      '123456',
    )
    await user.click(screen.getByRole('button', { name: zhCN.phone.verify }))

    await screen.findByText(zhCN.errors.authn.login_failed)
    expect(rig.transitionBegins()).toBe(0)

    await rig.session.restore()
    expect(rig.session.getSnapshot().state).toBe('anonymous')
    expect(rig.endedEdges()).toBe(1)
  })

  it('asks for the verifier only when the send is requested, then sends it with the proof', async () => {
    const fake = installFakeTurnstile()
    try {
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'POST' && call.path === PHONE_PATH) {
          return jsonResponse(200, { expires_in: 300, resend_after: 60 })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const rig = makeRealClientRig(respond)
      rig.store.set('access-1')
      renderWithProviders(
        <PhoneSection
          session={rig.session}
          humanGate={{
            phone: { required: true, provider: 'turnstile', site_key: 'site-1' },
            other: { required: false, provider: 'off' },
          }}
        />,
      )
      const user = userEvent.setup()

      await user.type(
        screen.getByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
        US_NATIONAL,
      )
      expect(fake.widgets).toHaveLength(0)
      const send = screen.getByRole('button', { name: zhCN.phone.send })
      expect(send).not.toHaveAttribute('aria-disabled', 'true')
      await user.click(send)

      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      expect(fake.widgets[0]?.siteKey).toBe('site-1')
      const waiting = screen.getByRole('button', { name: zhCN.phone.send })
      expect(waiting).toHaveAttribute('aria-busy', 'true')
      expect(rig.calls.filter((call) => call.path === PHONE_PATH)).toHaveLength(
        0,
      )

      act(() => {
        fake.token('token-1')
      })
      await waitFor(() =>
        expect(
          rig.calls.filter((call) => call.path === PHONE_PATH),
        ).toHaveLength(1),
      )
      expect(rig.calls.find((call) => call.path === PHONE_PATH)?.body).toEqual({
        phone: US_E164,
        human_proof: 'token-1',
      })
      await screen.findByLabelText(zhCN.phone.code.label)
    } finally {
      fake.restore()
    }
  })
})
