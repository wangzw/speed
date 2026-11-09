/**
 * The shared form's reading geometry in Chromium: one column at every
 * width, inside the shared form width. Only a browser measures the real
 * stack and track width; jsdom performs no layout.
 */

import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { page } from 'vitest/browser'
import { FORM_MAX_WIDTH } from '@speed/ui-kit'
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

describe('OrganizationCreateForm reading layout', () => {
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

  it('keeps the fields in one column within the reading width from sm up', async () => {
    await page.viewport(900, 900)
    renderWithProviders(<PlatformForm />, { language: 'en-US' })

    const { contactEmail, website } = boxes()
    // One column: the website is the next row, on the same left edge, and
    // the column stays inside the shared form width.
    expect(website.top).toBeGreaterThan(contactEmail.top + 1)
    expect(Math.abs(website.left - contactEmail.left)).toBeLessThan(1)
    expect(contactEmail.width).toBeLessThanOrEqual(FORM_MAX_WIDTH + 1)
    expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(
      window.innerWidth,
    )
  })
})
