/**
 * PhoneNumberField: the two-part phone-number control of the form family.
 *
 * A phone number is edited as a country/region selector and a local
 * number, and reaches the host as the two parts; `toE164` in
 * src/phone-number.ts joins them for submission. The selector is a
 * searchable combobox over the countries the phone metadata covers --
 * each option shows its flag, its name in the active language
 * (`Intl.DisplayNames`) and its calling code, and the search matches the
 * name, the ISO code and the calling code. The closed control shows the
 * flag and the calling code so it stays narrow at phone widths.
 *
 * The local field is `type=tel` with `inputMode=tel`, grouped with spaces
 * as the viewer types. Typing or pasting a value that starts with `+`
 * parses it: once enough of it exists to name a country the selector
 * switches and the local part is filled, so a pasted E.164 number needs no
 * country pick first. Until then the raw text stays visible, so typing a
 * `+`-number by hand does not lose its leading sign.
 *
 * Validation is per country, through libphonenumber-js: a number too short
 * for the country reads "incomplete under +<code>", an implausible one
 * reads "invalid under +<code>". The message appears after the field is
 * left or after a submit asked for it (`showValidationError`), and the
 * empty-at-submit case reads the form's required message. The host keeps
 * the parts and decides when to submit; this component never sends
 * anything.
 */

import { useId, useMemo, useState } from 'react'
import type { ChangeEvent, FocusEvent } from 'react'
import Autocomplete from '@mui/material/Autocomplete'
import Box from '@mui/material/Box'
import TextField from '@mui/material/TextField'
import type { SxProps, Theme } from '@mui/material/styles'
import { useUiKitTranslation } from '../internal/translation.js'
import {
  formatNationalPhoneNumber,
  nationalNumberDigits,
  parseInternationalPhoneNumber,
  phoneCountries,
  phoneCountryCallingCode,
  phoneCountryName,
  validatePhoneNumber,
} from '../phone-number.js'

/** One selector entry: a covered country under the active language. */
type CountryOption = {
  readonly code: string
  readonly name: string
  readonly callingCode: string
  readonly flag: string
}

/** The regional-indicator flag glyph of an ISO alpha-2 code. */
function flagOf(code: string): string {
  return code.replace(
    /[A-Z]/g,
    (letter) => String.fromCodePoint(0x1f1e6 + letter.charCodeAt(0) - 65),
  )
}

export type PhoneNumberFieldProps = {
  /** The selected country, ISO 3166-1 alpha-2. */
  readonly country: string
  /** The local number, digits only. */
  readonly nationalNumber: string
  /** Fired when the selector changes the country. */
  readonly onCountryChange: (country: string) => void
  /** Fired with the local digits after typing, deleting or pasting. */
  readonly onNationalNumberChange: (nationalNumber: string) => void
  /** Accessible name of the country/region selector. */
  readonly countryLabel: string
  /** Accessible name of the local number field. */
  readonly numberLabel: string
  /** Helper text under the number field while it holds no error. */
  readonly numberHelperText?: string
  /** Disables both controls. */
  readonly disabled?: boolean
  /** The number field's autocomplete token. */
  readonly autoComplete?: string
  /**
   * Show the validation message. The host sets this after a submit that
   * could not assemble a number, so a viewer is not scolded mid-typing.
   */
  readonly showValidationError?: boolean
}

/**
 * The selector fits its flag-and-calling-code content: 96px at phone
 * widths and 112px from sm, rather than a share of the row. The number
 * field takes the remaining width.
 */
const COUNTRY_WIDTH_SX: SxProps<Theme> = (theme) => ({
  width: { xs: theme.spacing(12), sm: theme.spacing(14) },
  flexShrink: 0,
  // The floating label keeps its natural width. MUI caps it at
  // `calc(133% - 32px)`, which ellipsizes "Country/region" at the 96px
  // phone width even though the scaled text fits inside the field.
  '& .MuiInputLabel-root.MuiInputLabel-shrink': { maxWidth: 'none' },
})

