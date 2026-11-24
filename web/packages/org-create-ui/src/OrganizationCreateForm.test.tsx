import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { OrganizationCreateForm } from './OrganizationCreateForm.js'
import {
  EMPTY_ORGANIZATION_CREATE_VALUES,
  type OrganizationCreateField,
  type OrganizationCreateValues,
} from './validation.js'
import { renderWithProviders } from '../test-utils/render.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

function ControlledForm({
  mode,
  onChange,
  onSubmit,
}: {
  readonly mode: 'platform' | 'self-service'
  readonly onChange?: (field: OrganizationCreateField, value: string) => void
  readonly onSubmit?: () => void
}) {
  const [values, setValues] = useState<OrganizationCreateValues>(
    EMPTY_ORGANIZATION_CREATE_VALUES,
  )
  return (
    <OrganizationCreateForm
      mode={mode}
      values={values}
      onChange={(field, value) => {
        setValues((current) => ({ ...current, [field]: value }))
        onChange?.(field, value)
      }}
      submitLabel="Create organization"
      onSubmit={onSubmit ?? (() => undefined)}
    />
  )
}

function SubmittingForm({
  onSubmit,
}: {
  readonly onSubmit?: () => void
}) {
  const [values, setValues] = useState<OrganizationCreateValues>(
    EMPTY_ORGANIZATION_CREATE_VALUES,
  )
  const [pending, setPending] = useState(false)
  return (
    <OrganizationCreateForm
      mode="self-service"
      values={values}
      onChange={(field, value) => {
        setValues((current) => ({ ...current, [field]: value }))
      }}
      disabled={pending}
      pending={pending}
      submitLabel="Create organization"
      onSubmit={() => {
        onSubmit?.()
        setPending(true)
      }}
    />
  )
}

function UnavailableForm({ onSubmit }: { readonly onSubmit: () => void }) {
  return (
    <OrganizationCreateForm
      mode="self-service"
      values={{ ...EMPTY_ORGANIZATION_CREATE_VALUES, name: 'acme' }}
      onChange={() => undefined}
      disabled
      submitLabel="Create organization"
      onSubmit={onSubmit}
    />
  )
}

