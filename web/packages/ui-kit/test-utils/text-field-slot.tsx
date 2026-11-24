/**
 * The canonical host control for the form-family tests: a TextField wired
 * to a `FormField` render state as the docs show. The state is a minimal
 * structural slice, so it is assignable from any form's render state.
 */

import TextField from '@mui/material/TextField'

export interface TextFieldSlotState {
  readonly field: {
    readonly name: string
    readonly value: unknown
    readonly onChange: (...event: unknown[]) => void
    readonly onBlur: () => void
    readonly ref: (instance: unknown) => void
  }
  readonly invalid: boolean
  readonly errorText: string | null
}

export function TextFieldSlot({
  state,
  label,
  type,
}: {
  readonly state: TextFieldSlotState
  readonly label: string
  readonly type?: string
}) {
  return (
    <TextField
      {...state.field}
      label={label}
      type={type}
      error={state.invalid}
      helperText={state.errorText ?? undefined}
      fullWidth
    />
  )
}
