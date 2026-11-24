/**
 * IdentifiersSection in a real browser: the initiating control keeps focus and a landed write hands it on.
 */
import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import { page, userEvent } from 'vitest/browser'
import { captchaChallenge } from '@speed/test-utils/session-harness'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import uiKitZhCN from '../../ui-kit/src/locales/zh-CN.json' with { type: 'json' }
import { identifier, profileBody } from '../test-utils/fixtures.js'
import {
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { IdentifiersSection } from './IdentifiersSection.js'

const PROFILE_PATH = '/api/v1/auth/me'
const ADD_PATH = '/api/v1/auth/identifiers'
const CAPTCHA_PATH = '/api/v1/auth/human/captcha/challenge'
const CAPTCHA_ANSWER = uiKitZhCN.humanVerification.captcha.answerLabel

function deferred<T>(): {
  readonly promise: Promise<T>
  readonly resolve: (value: T) => void
} {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

function nextFrame(): Promise<void> {
  return new Promise((resolve) => {
    requestAnimationFrame(() => resolve())
  })
}

function describeActiveElement(): string {
  const active = document.activeElement
  if (active === null) {
    return 'null'
  }
  const parts = [active.tagName.toLowerCase()]
  const role = active.getAttribute('role')
  const label = active.getAttribute('aria-label')
  const text = (active.textContent ?? '').replace(/\s+/g, ' ').trim().slice(0, 80)
  if (role !== null) {
    parts.push(`role="${role}"`)
  }
  if (label !== null) {
    parts.push(`aria-label="${label}"`)
  }
  if (text !== '') {
    parts.push(`text="${text}"`)
  }
  return parts.join(' ')
}

function twoEmailProfile(): Record<string, unknown> {
  return profileBody({
    password_set: false,
    identifiers: [
      identifier({
        id: 'id-a',
        kind: 'email',
        value: 'a@x.test',
        proven: true,
        sign_in: true,
      }),
      identifier({
        id: 'id-b',
        kind: 'email',
        value: 'b@x.test',
        proven: true,
        sign_in: true,
      }),
    ],
  })
}

function rowScope(value: string): HTMLElement {
  for (const text of screen.getAllByText(value)) {
    const row = text.closest('li')
    if (row !== null) {
      return row as HTMLElement
    }
  }
  throw new Error(`no row shows ${value}`)
}

function mount(respond: RealResponder) {
  const rig = makeRealClientRig(respond)
  rig.store.set('access-1')
  return renderWithProviders(<IdentifiersSection session={rig.session} />)
}

describe('IdentifiersSection write focus in Chromium', () => {
  it('keeps focus on the add-form send while the request is in flight', async () => {
    await page.viewport(1200, 900)
    const post = deferred<Response>()
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return post.promise
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    await userEvent.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await userEvent.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'new@x.test',
    )
    const submit = screen.getByRole('button', {
      name: zhCN.identifiers.add.submitEmail,
    })
    await userEvent.click(submit)
    await nextFrame()

    expect(
      submit,
      `focus while the send is in flight; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
    expect(submit).toHaveAttribute('aria-busy', 'true')

    post.resolve(
      jsonResponse(200, { state: 'pending', expires_in: 600, resend_after: 0 }),
    )
  })

  it('keeps focus on the row remove control while the removal is in flight', async () => {
    await page.viewport(1200, 900)
    const remove = deferred<Response>()
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, twoEmailProfile())
      }
      if (call.method === 'DELETE') {
        return remove.promise
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    await screen.findAllByText('a@x.test')
    const removeButton = within(rowScope('a@x.test')).getByRole('button', {
      name: zhCN.identifiers.remove,
    })
    await userEvent.click(removeButton)
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(
      within(dialog).getByRole('button', {
        name: zhCN.identifiers.confirmLabel,
      }),
    )
    await nextFrame()

    expect(
      removeButton,
      `focus while the removal is in flight; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
    expect(removeButton).toHaveAttribute('aria-busy', 'true')

    remove.resolve(new Response(null, { status: 204 }))
  })

  it('keeps focus on the sign-in switch while its write is in flight', async () => {
    await page.viewport(1200, 900)
    const put = deferred<Response>()
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, twoEmailProfile())
      }
      if (call.method === 'PUT') {
        return put.promise
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    const toggle = await screen.findByRole('switch', {
      name: zhCN.identifiers.action.disable,
    })
    toggle.focus()
    await userEvent.keyboard(' ')
    await nextFrame()

    expect(
      toggle,
      `focus while the switch write is in flight; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
    expect(toggle).toHaveAttribute('aria-disabled', 'true')

    put.resolve(new Response(null, { status: 204 }))
  })

  it('returns focus to the email sign-in select after an address is chosen', async () => {
    await page.viewport(1200, 900)
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, twoEmailProfile())
      }
      if (call.method === 'PUT') {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    const signInRegion = await screen.findByRole('region', {
      name: zhCN.identifiers.groups.signIn,
    })
    const combo = within(signInRegion).getByRole('combobox', {
      name: zhCN.identifiers.kind.email,
    })
    combo.focus()
    await userEvent.keyboard('{ArrowDown}')
    await screen.findByRole('listbox')
    await userEvent.click(screen.getByRole('option', { name: 'b@x.test' }))

    await waitFor(() =>
      expect(
        document.activeElement,
        `focus after choosing an address; document.activeElement is ${describeActiveElement()}`,
      ).toBe(combo),
    )
  })

  it('marks the page inert while the email sign-in menu is open and lifts it on Tab', async () => {
    await page.viewport(1200, 900)
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, twoEmailProfile())
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { container } = mount(respond)

    const signInRegion = await screen.findByRole('region', {
      name: zhCN.identifiers.groups.signIn,
    })
    const combo = within(signInRegion).getByRole('combobox', {
      name: zhCN.identifiers.kind.email,
    })
    combo.focus()
    await userEvent.keyboard('{ArrowDown}')
    const listbox = await screen.findByRole('listbox')
    const menuRoot = listbox.closest('.MuiModal-root')
    expect(menuRoot).not.toBeNull()

    await waitFor(() => expect(container).toHaveAttribute('inert'))
    expect(menuRoot).not.toHaveAttribute('inert')

    // A Select menu closes on Tab and hands focus back to the control.
    await userEvent.keyboard('{Tab}')
    await waitFor(() =>
      expect(
        document.activeElement,
        `focus after Tab; document.activeElement is ${describeActiveElement()}`,
      ).toBe(combo),
    )
    await waitFor(() => expect(container).not.toHaveAttribute('inert'))
  })

  it('keeps an empty add-form send in the tab order and says what is missing', async () => {
    await page.viewport(1200, 900)
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    await userEvent.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    const submit = screen.getByRole('button', {
      name: zhCN.identifiers.add.submitEmail,
    })
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).toHaveAccessibleDescription(
      zhCN.identifiers.add.emailEmpty,
    )
    screen.getByLabelText(zhCN.identifiers.add.value.labelEmail).focus()
    await userEvent.tab()
    await userEvent.tab()
    expect(
      submit,
      `focus after Tab from the value field; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
  })

  it("hands focus to the next row's remove control when the removed row leaves", async () => {
    await page.viewport(1200, 900)
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          removed
            ? profileBody({
                password_set: false,
                identifiers: [
                  identifier({
                    id: 'id-b',
                    kind: 'email',
                    value: 'b@x.test',
                    proven: true,
                    sign_in: true,
                  }),
                ],
              })
            : twoEmailProfile(),
        )
      }
      if (call.method === 'DELETE') {
        removed = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    await screen.findAllByText('a@x.test')
    const removeA = within(rowScope('a@x.test')).getByRole('button', {
      name: zhCN.identifiers.remove,
    })
    const removeB = within(rowScope('b@x.test')).getByRole('button', {
      name: zhCN.identifiers.remove,
    })
    await userEvent.click(removeA)
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(
      within(dialog).getByRole('button', {
        name: zhCN.identifiers.confirmLabel,
      }),
    )

    await waitFor(() =>
      expect(screen.queryByText('a@x.test')).toBeNull(),
    )
    await waitFor(() =>
      expect(
        removeB,
        `focus after the row left; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus(),
    )
  })

  it('hands focus to the section container when the only row leaves', async () => {
    await page.viewport(1200, 900)
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            identifiers: removed
              ? []
              : [
                  identifier({
                    id: 'id-only',
                    kind: 'email',
                    value: 'only@x.test',
                    proven: true,
                    sign_in: true,
                  }),
                ],
          }),
        )
      }
      if (call.method === 'DELETE') {
        removed = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    await screen.findAllByText('only@x.test')
    const remove = within(rowScope('only@x.test')).getByRole('button', {
      name: zhCN.identifiers.remove,
    })
    await userEvent.click(remove)
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(
      within(dialog).getByRole('button', {
        name: zhCN.identifiers.confirmLabel,
      }),
    )

    await waitFor(() =>
      expect(screen.getByText(zhCN.identifiers.empty.title)).toBeTruthy(),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    const container = screen.getByRole('group', {
      name: zhCN.identifiers.title,
    })
    await waitFor(() =>
      expect(
        container,
        `focus after the only row left; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus(),
    )
  })

  it('does not take focus back when the operator moved it during the removal', async () => {
    await page.viewport(1200, 900)
    const remove = deferred<Response>()
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          removed
            ? profileBody({
                password_set: false,
                identifiers: [
                  identifier({
                    id: 'id-b',
                    kind: 'email',
                    value: 'b@x.test',
                    proven: true,
                    sign_in: true,
                  }),
                ],
              })
            : twoEmailProfile(),
        )
      }
      if (call.method === 'DELETE') {
        return remove.promise.then(() => {
          removed = true
          return new Response(null, { status: 204 })
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    await screen.findAllByText('a@x.test')
    const removeA = within(rowScope('a@x.test')).getByRole('button', {
      name: zhCN.identifiers.remove,
    })
    await userEvent.click(removeA)
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(
      within(dialog).getByRole('button', {
        name: zhCN.identifiers.confirmLabel,
      }),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    const elsewhere = screen.getByRole('button', {
      name: zhCN.identifiers.add.open,
    })
    elsewhere.focus()
    await nextFrame()
    expect(elsewhere).toHaveFocus()

    remove.resolve(new Response(null, { status: 204 }))
    await waitFor(() => expect(screen.queryByText('a@x.test')).toBeNull())
    await nextFrame()
    expect(
      elsewhere,
      `focus after the row left; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
  })

  it("hands focus to the previous row's default action when the last row's choice removes it", async () => {
    await page.viewport(1200, 900)
    let defaultC = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            identifiers: [
              identifier({
                id: 'id-a',
                kind: 'email',
                value: 'a@x.test',
                proven: true,
                sign_in: true,
              }),
              identifier({
                id: 'id-b',
                kind: 'email',
                value: 'b@x.test',
                proven: true,
                sign_in: true,
              }),
              identifier({
                id: 'id-c',
                kind: 'email',
                value: 'c@x.test',
                proven: true,
                sign_in: true,
                default_contact: defaultC,
              }),
            ],
          }),
        )
      }
      if (
        call.method === 'PUT' &&
        call.path === '/api/v1/auth/identifiers/id-c'
      ) {
        defaultC = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} axis="contact" />,
    )

    await screen.findAllByText('c@x.test')
    const tableRow = (value: string): HTMLElement => {
      const row = screen.getByText(value).closest('tr')
      if (row === null) {
        throw new Error(`no table row shows ${value}`)
      }
      return row as HTMLElement
    }
    const previous = within(tableRow('b@x.test')).getByRole('button', {
      name: zhCN.identifiers.axes.defaultContact,
    })
    await userEvent.click(
      within(tableRow('c@x.test')).getByRole('button', {
        name: zhCN.identifiers.axes.defaultContact,
      }),
    )

    await waitFor(() =>
      expect(
        previous,
        `focus after the last row's choice; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus(),
    )
  })

  it('lands on the add region when the added username takes the entry away', async () => {
    await page.viewport(1200, 900)
    let added = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: added
              ? [
                  identifier({
                    id: 'id-user',
                    kind: 'username',
                    value: 'ada2',
                  }),
                ]
              : [],
          }),
        )
      }
      if (
        call.method === 'POST' &&
        call.path === '/api/v1/auth/identifiers'
      ) {
        added = true
        return jsonResponse(200, { state: 'added' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} emailAvailable={false} />,
    )

    await userEvent.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await userEvent.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelUsername),
      'ada2',
    )
    await userEvent.click(
      screen.getByRole('button', {
        name: zhCN.identifiers.add.submitUsername,
      }),
    )

    await screen.findByText('ada2')
    const region = screen.getByRole('group', {
      name: zhCN.identifiers.title,
    })
    await waitFor(() =>
      expect(
        region,
        `focus after the entry left; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus(),
    )
  })

  it('lands on the add region when a pending account takes the entry away with the added username', async () => {
    await page.viewport(1200, 900)
    let added = false
    const pendingAccount = {
      verification: { state: 'pending', email: 'ada@x.test', since: null },
    }
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            ...pendingAccount,
            identifiers: added
              ? [
                  identifier({
                    id: 'id-user',
                    kind: 'username',
                    value: 'ada2',
                  }),
                ]
              : [],
          }),
        )
      }
      if (
        call.method === 'POST' &&
        call.path === '/api/v1/auth/identifiers'
      ) {
        added = true
        return jsonResponse(200, { state: 'added' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(<IdentifiersSection session={rig.session} />)

    await userEvent.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await userEvent.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelUsername),
      'ada2',
    )
    await userEvent.click(
      screen.getByRole('button', {
        name: zhCN.identifiers.add.submitUsername,
      }),
    )

    await screen.findByText('ada2')
    const region = screen.getByRole('group', {
      name: zhCN.identifiers.title,
    })
    await waitFor(() =>
      expect(
        region,
        `focus after the entry left; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus(),
    )
  })

  it('returns focus to the add-form send when the answered proof lets the add leave', async () => {
    await page.viewport(1200, 900)
    const post = deferred<Response>()
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === CAPTCHA_PATH) {
        return jsonResponse(200, captchaChallenge())
      }
      if (call.method === 'POST' && call.path === ADD_PATH) {
        return post.promise
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection
        session={rig.session}
        axis="contact"
        humanGate={{
          phone: { required: false, provider: 'off' },
          other: { required: true, provider: 'captcha' },
        }}
      />,
    )

    await userEvent.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await userEvent.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'new@x.test',
    )
    const submit = screen.getByRole('button', {
      name: zhCN.identifiers.add.submitEmail,
    })
    submit.focus()
    await userEvent.keyboard('{Enter}')

    const answer = await screen.findByLabelText(CAPTCHA_ANSWER)
    answer.focus()
    await userEvent.type(answer, '12345')
    await userEvent.keyboard('{Enter}')

    await waitFor(() =>
      expect(screen.queryByLabelText(CAPTCHA_ANSWER)).toBeNull(),
    )
    expect(
      submit,
      `focus after the proof settled; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()

    post.resolve(
      jsonResponse(200, { state: 'pending', expires_in: 600, resend_after: 0 }),
    )
    await screen.findByLabelText(zhCN.identifiers.add.code.label)
  })
})