describe('OrganizationCreateForm', () => {
  it('renders only the self-service fields in self-service mode', () => {
    renderWithProviders(<ControlledForm mode="self-service" />, {
      language: 'en-US',
    })
    expect(screen.getByLabelText(enUS.fields.name)).toBeInTheDocument()
    expect(screen.getByLabelText(enUS.fields.displayName)).toBeInTheDocument()
    expect(screen.getByLabelText(enUS.fields.description)).toBeInTheDocument()
    expect(
      screen.queryByLabelText(enUS.fields.contactEmail),
    ).not.toBeInTheDocument()
    expect(screen.queryByLabelText(enUS.fields.website)).not.toBeInTheDocument()
  })

  it('renders the profile fields in platform mode, with no identifier', () => {
    renderWithProviders(<ControlledForm mode="platform" />, {
      language: 'en-US',
    })
    for (const label of [
      enUS.fields.name,
      enUS.fields.displayName,
      enUS.fields.description,
      enUS.fields.contactEmail,
      enUS.fields.website,
    ]) {
      expect(screen.getByLabelText(label), label).toBeInTheDocument()
    }
    expect(screen.queryByLabelText('Identifier')).not.toBeInTheDocument()
  })

  it('reports each field change with its field name', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderWithProviders(
      <ControlledForm mode="self-service" onChange={onChange} />,
      { language: 'en-US' },
    )
    await user.type(screen.getByLabelText(enUS.fields.name), 'acme')
    expect(onChange).toHaveBeenLastCalledWith('name', 'acme')
    await user.type(screen.getByLabelText(enUS.fields.displayName), 'Acme')
    expect(onChange).toHaveBeenLastCalledWith('displayName', 'Acme')
  })

  it('ties a violation to its field and marks the control invalid', () => {
    renderWithProviders(
      <OrganizationCreateForm
        mode="self-service"
        values={{ ...EMPTY_ORGANIZATION_CREATE_VALUES, name: 'Acme' }}
        onChange={() => undefined}
        violations={{ name: 'nameShape' }}
        submitLabel="Create organization"
        onSubmit={() => undefined}
      />,
      { language: 'en-US' },
    )
    const name = screen.getByLabelText(enUS.fields.name)
    expect(name).toHaveAttribute('aria-invalid', 'true')
    expect(name).toHaveAccessibleDescription(enUS.validation.nameShape)
    expect(
      screen.queryByText(enUS.helpers.name, { exact: true }),
    ).not.toBeInTheDocument()
  })

  it('renders the standing hint when the field carries no violation', () => {
    renderWithProviders(<ControlledForm mode="self-service" />, {
      language: 'zh-CN',
    })
    expect(
      screen.getByLabelText(zhCN.fields.name),
    ).toHaveAccessibleDescription(zhCN.helpers.name)
  })

  it('submits through the form without a page reload', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    renderWithProviders(
      <ControlledForm mode="self-service" onSubmit={onSubmit} />,
      { language: 'en-US' },
    )
    await user.click(
      screen.getByRole('button', { name: 'Create organization' }),
    )
    expect(onSubmit).toHaveBeenCalledTimes(1)
  })

  it('refuses a programmatic submit while the write is in flight', () => {
    const request = vi.fn()
    const onSubmit = vi.fn(() => {
      request()
    })
    const { container } = renderWithProviders(
      <OrganizationCreateForm
        mode="self-service"
        values={{ ...EMPTY_ORGANIZATION_CREATE_VALUES, name: 'acme' }}
        onChange={() => undefined}
        pending
        submitLabel="Create organization"
        onSubmit={onSubmit}
      />,
      { language: 'en-US' },
    )

    fireEvent.submit(container.querySelector('form') as HTMLFormElement)

    expect(onSubmit).not.toHaveBeenCalled()
    expect(request).not.toHaveBeenCalled()
  })

  it('refuses a programmatic submit while the form is not operable', () => {
    const request = vi.fn()
    const onSubmit = vi.fn(() => {
      request()
    })
    const { container } = renderWithProviders(
      <OrganizationCreateForm
        mode="self-service"
        values={{ ...EMPTY_ORGANIZATION_CREATE_VALUES, name: 'acme' }}
        onChange={() => undefined}
        disabled
        submitLabel="Create organization"
        onSubmit={onSubmit}
      />,
      { language: 'en-US' },
    )

    fireEvent.submit(container.querySelector('form') as HTMLFormElement)

    expect(onSubmit).not.toHaveBeenCalled()
    expect(request).not.toHaveBeenCalled()
  })

  it('keeps focus in the field when Enter submits and the write is in flight', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    renderWithProviders(<SubmittingForm onSubmit={onSubmit} />, {
      language: 'en-US',
    })

    const name = screen.getByLabelText(enUS.fields.name)
    await user.click(name)
    await user.keyboard('acme')
    expect(name).toHaveFocus()

    await user.keyboard('{Enter}')

    expect(onSubmit).toHaveBeenCalledTimes(1)
    expect(name).toBeEnabled()
    expect(name).toHaveAttribute('readonly')
    expect(name).toHaveFocus()
    const submit = screen.getByRole('button', { name: 'Create organization' })
    expect(submit).toHaveAttribute('aria-busy', 'true')

    await user.keyboard('{Enter}')
    expect(onSubmit).toHaveBeenCalledTimes(1)
    expect(name).toHaveFocus()
  })

  it('keeps focus on the submit control when it starts the write', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    renderWithProviders(<SubmittingForm onSubmit={onSubmit} />, {
      language: 'en-US',
    })

    const submit = screen.getByRole('button', { name: 'Create organization' })
    await user.click(submit)

    expect(onSubmit).toHaveBeenCalledTimes(1)
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).toHaveAttribute('aria-busy', 'true')
    expect(submit).toHaveFocus()

    await user.click(submit)
    expect(onSubmit).toHaveBeenCalledTimes(1)
    expect(submit).toHaveFocus()
  })

  it('stays focusable but not operable while a switch is in flight', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    renderWithProviders(<UnavailableForm onSubmit={onSubmit} />, {
      language: 'en-US',
    })

    const name = screen.getByLabelText(enUS.fields.name)
    expect(name).toBeEnabled()
    expect(name).toHaveAttribute('readonly')

    const submit = screen.getByRole('button', { name: 'Create organization' })
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).not.toHaveAttribute('aria-busy')
    expect(submit).toHaveAccessibleDescription('')
    expect(submit).toBeEnabled()

    await user.click(submit)
    expect(onSubmit).not.toHaveBeenCalled()
    expect(submit).toHaveFocus()

    await user.click(name)
    await user.keyboard('{Enter}')
    expect(onSubmit).not.toHaveBeenCalled()
    expect(name).toHaveFocus()
  })
})
