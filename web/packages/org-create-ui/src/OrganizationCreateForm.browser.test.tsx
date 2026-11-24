import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { page, userEvent } from 'vitest/browser'
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

function SubmittingForm({ submits }: { readonly submits: { count: number } }) {
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
      submitLabel={enUS.submit}
      onSubmit={() => {
        submits.count += 1
        setPending(true)
      }}
    />
  )
}

function UnavailableForm({
  submits,
}: {
  readonly submits: { count: number }
}) {
  return (
    <OrganizationCreateForm
      mode="self-service"
      values={{ ...EMPTY_ORGANIZATION_CREATE_VALUES, name: 'acme' }}
      onChange={() => undefined}
      disabled
      submitLabel={enUS.submit}
      onSubmit={() => {
        submits.count += 1
      }}
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
    expect(website.top).toBeGreaterThan(contactEmail.top + 1)
    expect(Math.abs(website.left - contactEmail.left)).toBeLessThan(1)
    expect(contactEmail.width).toBeLessThanOrEqual(FORM_MAX_WIDTH + 1)
    expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(
      window.innerWidth,
    )
  })
})

describe('OrganizationCreateForm focus in Chromium', () => {
  it('keeps focus in the field when Enter submits and the write is in flight', async () => {
    await page.viewport(900, 700)
    const submits = { count: 0 }
    renderWithProviders(<SubmittingForm submits={submits} />, {
      language: 'en-US',
    })

    const name = await screen.findByLabelText(enUS.fields.name)
    await userEvent.click(name)
    await userEvent.type(name, 'acme')
    expect(name).toHaveFocus()

    await userEvent.keyboard('{Enter}')

    expect(submits.count).toBe(1)
    expect(name).toBeEnabled()
    expect(name).toHaveAttribute('readonly')
    expect(name).toHaveFocus()

    await userEvent.keyboard('{Enter}')
    expect(submits.count).toBe(1)
    expect(name).toHaveFocus()
  })

  it('keeps focus on the submit control when it starts the write', async () => {
    await page.viewport(900, 700)
    const submits = { count: 0 }
    renderWithProviders(<SubmittingForm submits={submits} />, {
      language: 'en-US',
    })

    const submit = await screen.findByRole('button', { name: enUS.submit })
    await userEvent.click(submit)

    expect(submits.count).toBe(1)
    expect(submit).toHaveAttribute('aria-busy', 'true')
    expect(submit).toHaveFocus()

    submit.click()
    expect(submits.count).toBe(1)
    expect(submit).toHaveFocus()
  })

  it('stays focusable but not operable while a switch is in flight', async () => {
    await page.viewport(900, 700)
    const submits = { count: 0 }
    renderWithProviders(<UnavailableForm submits={submits} />, {
      language: 'en-US',
    })

    const name = await screen.findByLabelText(enUS.fields.name)
    expect(name).toBeEnabled()
    expect(name).toHaveAttribute('readonly')

    const submit = screen.getByRole('button', { name: enUS.submit })
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).not.toHaveAttribute('aria-busy')
    expect(submit).toHaveAccessibleDescription('')

    submit.focus()
    expect(submit).toHaveFocus()
    submit.click()
    expect(submits.count).toBe(0)
    expect(submit).toHaveFocus()
    await userEvent.keyboard('{Enter}')
    expect(submits.count).toBe(0)
    expect(submit).toHaveFocus()

    await userEvent.click(name)
    expect(name).toHaveFocus()
    await userEvent.keyboard('{Enter}')
    expect(submits.count).toBe(0)
    expect(name).toHaveFocus()
  })
})
