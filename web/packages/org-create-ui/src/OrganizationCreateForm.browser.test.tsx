/**
 * The shared form's responsive geometry in Chromium: one column below the
 * theme's `sm` breakpoint and two from it up. Only a browser measures the
 * grid's real track placement; jsdom performs no layout.
 */

import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { page } from 'vitest/browser'
import { OrganizationCreateForm } from './OrganizationCreateForm.js'
import {
  EMPTY_ORGANIZATION_CREATE_VALUES,
  type OrganizationCreateField,
  type OrganizationCreateValues,
} from './validation.js'
import { renderWithProviders } from '../test-utils/render.js'
import enUS from './locales/en-US.json' with { type: 'json' }

function PlatformForm() {
  const [values, setValues] = useState<OrganizationCreateValues>(
    EMPTY_ORGANIZATION_CREATE_VALUES,
  )
  const change = (field: OrganizationCreateField, value: string) => {
    setValues((current) => ({ ...current, [field]: value }))
  }
  return (
    <OrganizationCreateForm
      mode="platform"
      values={values}
      onChange={change}
      submitLabel={enUS.submit}
      onSubmit={() => undefined}
    />
  )
}

function boxes() {
  return {
    id: screen.getByLabelText(enUS.fields.id).getBoundingClientRect(),
    alias: screen.getByLabelText(enUS.fields.alias).getBoundingClientRect(),
  }
}

describe('OrganizationCreateForm responsive grid', () => {
  it('stacks the fields in one column below sm', async () => {
    await page.viewport(599, 900)
    renderWithProviders(<PlatformForm />, { language: 'en-US' })

    const { id, alias } = boxes()
    // The alias is the next grid row: below the identifier, on the same
    // left edge.
    expect(alias.top).toBeGreaterThan(id.top + 1)
    expect(Math.abs(alias.left - id.left)).toBeLessThan(1)
    expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(
      window.innerWidth,
    )
  })

  it('puts the identifier and alias side by side from sm up', async () => {
    await page.viewport(900, 900)
    renderWithProviders(<PlatformForm />, { language: 'en-US' })

    const { id, alias } = boxes()
    // One row, two tracks: same top edge, the alias in the second track.
    expect(Math.abs(alias.top - id.top)).toBeLessThan(1)
    expect(alias.left).toBeGreaterThan(id.left + id.width / 2)
  })
})
