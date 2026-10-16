/**
 * FormLayout: the form skeleton of the form family.
 *
 * Owns the parts every react-hook-form screen repeats, controlled by the
 * host's own useForm instance:
 *
 *  - the FormProvider context, so the FormField children need no control
 *    prop of their own;
 *  - the <form> element with RHF's handleSubmit wired (native browser
 *    validation is off: required markers come from RHF rules, which speak
 *    the ui-kit validation-error contract);
 *  - the vertical field flow with uniform spacing;
 *  - the right-aligned actions row (submit button etc. -- submit busy
 *    state stays with the host, who owns the request).
 *
 * Pass no onSubmit to render a bare field flow (a form section inside a
 * larger form, a filters panel). The form element carries no styling
 * beyond the layout; hosts keep their own width constraints.
 *
 * Columns (opt-in, additive): `columns` defaults to 1 -- the
 * unconditional single-column flex flow, preserved for every consumer
 * that does not pass it. Setting
 * `columns={2}` switches the flow to a CSS Grid with two responsive
 * column tracks (`sm` -- 600px -- and up; a single track below it) so
 * each direct child (typically one FormField per cell) lays out two per
 * row once the form has room and collapses to one column when it does
 * not.
 *
 * The trigger is the form's container, not the viewport: FormLayout
 * wraps its flow in a box that declares `container-type: inline-size`,
 * and the grid's two-track rule is a container query against that box.
 * A form filling the content region and the same form in a narrow side
 * panel therefore collapse at their own widths (the layout design's
 * viewport-vs-container split: the shell follows the viewport, a
 * component follows its container). `sm` -- 600px -- is the chosen
 * container width over `md`: a two-field row (e.g. name/email) still
 * has legible field widths as soon as the form clears the
 * phone/tablet-portrait boundary, and a form whose fields genuinely
 * need the full row can still pass `columns={1}` regardless of
 * container. The actions row spans every column track in grid mode so
 * it always reads as one full-width row, matching its single-column
 * appearance.
 */

import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import { FormProvider } from 'react-hook-form'
import type { FieldValues, SubmitHandler, UseFormReturn } from 'react-hook-form'

/** The field-flow column count. 2 is a responsive CSS Grid (1 column
 * below the container's `sm`, 2 from it up); 1 is the single-column
 * flex flow. */
export type FormLayoutColumns = 1 | 2

export interface FormLayoutProps<TFieldValues extends FieldValues = FieldValues> {
  /** The host's useForm instance (control, handleSubmit, reset all live with the host). */
  readonly form: UseFormReturn<TFieldValues>
  /** The field flow: FormField instances or any custom controls. */
  readonly children: ReactNode
  /**
   * Submit handler; when given, the layout renders a <form> that
   * validates through RHF (noValidate) before calling it with the
   * values. Async errors inside the handler are the host's to catch.
   */
  readonly onSubmit?: SubmitHandler<TFieldValues>
  /** The bottom action row (submit button, cancel link); right-aligned. */
  readonly actions?: ReactNode
  /** Vertical gap between rows, in theme spacing units. Defaults to 2. */
  readonly spacing?: number
  /** Content width in px. Defaults to 600; false widens to the parent. */
  readonly maxWidth?: number | false
  /**
   * Field-flow column count. Defaults to 1 (the unconditional
   * single-column flow, for every consumer that omits this prop). See
   * the module doc comment for the container width this switches on and
   * the reasoning behind it.
   */
  readonly columns?: FormLayoutColumns
}

/**
 * The form skeleton: FormProvider context, optional <form> submission,
 * field flow and actions row.
 */
export function FormLayout<TFieldValues extends FieldValues>({
  form,
  children,
  onSubmit,
  actions,
  spacing = 2,
  maxWidth = 600,
  columns = 1,
}: FormLayoutProps<TFieldValues>) {
  const handleSubmit = onSubmit === undefined ? undefined : form.handleSubmit(onSubmit)
  const isGrid = columns === 2
  const body = (
    <>
      {children}
      {actions !== undefined && (
        <Box
          sx={{
            display: 'flex',
            justifyContent: 'flex-end',
            alignItems: 'center',
            gap: 1,
            // Span every grid track so the actions row always reads as
            // one full-width row in the two-column layout, matching its
            // single-column appearance; a no-op in the default flex flow.
            ...(isGrid ? { gridColumn: '1 / -1' } : {}),
          }}
        >
          {actions}
        </Box>
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
        // Container-keyed tracks: one column at the container's narrow
        // end (the `@` shorthand's 0px baseline), two equal tracks from
        // the container's `sm` up. The query resolves against the
        // `containerSx` box below, so the form reflows by its own width.
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
  // The containment context the grid's container query resolves against.
  // It is a plain block filling its parent, so the container's width is
  // the width the host gives the form -- the form collapses in a narrow
  // panel and stays two-column in a wide one, independent of the
  // viewport.
  const containerSx = { containerType: 'inline-size' } as const
  if (handleSubmit !== undefined) {
    return (
      <FormProvider {...form}>
        <Box sx={containerSx}>
          <Box component="form" noValidate onSubmit={handleSubmit} sx={flowSx}>
            {body}
          </Box>
        </Box>
      </FormProvider>
    )
  }
  return (
    <FormProvider {...form}>
      <Box sx={containerSx}>
        <Box sx={flowSx}>{body}</Box>
      </Box>
    </FormProvider>
  )
}
