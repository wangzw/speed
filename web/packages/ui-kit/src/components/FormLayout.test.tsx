
import { createRef } from 'react'
import { act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useForm } from 'react-hook-form'
import type { SubmitHandler } from 'react-hook-form'
import { describe, expect, it, vi } from 'vitest'
import { switchLanguage } from '@speed/i18n'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import enUS from '../locales/en-US.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import { emittedStyleText } from '@speed/test-utils/emitted-css'
import { TextFieldSlot } from '../../test-utils/text-field-slot.js'
import { FormLayout } from './FormLayout.js'
import { FormField } from './FormField.js'

interface MemberFormValues {
  name: string
  email: string
}

function FullForm({
  onSubmit,
  defaultValues,
}: {
  onSubmit: SubmitHandler<MemberFormValues>
  defaultValues?: MemberFormValues
}) {
  const form = useForm<MemberFormValues>({
    mode: 'onSubmit',
    defaultValues: defaultValues ?? { name: '', email: '' },
  })
  return (
    <FormLayout
      form={form}
      onSubmit={onSubmit}
      actions={<button type="submit">Save member</button>}
    >
      <FormField name="name" required render={(s) => <TextFieldSlot state={s} label="Name" />} />
      <FormField
        name="email"
        required
        render={(s) => <TextFieldSlot state={s} label="Email" type="email" />}
      />
    </FormLayout>
  )
}

