/**
 * The two-part mobile-number control: a country/region selector and a local
 * number, validated per country against libphonenumber-js's `mobile`
 * metadata. The host owns both parts and does the sending.
 */

import { forwardRef, useId, useMemo, useState } from 'react'
import type { ChangeEvent, FocusEvent, Ref } from 'react'
import Autocomplete from '@mui/material/Autocomplete'
import Box from '@mui/material/Box'
import TextField from '@mui/material/TextField'
import FormHelperText from '@mui/material/FormHelperText'
import type { SxProps, Theme } from '@mui/material/styles'
import { useUiKitTranslation } from '../internal/translation.js'
import { layerSx } from '../internal/root-style-props.js'
import type { RootStyleProps } from '../internal/root-style-props.js'
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

export type PhoneNumberFieldProps = RootStyleProps & {
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
  /** Keeps both parts focusable but unchangeable, for a write in flight. */
  readonly readOnly?: boolean
  /** The number field's autocomplete token. */
  readonly autoComplete?: string
  /** Show the validation message; the host sets it after a failed submit. */
  readonly showValidationError?: boolean
  /**
   * Country codes to list first, in order; a code the metadata does not
   * cover is skipped. Omitting it keeps the usual order.
   */
  readonly preferredCountries?: readonly string[]
}

/** The selector fits its flag-and-calling-code content; the number field takes the rest. */
const COUNTRY_WIDTH_SX: SxProps<Theme> = (theme) => ({
  width: { xs: theme.spacing(12), sm: theme.spacing(14) },
  flexShrink: 0,
  // MUI caps the floating label at `calc(133% - 32px)`, which ellipsizes it in this width.
  '& .MuiInputLabel-root.MuiInputLabel-shrink': { maxWidth: 'none' },
})

function PhoneNumberFieldInner(
  {
    country,
    nationalNumber,
    onCountryChange,
    onNationalNumberChange,
    countryLabel,
    numberLabel,
    numberHelperText,
    disabled = false,
    readOnly = false,
    autoComplete = 'tel',
    showValidationError = false,
    preferredCountries,
    sx,
    className,
  }: PhoneNumberFieldProps,
  ref: Ref<HTMLDivElement>,
) {
  const { t, i18n } = useUiKitTranslation()
  // A disabled control is unchangeable already, so disabled wins when both are set.
  const readOnlyEffective = readOnly && !disabled
  const countryFieldId = useId()
  const numberFieldId = useId()
  const numberHelpId = `${numberFieldId}-help`
  // A `+` value too short to name a country stays on screen verbatim.
  const [internationalDraft, setInternationalDraft] = useState<string | null>(
    null,
  )
  const [touched, setTouched] = useState(false)

  const options = useMemo<CountryOption[]>(() => {
    const sorted = phoneCountries()
      .map((code) => ({
        code,
        name: phoneCountryName(code, i18n.language),
        callingCode: phoneCountryCallingCode(code) ?? '',
        flag: flagOf(code),
      }))
      .sort((left, right) => left.name.localeCompare(right.name, i18n.language))
    if (preferredCountries === undefined || preferredCountries.length === 0) {
      return sorted
    }
    // Deleting from the map leaves the rest in their usual sorted order.
    const remaining = new Map(sorted.map((option) => [option.code, option]))
    const first: CountryOption[] = []
    for (const code of preferredCountries) {
      const option = remaining.get(code)
      if (option !== undefined) {
        first.push(option)
        remaining.delete(code)
      }
    }
    return [...first, ...remaining.values()]
  }, [i18n.language, preferredCountries])

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
    // A draft names no country, so naming the selected one would be wrong.
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
        // The host value must follow the screen, or a submit would send the previous number.
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
      ref={ref}
      className={className}
      sx={layerSx(
        {
          display: 'flex',
          flexDirection: 'column',
          gap: 0.5,
          width: '100%',
        },
        sx,
      )}
    >
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
                // The slot's own handler must run: it marks the control focused,
                // and an unfocused control resets its input on every render.
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
        readOnly={readOnlyEffective}
        slotProps={{
          popper: {
            // Widen the list to the option text instead of clipping names to the field.
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
          helperText={errorText ?? undefined}
          slotProps={{
            htmlInput: {
              inputMode: 'tel',
              readOnly: readOnlyEffective,
              ...(errorText === null && numberHelperText !== undefined
                ? { 'aria-describedby': numberHelpId }
                : {}),
            },
          }}
          sx={{ flex: 1, minWidth: 0 }}
        />
      </Box>
      {errorText === null && numberHelperText !== undefined ? (
        <FormHelperText id={numberHelpId} sx={{ mx: 0 }}>
          {numberHelperText}
        </FormHelperText>
      ) : null}
    </Box>
  )
}

/** `sx`, `className` and the ref reach the field as a whole, help line included. */
export const PhoneNumberField = forwardRef(PhoneNumberFieldInner)
