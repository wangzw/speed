/**
 * The shared form: its field set per surface, its controlled change
 * reporting, the field-tied violation text, and the submit action.
 */

import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
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
    // The server mints the internal identifier; no surface asks for it.
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
    // The standing hint gives way to the violation, never both.
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
})
