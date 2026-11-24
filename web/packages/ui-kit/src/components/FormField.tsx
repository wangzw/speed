/**
 * The react-hook-form field adapter: a bound Controller plus the resolved
 * error text, rendered entirely by the host's `render` control.
 */

import { forwardRef, useMemo } from 'react'
import type { ReactElement, Ref } from 'react'
import Box from '@mui/material/Box'
import { Controller, useFormContext } from 'react-hook-form'
import type {
  Control,
  ControllerRenderProps,
  FieldPath,
  FieldPathValue,
  FieldValues,
  RegisterOptions,
} from 'react-hook-form'
import { UI_KIT_NAMESPACE } from '../resources.js'
import { useUiKitTranslation } from '../internal/translation.js'
import type { RootStyleProps } from '../internal/root-style-props.js'
import { resolveValidationError } from '../internal/validation-error.js'

/** The message key the `required` convenience switch injects. */
export const REQUIRED_ERROR_KEY = 'form.required'

export interface FormFieldRenderState<
  TFieldValues extends FieldValues = FieldValues,
  TName extends FieldPath<TFieldValues> = FieldPath<TFieldValues>,
> {
  /** The bound field handlers for the host control ({...field} onto an input). */
  readonly field: ControllerRenderProps<TFieldValues, TName>
  /** Whether the field currently fails validation. */
  readonly invalid: boolean
  /** Whether the field has been blurred at least once. */
  readonly isTouched: boolean
  /** The `required` convenience switch (for host asterisks). */
  readonly required: boolean
  /** The raw error message (namespace key or verbatim text), if any. */
  readonly errorMessage: string | null
  /** The resolved display text: namespace-key errors translated, others verbatim. */
  readonly errorText: string | null
}

export interface FormFieldProps<
  TFieldValues extends FieldValues = FieldValues,
  TName extends FieldPath<TFieldValues> = FieldPath<TFieldValues>,
> extends RootStyleProps {
  /** The field path inside the form values. */
  readonly name: TName
  /** The form control; optional inside `FormLayout`, whose provider supplies it. */
  readonly control?: Control<TFieldValues>
  /**
   * Validation rules. Error messages follow the validation-error
   * contract: a ui-kit-namespace key ('form.required', 'form.invalid')
   * or verbatim text.
   */
  readonly rules?: Omit<
    RegisterOptions<TFieldValues, TName>,
    'valueAsNumber' | 'valueAsDate' | 'setValueAs' | 'disabled'
  >
  /** Default value for the field when the form carries none. */
  readonly defaultValue?: FieldPathValue<TFieldValues, TName>
  /** Injects the 'form.required' rule unless `rules` already define one. */
  readonly required?: boolean
  /** Renders the host control from the bound state. */
  readonly render: (
    state: FormFieldRenderState<TFieldValues, TName>,
  ) => ReactElement
}

/**
 * `sx`, `className` and the ref apply to a box around the host control, and
 * only when one of them is passed.
 */
function FormFieldInner<
  TFieldValues extends FieldValues,
  TName extends FieldPath<TFieldValues>,
>(
  {
    name,
    control: controlProp,
    rules,
    defaultValue,
    required = false,
    render,
    sx,
    className,
  }: FormFieldProps<TFieldValues, TName>,
  ref: Ref<HTMLDivElement>,
): ReactElement {
  const { t, i18n } = useUiKitTranslation()
  const contextControl = useFormContext<TFieldValues>()?.control
  const control = controlProp ?? contextControl
  if (control === undefined) {
    throw new Error(
      'FormField: no control available. Pass the control prop, or render the ' +
        'field inside FormLayout (which provides the FormProvider context).',
    )
  }

  // An explicit host rule always wins.
  const effectiveRules = useMemo(() => {
    if (!required || rules?.required !== undefined) {
      return rules
    }
    return { ...rules, required: REQUIRED_ERROR_KEY }
  }, [required, rules])

  const exists = (key: string): boolean =>
    i18n.exists(key, { ns: UI_KIT_NAMESPACE })
  const needsRoot = sx !== undefined || className !== undefined || ref != null

  return (
    <Controller<TFieldValues, TName>
      name={name}
      control={control}
      rules={effectiveRules}
      defaultValue={defaultValue}
      render={({ field, fieldState }) => {
        const message = fieldState.error?.message ?? null
        const controlNode = render({
          field,
          invalid: fieldState.invalid,
          isTouched: fieldState.isTouched,
          required,
          errorMessage: message,
          errorText: resolveValidationError(message, { exists, t }),
        })
        if (!needsRoot) {
          return controlNode
        }
        return (
          <Box ref={ref} className={className} sx={sx}>
            {controlNode}
          </Box>
        )
      }}
    />
  )
}

/**
 * The field adapter. `forwardRef` erases the component's type parameters, so
 * the cast below restores the generic signature.
 */
export const FormField = forwardRef(FormFieldInner) as <
  TFieldValues extends FieldValues = FieldValues,
  TName extends FieldPath<TFieldValues> = FieldPath<TFieldValues>,
>(
  props: FormFieldProps<TFieldValues, TName> & {
    readonly ref?: Ref<HTMLDivElement>
  },
) => ReactElement
