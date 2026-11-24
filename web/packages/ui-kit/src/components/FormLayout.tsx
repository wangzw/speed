/**
 * The form skeleton: FormProvider, optional `<form>` submission, the field
 * flow and the actions row. `columns={2}` is a container-query grid; the
 * default stays the single-column flex flow.
 */

import { forwardRef } from 'react'
import type { ReactElement, ReactNode, Ref } from 'react'
import Box from '@mui/material/Box'
import { FormProvider } from 'react-hook-form'
import type { FieldValues, SubmitHandler, UseFormReturn } from 'react-hook-form'
import { layerSx } from '../internal/root-style-props.js'
import type { RootStyleProps } from '../internal/root-style-props.js'
import { ActionRow } from './ActionRow.js'

/** The field-flow column count: 2 is a container-query grid, 1 the flex flow. */
export type FormLayoutColumns = 1 | 2

/**
 * The form family's default content width in px, shared so every form on a
 * page reads at one width.
 */
export const FORM_MAX_WIDTH = 600

export interface FormLayoutProps<TFieldValues extends FieldValues = FieldValues>
  extends RootStyleProps {
  /** The host's useForm instance (control, handleSubmit, reset all live with the host). */
  readonly form: UseFormReturn<TFieldValues>
  /** The field flow: FormField instances or any custom controls. */
  readonly children: ReactNode
  /** Submit handler; given, the layout renders a `<form>` that validates through RHF first. */
  readonly onSubmit?: SubmitHandler<TFieldValues>
  /** The bottom action row (submit button, cancel link); right-aligned. */
  readonly actions?: ReactNode
  /** Vertical gap between rows, in theme spacing units. Defaults to 2. */
  readonly spacing?: number
  /** Content width in px. Defaults to 600; false widens to the parent. */
  readonly maxWidth?: number | false
  /** Field-flow column count; defaults to 1, the unchanged single-column flow. */
  readonly columns?: FormLayoutColumns
}

/** `sx`, `className` and the ref reach the outer containment box. */
function FormLayoutInner<TFieldValues extends FieldValues>(
  {
    form,
    children,
    onSubmit,
    actions,
    spacing = 2,
    maxWidth = FORM_MAX_WIDTH,
    columns = 1,
    sx,
    className,
  }: FormLayoutProps<TFieldValues>,
  ref: Ref<HTMLDivElement>,
): ReactElement {
  const handleSubmit = onSubmit === undefined ? undefined : form.handleSubmit(onSubmit)
  const isGrid = columns === 2
  const body = (
    <>
      {children}
      {actions !== undefined && (
        <ActionRow
          // Span every track so the row reads as one full-width row in grid mode.
          sx={isGrid ? { gridColumn: '1 / -1' } : undefined}
        >
          {actions}
        </ActionRow>
      )}
    </>
  )
  const widthSx = {
    width: '100%',
    maxWidth: maxWidth === false ? 'none' : maxWidth,
  } as const
  const flowSx = isGrid
    ? ({
        display: 'grid',
        // Container-keyed tracks; the query resolves against the box below.
        gridTemplateColumns: { '@': '1fr', '@sm': 'repeat(2, 1fr)' },
        columnGap: spacing,
        rowGap: spacing,
        ...widthSx,
      } as const)
    : ({
        display: 'flex',
        flexDirection: 'column',
        gap: spacing,
        ...widthSx,
      } as const)
  // The form reflows by its own width, not the viewport.
  const containerSx = { containerType: 'inline-size' } as const
  const rootSx = layerSx(containerSx, sx)
  if (handleSubmit !== undefined) {
    return (
      <FormProvider {...form}>
        <Box ref={ref} className={className} sx={rootSx}>
          <Box component="form" noValidate onSubmit={handleSubmit} sx={flowSx}>
            {body}
          </Box>
        </Box>
      </FormProvider>
    )
  }
  return (
    <FormProvider {...form}>
      <Box ref={ref} className={className} sx={rootSx}>
        <Box sx={flowSx}>{body}</Box>
      </Box>
    </FormProvider>
  )
}

/**
 * The form skeleton. `forwardRef` erases the component's type parameters, so
 * the cast below restores the generic signature.
 */
export const FormLayout = forwardRef(FormLayoutInner) as <
  TFieldValues extends FieldValues = FieldValues,
>(
  props: FormLayoutProps<TFieldValues> & {
    readonly ref?: Ref<HTMLDivElement>
  },
) => ReactElement
