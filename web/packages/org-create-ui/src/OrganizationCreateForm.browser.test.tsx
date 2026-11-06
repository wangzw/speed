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
    contactEmail: screen
      .getByLabelText(enUS.fields.contactEmail)
      .getBoundingClientRect(),
    website: screen.getByLabelText(enUS.fields.website).getBoundingClientRect(),
  }
}

describe('OrganizationCreateForm responsive grid', () => {
  it('stacks the fields in one column below sm', async () => {
    await page.viewport(599, 900)
    renderWithProviders(<PlatformForm />, { language: 'en-US' })

    const { contactEmail, website } = boxes()
    // The website is the next grid row: below the contact address, on the
    // same left edge.
    expect(website.top).toBeGreaterThan(contactEmail.top + 1)
    expect(Math.abs(website.left - contactEmail.left)).toBeLessThan(1)
    expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(
      window.innerWidth,
    )
  })

  it('puts the contact address and website side by side from sm up', async () => {
    await page.viewport(900, 900)
    renderWithProviders(<PlatformForm />, { language: 'en-US' })

    const { contactEmail, website } = boxes()
    // One row, two tracks: same top edge, the website in the second track.
    expect(Math.abs(website.top - contactEmail.top)).toBeLessThan(1)
    expect(website.left).toBeGreaterThan(contactEmail.left + contactEmail.width / 2)
  })
})
