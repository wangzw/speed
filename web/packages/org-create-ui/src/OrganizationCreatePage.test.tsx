/**
 * The self-service page: validation gates the write, a landed creation
 * runs the host's hand-over before navigating, a refused creation keeps
 * the draft and shows the mapped refusal, and a failed hand-over offers a
 * retry that never repeats the write.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { OrganizationCreatePage } from './OrganizationCreatePage.js'
import type { CreatedOrganization } from './validation.js'
import { renderWithProviders } from '../test-utils/render.js'
import enUS from './locales/en-US.json' with { type: 'json' }

const CREATED: CreatedOrganization = {
  alias: 'acme',
  displayName: 'Acme Ltd',
  description: 'A team',
}

const SUCCESS_HREF = '/organization/admin/profile'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

async function fillForm(
  user: ReturnType<typeof userEvent.setup>,
  displayName: string = 'Acme Ltd',
  description: string = 'A team',
) {
  await user.type(screen.getByLabelText(enUS.fields.alias), 'acme')
  await user.type(screen.getByLabelText(enUS.fields.displayName), displayName)
  if (description !== '') {
    await user.type(screen.getByLabelText(enUS.fields.description), description)
  }
}

function submit(user: ReturnType<typeof userEvent.setup>) {
  return user.click(screen.getByRole('button', { name: enUS.submit }))
}

function page(
  createOrganization: (input: unknown) => Promise<CreatedOrganization>,
  onCreated: (created: CreatedOrganization) => Promise<void>,
  onNavigate = vi.fn(),
) {
  renderWithProviders(
    <OrganizationCreatePage
      createOrganization={createOrganization as never}
      onCreated={onCreated}
      successHref={SUCCESS_HREF}
      onNavigate={onNavigate}
    />,
    { language: 'en-US' },
  )
  return onNavigate
}

describe('OrganizationCreatePage', () => {
  it('blocks the write until the alias and display name are valid', async () => {
    const user = userEvent.setup()
    const createOrganization = vi.fn()
    const onCreated = vi.fn()
    page(createOrganization, onCreated)

    await submit(user)

    expect(createOrganization).not.toHaveBeenCalled()
    expect(screen.getByLabelText(enUS.fields.alias)).toHaveAccessibleDescription(
      enUS.validation.required,
    )
    expect(
      screen.getByLabelText(enUS.fields.displayName),
    ).toHaveAccessibleDescription(enUS.validation.required)
  })

  it('shows a reserved alias in place before the first submit', async () => {
    const user = userEvent.setup()
    const createOrganization = vi.fn()
    const onCreated = vi.fn()
    page(createOrganization, onCreated)

    await user.type(screen.getByLabelText(enUS.fields.alias), 'admin')

    expect(
      screen.getByLabelText(enUS.fields.alias),
    ).toHaveAccessibleDescription(enUS.validation.aliasReserved)
    await submit(user)
    expect(createOrganization).not.toHaveBeenCalled()
  })

  it('creates with the trimmed values, hands over, then navigates', async () => {
    const user = userEvent.setup()
    const createOrganization = vi.fn(async () => CREATED)
    const onCreated = vi.fn(async () => undefined)
    const onNavigate = page(createOrganization, onCreated)

    await fillForm(user, '  Acme Ltd  ', 'A team')
    await submit(user)

    await waitFor(() => {
      expect(createOrganization).toHaveBeenCalledWith({
        alias: 'acme',
        displayName: 'Acme Ltd',
        description: 'A team',
      })
    })
    expect(onCreated).toHaveBeenCalledWith(CREATED)
    await waitFor(() => {
      expect(onNavigate).toHaveBeenCalledWith(SUCCESS_HREF)
    })
  })

  it('shows the entering state while the hand-over runs', async () => {
    const user = userEvent.setup()
    const createOrganization = vi.fn(async () => CREATED)
    const enter = deferred<void>()
    const onCreated = vi.fn(() => enter.promise)
    const onNavigate = page(createOrganization, onCreated)

    await fillForm(user)
    await submit(user)

    expect(await screen.findByText(enUS.switching.title)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: enUS.submit })).toBeDisabled()
    expect(onNavigate).not.toHaveBeenCalled()

    enter.resolve()
    await waitFor(() => {
      expect(onNavigate).toHaveBeenCalledWith(SUCCESS_HREF)
    })
  })

  it('keeps the draft and shows a refused creation through the resolver', async () => {
    const user = userEvent.setup()
    const createOrganization = vi.fn(async () => {
      throw { code: 'authn.contact_required' }
    })
    const onCreated = vi.fn()
    page(createOrganization, onCreated)

    await fillForm(user)
    await submit(user)

    expect(
      await screen.findByText(/Verify your email address/),
    ).toBeInTheDocument()
    expect(onCreated).not.toHaveBeenCalled()
    expect(screen.getByLabelText(enUS.fields.alias)).toHaveValue('acme')
    expect(screen.getByLabelText(enUS.fields.displayName)).toHaveValue(
      'Acme Ltd',
    )
  })

  it('names the stated wait on a throttled creation', async () => {
    const user = userEvent.setup()
    const createOrganization = vi.fn(async () => {
      throw { code: 'org.rate_limited', retryAfterMs: 45_000 }
    })
    page(createOrganization, vi.fn())

    await fillForm(user)
    await submit(user)

    expect(
      await screen.findByRole('alert'),
    ).toHaveTextContent(
      'Too many organization creations. Try again in 45 seconds.',
    )
  })

  it('retries only the hand-over after a failed enter, never the write', async () => {
    const user = userEvent.setup()
    const createOrganization = vi.fn(async () => CREATED)
    const onCreated = vi
      .fn()
      .mockRejectedValueOnce({ code: 'client.network' })
      .mockResolvedValueOnce(undefined)
    const onNavigate = page(createOrganization, onCreated)

    await fillForm(user)
    await submit(user)

    expect(await screen.findByText(enUS.switching.failedTitle)).toBeInTheDocument()
    expect(screen.getByText(enUS.switching.failedDescription)).toBeInTheDocument()
    expect(createOrganization).toHaveBeenCalledTimes(1)

    await user.click(
      screen.getByRole('button', { name: enUS.switching.retry }),
    )

    await waitFor(() => {
      expect(onCreated).toHaveBeenCalledTimes(2)
      expect(onNavigate).toHaveBeenCalledWith(SUCCESS_HREF)
    })
    expect(createOrganization).toHaveBeenCalledTimes(1)
    expect(onCreated).toHaveBeenLastCalledWith(CREATED)
  })
})
