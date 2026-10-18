/**
 * PhoneNumberField behaviour: the two controls and their accessible
 * names, the selector's search over country names and calling codes,
 * as-you-type space grouping, a pasted E.164 number switching the
 * selector and filling the local part, and the per-country validation
 * messages (including the empty-at-submit required message).
 */

import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { emittedStyleText } from '@speed/test-utils/emitted-css'
import { renderWithProviders } from '../../test-utils/render.js'
import { PhoneNumberField } from './PhoneNumberField.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }

const COUNTRY_LABEL = '国家/地区'
const NUMBER_LABEL = '手机号'

type HarnessProps = {
  readonly initialCountry?: string
  readonly initialNational?: string
  readonly showValidationError?: boolean
}

function Harness({
  initialCountry = 'US',
  initialNational = '',
  showValidationError = false,
}: HarnessProps) {
  const [country, setCountry] = useState(initialCountry)
  const [nationalNumber, setNationalNumber] = useState(initialNational)
  return (
    <PhoneNumberField
      country={country}
      nationalNumber={nationalNumber}
      onCountryChange={setCountry}
      onNationalNumberChange={setNationalNumber}
      countryLabel={COUNTRY_LABEL}
      numberLabel={NUMBER_LABEL}
      showValidationError={showValidationError}
    />
  )
}

describe('PhoneNumberField', () => {
  it('renders a country selector and a tel field under their accessible names', () => {
    renderWithProviders(<Harness initialCountry="CN" />)
    const selector = screen.getByRole('combobox', { name: COUNTRY_LABEL })
    expect((selector as HTMLInputElement).value).toContain('+86')
    const number = screen.getByLabelText(NUMBER_LABEL)
    expect(number).toHaveAttribute('type', 'tel')
    expect(number).toHaveAttribute('inputMode', 'tel')
  })

  it('searches countries by their ISO code', async () => {
    renderWithProviders(<Harness initialCountry="US" />)
    const user = userEvent.setup()
    const selector = screen.getByRole('combobox', { name: COUNTRY_LABEL })
    await user.type(selector, 'CN')
    expect(
      await screen.findByRole('option', { name: /中国/ }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('option', { name: /美国/ }),
    ).not.toBeInTheDocument()
  })

  it('groups the local number with spaces as the user types', async () => {
    renderWithProviders(<Harness initialCountry="CN" />)
    const user = userEvent.setup()
    const number = screen.getByLabelText(NUMBER_LABEL)
    await user.type(number, '13800000001')
    expect(number).toHaveValue('138 0000 0001')
  })

  it('switches the country and fills the local part from a pasted E.164 number', async () => {
    renderWithProviders(<Harness initialCountry="US" />)
    const user = userEvent.setup()
    const number = screen.getByLabelText(NUMBER_LABEL)
    await user.click(number)
    await user.paste('+8613800000001')
    expect(
      (
        screen.getByRole('combobox', {
          name: COUNTRY_LABEL,
        }) as HTMLInputElement
      ).value,
    ).toContain('+86')
    expect(number).toHaveValue('138 0000 0001')
  })

  it('keeps an unparsed leading + value visible while it is typed', async () => {
    renderWithProviders(<Harness initialCountry="US" />)
    const user = userEvent.setup()
    const number = screen.getByLabelText(NUMBER_LABEL)
    await user.type(number, '+86')
    // Too short to name a country yet: the raw value stays on screen
    // rather than being reduced to digits.
    expect(number).toHaveValue('+86')
  })

  it('names the calling code in an incomplete number error', async () => {
    renderWithProviders(
      <Harness initialCountry="CN" showValidationError />,
    )
    const user = userEvent.setup()
    const number = screen.getByLabelText(NUMBER_LABEL)
    await user.type(number, '138000')
    expect(
      screen.getByText(zhCN.phoneNumber.incomplete.replace('{{code}}', '86')),
    ).toBeInTheDocument()
    expect(number).toHaveAttribute('aria-invalid', 'true')
  })

  it('names the calling code in an invalid number error', async () => {
    renderWithProviders(
      <Harness initialCountry="US" initialNational="0000000000" showValidationError />,
    )
    expect(
      screen.getByText(zhCN.phoneNumber.invalid.replace('{{code}}', '1')),
    ).toBeInTheDocument()
  })

  it('asks for the required value when submitted empty', () => {
    renderWithProviders(<Harness showValidationError />)
    expect(screen.getByText(zhCN.form.required)).toBeInTheDocument()
    expect(screen.getByLabelText(NUMBER_LABEL)).toHaveAttribute(
      'aria-invalid',
      'true',
    )
  })

  it('sizes the selector with theme spacing, not raw pixels', () => {
    // `sx` turns a numeric `width` into pixels; the selector's width must
    // go through theme.spacing instead, or the field collapses to 15px at
    // phone widths. The base rule is the xs width: spacing(15) = 120px.
    renderWithProviders(<Harness initialCountry="CN" />)
    const root = document.querySelector('.MuiAutocomplete-root')
    expect(root).not.toBeNull()
    const css = emittedStyleText()
    const ownClasses = Array.from(root?.classList ?? []).filter((name) =>
      name.startsWith('css-'),
    )
    const widths = ownClasses
      .map((name) => new RegExp(`\\.${name}\\{[^}]*width:(\\d+)px`).exec(css)?.[1])
      .filter((width): width is string => width !== undefined)
    expect(widths).toContain('120')
  })
})
