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
    expect(screen.getByLabelText(enUS.fields.alias)).toBeInTheDocument()
    expect(screen.getByLabelText(enUS.fields.displayName)).toBeInTheDocument()
    expect(screen.getByLabelText(enUS.fields.description)).toBeInTheDocument()
    expect(screen.queryByLabelText(enUS.fields.id)).not.toBeInTheDocument()
    expect(
      screen.queryByLabelText(enUS.fields.contactEmail),
    ).not.toBeInTheDocument()
    expect(screen.queryByLabelText(enUS.fields.website)).not.toBeInTheDocument()
  })

  it('renders the identifier and profile fields in platform mode', () => {
    renderWithProviders(<ControlledForm mode="platform" />, {
      language: 'en-US',
    })
    for (const label of [
      enUS.fields.id,
      enUS.fields.alias,
      enUS.fields.displayName,
      enUS.fields.description,
      enUS.fields.contactEmail,
      enUS.fields.website,
    ]) {
      expect(screen.getByLabelText(label), label).toBeInTheDocument()
    }
  })

  it('reports each field change with its field name', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderWithProviders(
      <ControlledForm mode="self-service" onChange={onChange} />,
      { language: 'en-US' },
    )
    await user.type(screen.getByLabelText(enUS.fields.alias), 'acme')
    expect(onChange).toHaveBeenLastCalledWith('alias', 'acme')
    await user.type(screen.getByLabelText(enUS.fields.displayName), 'Acme')
    expect(onChange).toHaveBeenLastCalledWith('displayName', 'Acme')
  })

  it('ties a violation to its field and marks the control invalid', () => {
    renderWithProviders(
      <OrganizationCreateForm
        mode="self-service"
        values={{ ...EMPTY_ORGANIZATION_CREATE_VALUES, alias: 'Acme' }}
        onChange={() => undefined}
        violations={{ alias: 'aliasShape' }}
        submitLabel="Create organization"
        onSubmit={() => undefined}
      />,
      { language: 'en-US' },
    )
    const alias = screen.getByLabelText(enUS.fields.alias)
    expect(alias).toHaveAttribute('aria-invalid', 'true')
    expect(alias).toHaveAccessibleDescription(enUS.validation.aliasShape)
    // The standing hint gives way to the violation, never both.
    expect(
      screen.queryByText(enUS.helpers.alias, { exact: true }),
    ).not.toBeInTheDocument()
  })

  it('renders the standing hint when the field carries no violation', () => {
    renderWithProviders(<ControlledForm mode="self-service" />, {
      language: 'zh-CN',
    })
    expect(
      screen.getByLabelText(zhCN.fields.alias),
    ).toHaveAccessibleDescription(zhCN.helpers.alias)
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