export function PhoneNumberField({
  country,
  nationalNumber,
  onCountryChange,
  onNationalNumberChange,
  countryLabel,
  numberLabel,
  numberHelperText,
  disabled = false,
  autoComplete = 'tel',
  showValidationError = false,
}: PhoneNumberFieldProps) {
  const { t, i18n } = useUiKitTranslation()
  const countryFieldId = useId()
  const numberFieldId = useId()
  // A value that starts with `+` but is still too short to name a country
  // stays on screen verbatim; the parsed parts replace it once they can.
  const [internationalDraft, setInternationalDraft] = useState<string | null>(
    null,
  )
  const [touched, setTouched] = useState(false)

  const options = useMemo<CountryOption[]>(
    () =>
      phoneCountries()
        .map((code) => ({
          code,
          name: phoneCountryName(code, i18n.language),
          callingCode: phoneCountryCallingCode(code) ?? '',
          flag: flagOf(code),
        }))
        .sort((left, right) => left.name.localeCompare(right.name, i18n.language)),
    [i18n.language],
  )

  const selected =
    options.find((option) => option.code === country) ?? null
  const callingCode = selected?.callingCode ?? ''

  const displayValue =
    internationalDraft ??
    (nationalNumber === ''
      ? ''
      : formatNationalPhoneNumber(country, nationalNumber))

  const validation =
    nationalNumber === ''
      ? internationalDraft === null
        ? null
        : 'incomplete'
      : validatePhoneNumber(country, nationalNumber)

  const messageKey =
    // A draft still names no country, so naming the selected one would
    // point the viewer at a country the value does not belong to.
    internationalDraft !== null
      ? 'phoneNumber.incompleteInternational'
      : validation === 'incomplete'
        ? 'phoneNumber.incomplete'
        : validation === 'invalid'
          ? 'phoneNumber.invalid'
          : null
  const errorText =
    messageKey !== null
      ? touched || showValidationError
        ? t(messageKey, { code: callingCode })
        : null
      : showValidationError &&
          nationalNumber === '' &&
          internationalDraft === null
        ? t('form.required')
        : null

  const handleNumberChange = (event: ChangeEvent<HTMLInputElement>): void => {
    const raw = event.target.value
    if (raw.trimStart().startsWith('+')) {
      const parts = parseInternationalPhoneNumber(raw)
      if (parts === null) {
        setInternationalDraft(raw)
        // The draft replaces the number on screen, so the value the host
        // holds must go with it: otherwise a submit would send the
        // previous number while the viewer reads the new, unfinished one.
        onNationalNumberChange('')
        return
      }
      setInternationalDraft(null)
      onCountryChange(parts.country)
      onNationalNumberChange(parts.nationalNumber)
      return
    }
    setInternationalDraft(null)
    onNationalNumberChange(nationalNumberDigits(raw))
  }

  return (
    <Box
      sx={{
        display: 'flex',
        flexWrap: 'nowrap',
        alignItems: 'flex-start',
        gap: 1,
        width: '100%',
      }}
    >
      <Autocomplete<CountryOption, false, true, false, 'div', string>
        id={countryFieldId}
        options={options}
        // The selection is the country code itself, which is exactly the
        // value the host holds: options carry the labels, the selection
        // carries the code.
        getOptionValue={(option) => option.code}
        value={selected?.code ?? undefined}
        onChange={(_event, code) => {
          if (code !== null) {
            setInternationalDraft(null)
            onCountryChange(code)
          }
        }}
        getOptionLabel={(option) =>
          `${option.flag} +${option.callingCode}`
        }
        filterOptions={(candidates, state) => {
          const query = state.inputValue.trim().toLowerCase()
          if (query === '') {
            return candidates
          }
          const digits = nationalNumberDigits(query)
          return candidates.filter(
            (option) =>
              option.name.toLowerCase().includes(query) ||
              option.code.toLowerCase().includes(query) ||
              (digits !== '' && option.callingCode.startsWith(digits)),
          )
        }}
        isOptionEqualToValue={(option, value) => option.code === value}
        renderOption={(props, option) => {
          const { key, ...optionProps } = props
          return (
            <Box
              component="li"
              key={key}
              {...optionProps}
              sx={{ display: 'flex', alignItems: 'center', gap: 1 }}
            >
              <Box component="span" aria-hidden>
                {option.flag}
              </Box>
              <Box
                component="span"
                sx={{
                  flex: 1,
                  minWidth: 0,
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                }}
              >
                {option.name}
              </Box>
              <Box component="span" sx={{ color: 'text.secondary' }}>
                {`+${option.callingCode}`}
              </Box>
            </Box>
          )
        }}
        renderInput={(params) => (
          <TextField
            {...params}
            label={countryLabel}
            autoComplete="tel-country-code"
            slotProps={{
              ...params.slotProps,
              htmlInput: {
                ...params.slotProps.htmlInput,
                // The field holds the selected label; selecting it on
                // focus makes the next keystroke a search rather than an
                // append to that label. The slot's own focus handler must
                // keep running: it is what marks the control focused, and
                // an unfocused control resets its input on every render.
                onFocus: (event: FocusEvent<HTMLInputElement>) => {
                  params.slotProps.htmlInput.onFocus?.(event)
                  event.target.select()
                },
              },
            }}
          />
        )}
        disableClearable
        disabled={disabled}
        slotProps={{
          popper: {
            // The field is deliberately narrow; the list widens to the
            // option text instead of clipping names to the field width.
            style: { width: 'max-content', maxWidth: 'calc(100vw - 2rem)' },
          },
        }}
        sx={COUNTRY_WIDTH_SX}
      />
      <TextField
        id={numberFieldId}
        label={numberLabel}
        type="tel"
        autoComplete={autoComplete}
        fullWidth
        value={displayValue}
        onChange={handleNumberChange}
        onBlur={() => setTouched(true)}
        disabled={disabled}
        error={errorText !== null}
        helperText={errorText ?? numberHelperText}
        slotProps={{ htmlInput: { inputMode: 'tel' } }}
        sx={{ flex: 1, minWidth: 0 }}
      />
    </Box>
  )
}
