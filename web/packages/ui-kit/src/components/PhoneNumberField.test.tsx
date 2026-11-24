
import { createRef, useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { autocompleteClasses } from '@mui/material/Autocomplete'
import { inputLabelClasses } from '@mui/material/InputLabel'
import { emittedStyleText } from '@speed/test-utils/emitted-css'
import { renderWithProviders } from '../../test-utils/render.js'
import { PhoneNumberField } from './PhoneNumberField.js'
import { phoneCountries, phoneCountryName } from '../phone-number.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }

const COUNTRY_LABEL = 'Country/region'
const NUMBER_LABEL = 'Phone number'

type HarnessProps = {
  readonly initialCountry?: string
  readonly initialNational?: string
  readonly showValidationError?: boolean
  readonly numberHelperText?: string
  readonly readOnly?: boolean
  readonly disabled?: boolean
}

function Harness({
  initialCountry = 'US',
  initialNational = '',
  showValidationError = false,
  numberHelperText,
  readOnly,
  disabled,
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
      {...(numberHelperText === undefined ? {} : { numberHelperText })}
      showValidationError={showValidationError}
      {...(readOnly === true ? { readOnly } : {})}
      {...(disabled === true ? { disabled } : {})}
    />
  )
}

describe('PhoneNumberField', () => {
  const HELPER = 'Choose the country, then the local number.'

  it('sets the help line under the whole row, not under the number field', () => {
    renderWithProviders(<Harness numberHelperText={HELPER} />)
    const help = screen.getByText(HELPER)
    const number = screen.getByLabelText(NUMBER_LABEL)
    const selector = screen.getByRole('combobox', { name: COUNTRY_LABEL })
    const container = help.parentElement
    expect(container).not.toBeNull()
    expect(container?.contains(selector)).toBe(true)
    expect(container?.contains(number)).toBe(true)
    expect(container?.contains(help)).toBe(true)
    const row = selector.closest('div')
    expect(row).not.toBe(container)
    expect(number).toHaveAttribute('aria-describedby', help.id)
  })

  it('keeps a validation error under the number field and drops the help line', () => {
    renderWithProviders(
      <Harness numberHelperText={HELPER} showValidationError />,
    )
    expect(screen.getByText(zhCN.form.required)).toBeInTheDocument()
    expect(screen.queryByText(HELPER)).not.toBeInTheDocument()
    const number = screen.getByLabelText(NUMBER_LABEL)
    expect(number).toHaveAttribute('aria-invalid', 'true')
  })

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
    expect(options[0]?.textContent).toContain(phoneCountryName('DE', 'zh-CN'))
    expect(options[1]?.textContent).toContain(phoneCountryName('CN', 'zh-CN'))
    expect(options).toHaveLength(phoneCountries().length)
  })

  it('keeps the usual order when no preferred country is given', async () => {
    const user = userEvent.setup()
    renderWithProviders(<Harness initialCountry="US" />)
    await user.click(screen.getByRole('combobox', { name: COUNTRY_LABEL }))
    const options = await screen.findAllByRole('option')
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
    expect(number).toHaveValue('+86')
  })

  it('clears the host number while a leading + names no country yet', () => {
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
    renderWithProviders(<Harness initialCountry="CN" />)
    const root = document.querySelector(`.${autocompleteClasses.root}`)
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
    expect(css).toContain(
      `.${inputLabelClasses.root}.${inputLabelClasses.shrink}`,
    )
    expect(css).toContain('max-width:none')
  })

  it('keeps both parts focusable but frozen while read-only', async () => {
    const user = userEvent.setup()
    const countryChange = vi.fn()
    const numberChange = vi.fn()
    renderWithProviders(
      <PhoneNumberField
        country="US"
        nationalNumber="4155552671"
        onCountryChange={countryChange}
        onNationalNumberChange={numberChange}
        countryLabel={COUNTRY_LABEL}
        numberLabel={NUMBER_LABEL}
        readOnly
      />,
    )
    const selector = screen.getByRole('combobox', { name: COUNTRY_LABEL })
    const number = screen.getByLabelText(NUMBER_LABEL)
    selector.focus()
    expect(selector).toHaveFocus()
    number.focus()
    expect(number).toHaveFocus()
    expect(selector).toHaveAttribute('readonly')
    expect(number).toHaveAttribute('readonly')
    await user.type(number, '999')
    expect(number).toHaveValue('415 555 2671')
    expect(numberChange).not.toHaveBeenCalled()
    await user.click(selector)
    expect(screen.queryByRole('option')).not.toBeInTheDocument()
    await user.type(selector, 'CN')
    expect(countryChange).not.toHaveBeenCalled()
  })

  it('treats disabled as taking precedence over read-only', async () => {
    const user = userEvent.setup()
    const countryChange = vi.fn()
    const numberChange = vi.fn()
    renderWithProviders(
      <PhoneNumberField
        country="US"
        nationalNumber="4155552671"
        onCountryChange={countryChange}
        onNationalNumberChange={numberChange}
        countryLabel={COUNTRY_LABEL}
        numberLabel={NUMBER_LABEL}
        disabled
        readOnly
      />,
    )
    const selector = screen.getByRole('combobox', { name: COUNTRY_LABEL })
    const number = screen.getByLabelText(NUMBER_LABEL)
    expect(selector).toBeDisabled()
    expect(number).toBeDisabled()
    await user.click(selector)
    expect(screen.queryByRole('option')).not.toBeInTheDocument()
    expect(countryChange).not.toHaveBeenCalled()
    expect(numberChange).not.toHaveBeenCalled()
  })

  it('keeps the validation message while read-only', () => {
    renderWithProviders(
      <Harness
        initialCountry="US"
        initialNational="0000000000"
        showValidationError
        readOnly
      />,
    )
    expect(
      screen.getByText(zhCN.phoneNumber.invalid.replace('{{code}}', '1')),
    ).toBeInTheDocument()
    expect(screen.getByLabelText(NUMBER_LABEL)).toHaveAttribute(
      'aria-invalid',
      'true',
    )
  })

  it('forwards sx, className and ref to the row box', () => {
    const ref = createRef<HTMLDivElement>()
    const { container } = renderWithProviders(
      <PhoneNumberField
        ref={ref}
        className="host-phone"
        sx={{ mt: 4 }}
        country="US"
        nationalNumber=""
        onCountryChange={() => undefined}
        onNationalNumberChange={() => undefined}
        countryLabel={COUNTRY_LABEL}
        numberLabel={NUMBER_LABEL}
      />,
    )
    const root = container.firstChild as HTMLElement
    expect(root).toHaveClass('host-phone')
    expect(root).toBe(ref.current)
    expect(root).toHaveStyle({ marginTop: '32px' })
  })
})
