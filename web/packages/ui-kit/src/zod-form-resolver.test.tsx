/**
 * zodFormResolver contract: a passing schema yields no errors; a missing
 * value maps to the 'form.required' key and any other failure to
 * 'form.invalid'; zod issues fold into react-hook-form's nested error
 * object by path; and -- the point of the bridge -- FormField renders
 * those keys as the active language's text through the form family's
 * validation-error contract.
 */

import userEvent from '@testing-library/user-event'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { describe, expect, it } from 'vitest'
import { switchLanguage } from '@speed/i18n'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }
import { renderWithProviders } from '../test-utils/render.js'
import { TextFieldSlot } from '../test-utils/text-field-slot.js'
import { FormField } from './components/FormField.js'
import { FormLayout } from './components/FormLayout.js'
import { INVALID_ERROR_KEY, zodFormResolver } from './zod-form-resolver.js'

async function runResolver<TFieldValues extends Record<string, unknown>>(
  schema: z.ZodType<TFieldValues>,
  values: unknown,
) {
  const resolver = zodFormResolver(schema)
  return resolver(values as TFieldValues, undefined, {
    fields: {},
    shouldUseNativeValidation: false,
  })
}

describe('zodFormResolver', () => {
  it('returns the parsed values and no errors when the schema passes', async () => {
    const result = await runResolver(
      z.object({ name: z.string() }),
      { name: 'Ada' },
    )
    expect(result.errors).toEqual({})
    expect(result.values).toEqual({ name: 'Ada' })
  })

  it('maps a value that is missing to the required key', async () => {
    const result = await runResolver(z.object({ name: z.string() }), {})
    expect(result.errors).toMatchObject({
      name: { message: 'form.required' },
    })
  })

  it('maps a present but invalid value to the invalid key, never zod text', async () => {
    const result = await runResolver(
      z.object({ email: z.string().email() }),
      { email: 'not-an-email' },
    )
    expect(result.errors).toMatchObject({
      email: { message: INVALID_ERROR_KEY },
    })
  })

  it('folds a nested path into a nested error object', async () => {
    const result = await runResolver(
      z.object({ address: z.object({ city: z.string() }) }),
      { address: {} },
    )
    expect(result.errors).toMatchObject({
      address: { city: { message: 'form.required' } },
    })
  })
})

describe('zodFormResolver through FormField', () => {
  function renderRegisterForm() {
    function Harness() {
      const form = useForm<{ email: string }>({
        resolver: zodFormResolver(z.object({ email: z.string() })),
        mode: 'onSubmit',
      })
      return (
        <FormLayout
          form={form}
          onSubmit={() => {}}
          actions={<button type="submit">Submit</button>}
        >
          <FormField<{ email: string }, 'email'>
            name="email"
            render={(state) => <TextFieldSlot state={state} label="Email" />}
          />
        </FormLayout>
      )
    }
    return renderWithProviders(<Harness />)
  }

  it('renders the required key as the active language and follows a switch', async () => {
    const user = userEvent.setup()
    const utils = renderRegisterForm()

    await user.click(utils.getByRole('button', { name: 'Submit' }))
    expect(await utils.findByText(zhCN.form.required)).toBeInTheDocument()

    await switchLanguage(utils.i18n, 'en-US')
    expect(await utils.findByText(enUS.form.required)).toBeInTheDocument()
  })

  it('surfaces a nested issue on the nested field, proving RHF reads the folded path', async () => {
    function Harness() {
      const form = useForm<{ address: { city: string } }>({
        resolver: zodFormResolver(
          z.object({ address: z.object({ city: z.string() }) }),
        ),
        mode: 'onSubmit',
      })
      return (
        <FormLayout
          form={form}
          onSubmit={() => {}}
          actions={<button type="submit">Submit</button>}
        >
          <FormField<{ address: { city: string } }, 'address.city'>
            name="address.city"
            render={(state) => <TextFieldSlot state={state} label="City" />}
          />
        </FormLayout>
      )
    }
    const user = userEvent.setup()
    const utils = renderWithProviders(<Harness />)

    await user.click(utils.getByRole('button', { name: 'Submit' }))
    expect(await utils.findByText(zhCN.form.required)).toBeInTheDocument()
  })
})