describe('FormLayout', () => {
  it('submits nothing while fields are invalid and shows every translated error', async () => {
    const onSubmit = vi.fn<SubmitHandler<MemberFormValues>>()
    const { getByRole, getAllByText } = renderWithProviders(
      <FullForm onSubmit={onSubmit} />,
    )
    const user = userEvent.setup()
    await user.click(getByRole('button', { name: 'Save member' }))

    expect(onSubmit).not.toHaveBeenCalled()
    expect(getAllByText(zhCN.form.required)).toHaveLength(2)
  })

  it('submits the collected values once every field is valid', async () => {
    const onSubmit = vi.fn<SubmitHandler<MemberFormValues>>()
    const utils = renderWithProviders(<FullForm onSubmit={onSubmit} />)
    const user = userEvent.setup()
    await user.click(utils.getByRole('button', { name: 'Save member' }))
    expect(onSubmit).not.toHaveBeenCalled()

    await user.type(utils.getByLabelText('Name'), 'Ada')
    await user.type(utils.getByLabelText('Email'), 'ada@example.com')
    await user.click(utils.getByRole('button', { name: 'Save member' }))

    expect(onSubmit).toHaveBeenCalledWith(
      { name: 'Ada', email: 'ada@example.com' },
      expect.anything(),
    )
  })

  it('submits the host form default values untouched by any rule', async () => {
    const onSubmit = vi.fn<SubmitHandler<MemberFormValues>>()
    const utils = renderWithProviders(
      <FullForm
        onSubmit={onSubmit}
        defaultValues={{ name: 'Ada', email: 'ada@example.com' }}
      />,
    )
    const user = userEvent.setup()
    await user.click(utils.getByRole('button', { name: 'Save member' }))
    expect(onSubmit).toHaveBeenCalledWith(
      { name: 'Ada', email: 'ada@example.com' },
      expect.anything(),
    )
  })

  it('switches the shown errors into the new language', async () => {
    const onSubmit = vi.fn<SubmitHandler<MemberFormValues>>()
    const utils = renderWithProviders(<FullForm onSubmit={onSubmit} />)
    const user = userEvent.setup()
    await user.click(utils.getByRole('button', { name: 'Save member' }))
    expect(utils.getAllByText(zhCN.form.required)).toHaveLength(2)

    await act(async () => {
      await switchLanguage(utils.i18n, 'en-US')
    })
    expect(utils.getAllByText(enUS.form.required)).toHaveLength(2)
    expect(utils.queryByText(zhCN.form.required)).not.toBeInTheDocument()
  })

  it('renders a novalidate form element with the host submit wired', () => {
    const onSubmit = vi.fn<SubmitHandler<MemberFormValues>>()
    const { container } = renderWithProviders(<FullForm onSubmit={onSubmit} />)
    const form = container.querySelector('form')
    expect(form).not.toBeNull()
    expect(form).toHaveAttribute('novalidate')
    expect(form).toHaveStyle({ 'max-width': '600px', gap: '16px' })
  })

  it('renders a bare field flow when no onSubmit is given', () => {
    function FlowOnly() {
      const form = useForm<MemberFormValues>({
        defaultValues: { name: '', email: '' },
      })
      return (
        <FormLayout form={form}>
          <FormField name="name" render={(s) => <TextFieldSlot state={s} label="Name" />} />
        </FormLayout>
      )
    }
    const { container, getByLabelText } = renderWithProviders(<FlowOnly />)
    expect(container.querySelector('form')).toBeNull()
    expect(getByLabelText('Name')).toBeInTheDocument()
  })

  it('renders the actions row right-aligned and apart from the fields', () => {
    const onSubmit = vi.fn<SubmitHandler<MemberFormValues>>()
    const { container, getByRole } = renderWithProviders(<FullForm onSubmit={onSubmit} />)
    const actionsRow = getByRole('button', { name: 'Save member' }).closest('div')
    expect(actionsRow).not.toBeNull()
    expect(container.querySelector('form')).toContainElement(actionsRow!)
  })

  describe('columns', () => {
    it('renders the default single-column flex flow when columns is omitted, byte-for-byte the pre-existing shape', () => {
      const onSubmit = vi.fn<SubmitHandler<MemberFormValues>>()
      const { container } = renderWithProviders(<FullForm onSubmit={onSubmit} />)
      const form = container.querySelector('form')
      expect(form).toHaveStyle({ display: 'flex', flexDirection: 'column' })
    })

    it('renders the default single-column flow the same way when columns={1} is passed explicitly', () => {
      const onSubmit = vi.fn<SubmitHandler<MemberFormValues>>()
      function OneColumnForm() {
        const form = useForm<MemberFormValues>({
          mode: 'onSubmit',
          defaultValues: { name: '', email: '' },
        })
        return (
          <FormLayout form={form} onSubmit={onSubmit} columns={1}>
            <FormField name="name" render={(s) => <TextFieldSlot state={s} label="Name" />} />
          </FormLayout>
        )
      }
      const { container } = renderWithProviders(<OneColumnForm />)
      const form = container.querySelector('form')
      expect(form).toHaveStyle({ display: 'flex', flexDirection: 'column' })
    })

    it('switches to a CSS Grid flow when columns={2} is passed', () => {
      const onSubmit = vi.fn<SubmitHandler<MemberFormValues>>()
      function TwoColumnForm() {
        const form = useForm<MemberFormValues>({
          mode: 'onSubmit',
          defaultValues: { name: '', email: '' },
        })
        return (
          <FormLayout
            form={form}
            onSubmit={onSubmit}
            columns={2}
            actions={<button type="submit">Save member</button>}
          >
            <FormField name="name" render={(s) => <TextFieldSlot state={s} label="Name" />} />
            <FormField name="email" render={(s) => <TextFieldSlot state={s} label="Email" />} />
          </FormLayout>
        )
      }
      const { container } = renderWithProviders(<TwoColumnForm />)
      const form = container.querySelector('form')
      expect(form).toHaveStyle({ display: 'grid' })
      const css = emittedStyleText()
      expect(css).toMatch(/grid-template-columns:1fr/)
      expect(css).toMatch(/@container \(min-width:600px\)/)
      expect(css).toMatch(/grid-template-columns:repeat\(2,\s*1fr\)/)
      expect(css).toMatch(/grid-column:1\/-1/)
    })

    it('declares a containment context, so the grid column query can resolve -- the parent declaration without which it never fires', () => {
      const onSubmit = vi.fn<SubmitHandler<MemberFormValues>>()
      function TwoColumnForm() {
        const form = useForm<MemberFormValues>({
          mode: 'onSubmit',
          defaultValues: { name: '', email: '' },
        })
        return (
          <FormLayout form={form} onSubmit={onSubmit} columns={2}>
            <FormField name="name" render={(s) => <TextFieldSlot state={s} label="Name" />} />
          </FormLayout>
        )
      }
      renderWithProviders(<TwoColumnForm />)
      const css = emittedStyleText()
      expect(css).toMatch(/container-type:inline-size/)
      expect(css).not.toContain('@media (min-width:600px)')
    })

    it('still submits the collected values with columns={2}, unchanged behavior', async () => {
      const onSubmit = vi.fn<SubmitHandler<MemberFormValues>>()
      function TwoColumnForm() {
        const form = useForm<MemberFormValues>({
          mode: 'onSubmit',
          defaultValues: { name: '', email: '' },
        })
        return (
          <FormLayout
            form={form}
            onSubmit={onSubmit}
            columns={2}
            actions={<button type="submit">Save member</button>}
          >
            <FormField
              name="name"
              required
              render={(s) => <TextFieldSlot state={s} label="Name" />}
            />
            <FormField
              name="email"
              required
              render={(s) => <TextFieldSlot state={s} label="Email" />}
            />
          </FormLayout>
        )
      }
      const utils = renderWithProviders(<TwoColumnForm />)
      const user = userEvent.setup()
      await user.type(utils.getByLabelText('Name'), 'Ada')
      await user.type(utils.getByLabelText('Email'), 'ada@example.com')
      await user.click(utils.getByRole('button', { name: 'Save member' }))
      expect(onSubmit).toHaveBeenCalledWith(
        { name: 'Ada', email: 'ada@example.com' },
        expect.anything(),
      )
    })
  })

  it('passes axe over a labeled form', async () => {
    const onSubmit = vi.fn<SubmitHandler<MemberFormValues>>()
    const utils = renderWithProviders(
      <div>
        <h1>Members</h1>
        <FullForm onSubmit={onSubmit} />
      </div>,
    )
    const user = userEvent.setup()
    await user.click(utils.getByRole('button', { name: 'Save member' }))
    await expectNoAxeViolations()
  })

  it('forwards sx, className and ref to the form root box', () => {
    const ref = createRef<HTMLDivElement>()
    function RootPropsForm() {
      const form = useForm<MemberFormValues>({
        defaultValues: { name: '', email: '' },
      })
      return (
        <FormLayout
          form={form}
          ref={ref}
          className="host-layout"
          sx={{ mt: 4 }}
        >
          <span>field</span>
        </FormLayout>
      )
    }
    const { container } = renderWithProviders(<RootPropsForm />)
    const root = container.firstChild as HTMLElement
    expect(root).toHaveClass('host-layout')
    expect(root).toBe(ref.current)
    expect(root).toHaveStyle({ marginTop: '32px' })
    expect(root).toHaveTextContent('field')
  })
})
