/**
 * PhoneNumberField behaviour: the two controls and their accessible
 * names, the selector's search over country names and calling codes,
 * as-you-type space grouping, a pasted E.164 number switching the
 * selector and filling the local part, and the per-country validation
 * messages (including the empty-at-submit required message).
 */

import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { emittedStyleText } from '@speed/test-utils/emitted-css'
import { renderWithProviders } from '../../test-utils/render.js'
import { PhoneNumberField } from './PhoneNumberField.js'
import { phoneCountries, phoneCountryName } from '../phone-number.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }

// The host supplies both labels as props; the field renders them as the
// controls' accessible names.
const COUNTRY_LABEL = 'Country/region'
const NUMBER_LABEL = 'Phone number'

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
    // The options carry the country's name in the active language; the
    // expected names come from the same source the field reads them from,
    // never from a sentence typed into the test. The matcher is a substring
    // test rather than a RegExp so a name with regex metacharacters cannot
    // break the query.
    const optionNamed = (country: string) => {
      const name = phoneCountryName(country, 'zh-CN')
      return (accessibleName: string) => accessibleName.includes(name)
    }
    expect(
      await screen.findByRole('option', { name: optionNamed('CN') }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('option', { name: optionNamed('US') }),
    ).not.toBeInTheDocument()
  })

  it('lists the preferred countries first, in the given order, skipping unknown codes', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <PhoneNumberField
        country="US"
        nationalNumber=""
        onCountryChange={() => undefined}
        onNationalNumberChange={() => undefined}
        countryLabel={COUNTRY_LABEL}
        numberLabel={NUMBER_LABEL}
        preferredCountries={['ZZ', 'DE', 'CN']}
      />,
    )
    await user.click(screen.getByRole('combobox', { name: COUNTRY_LABEL }))
    const options = await screen.findAllByRole('option')
    // 'ZZ' is not a country the metadata covers, so it is skipped; the
    // covered preferred codes lead in the caller's order.
    expect(options[0]?.textContent).toContain(phoneCountryName('DE', 'zh-CN'))
    expect(options[1]?.textContent).toContain(phoneCountryName('CN', 'zh-CN'))
    expect(options).toHaveLength(phoneCountries().length)
  })

  it('keeps the usual order when no preferred country is given', async () => {
    const user = userEvent.setup()
    renderWithProviders(<Harness initialCountry="US" />)
    await user.click(screen.getByRole('combobox', { name: COUNTRY_LABEL }))
    const options = await screen.findAllByRole('option')
    // The sorted order this package has always rendered: the first option
    // is whichever country sorts first under the active language.
    const expectedFirst = [...phoneCountries()]
      .map((code) => ({ code, name: phoneCountryName(code, 'zh-CN') }))
      .sort((left, right) => left.name.localeCompare(right.name, 'zh-CN'))[0]
    expect(options[0]?.textContent).toContain(expectedFirst?.name)
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

  it('clears the host number while a leading + names no country yet', () => {
    // The draft replaces the number on screen; if the host kept the old
    // number, a submit would send it while the viewer reads the new one.
    function Tracked() {
      const [nationalNumber, setNationalNumber] = useState('4155552671')
      return (
        <>
          <PhoneNumberField
            country="US"
            nationalNumber={nationalNumber}
            onCountryChange={() => undefined}
            onNationalNumberChange={setNationalNumber}
            countryLabel={COUNTRY_LABEL}
            numberLabel={NUMBER_LABEL}
            showValidationError
          />
          <output data-testid="host-number">{nationalNumber}</output>
        </>
      )
    }
    renderWithProviders(<Tracked />)
    fireEvent.change(screen.getByLabelText(NUMBER_LABEL), {
      target: { value: '+' },
    })
    expect(screen.getByTestId('host-number').textContent).toBe('')
    // The draft names no country, so the message cannot name one either.
    expect(
      screen.getByText(zhCN.phoneNumber.incompleteInternational),
    ).toBeInTheDocument()
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

  it('sizes the selector to its flag-and-code content, not half the row', () => {
    // `sx` turns a numeric `width` into pixels; the selector's width must
    // go through theme.spacing instead, or the field collapses to 12px at
    // phone widths. The base rule is the xs width: spacing(12) = 96px;
    // from sm it is spacing(14) = 112px.
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
    expect(widths).toContain('96')
    expect(css).toContain('width:112px')
    // The floating label keeps its natural width instead of MUI's
    // ellipsizing cap, so "Country/region" is not truncated at 96px.
    expect(css).toContain('.MuiInputLabel-root.MuiInputLabel-shrink')
    expect(css).toContain('max-width:none')
  })
})
