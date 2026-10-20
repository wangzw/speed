/**
 * FormField: the react-hook-form field adapter of the form family.
 *
 * One field's worth of RHF plumbing, collapsed: a Controller bound to
 * `name` under `control` (or the FormProvider context FormLayout
 * installs), with the validation-error text contract applied at render
 * time -- an error message that is a ui-kit-namespace key renders as its
 * translation in the current language, anything else renders verbatim
 * (see src/internal/validation-error.ts).
 *
 * The component deliberately renders no chrome of its own: the host's
 * control renders through the `render` prop, which receives the bound
 * field state plus the resolved error text, and the host wires them into
 * whatever control fits -- MUI TextField is the common case:
 *
 *   <FormField
 *     name="email"
 *     control={control}
 *     label=...           // labels are host content, not namespace text
 *     required
 *     render={({ field, invalid, errorText }) => (
 *       <TextField
 *         {...field}
 *         label="Email"
 *         error={invalid}
 *         helperText={errorText ?? undefined}
 *       />
 *     )}
 *   />
 *
 * MUI's own form controls (TextField and friends) already render label +
 * helper text with the aria wiring, so painting chrome here would
 * duplicate theirs; the "uniform error display" of the form family is
 * this state contract plus FormLayout's spacing skeleton, not a second
 * label/error renderer.
 *
 * `required` is a convenience switch: it injects the 'form.required'
 * namespace message as the required rule (unless the rules already carry
 * one) and surfaces itself on the render state for host asterisks. Rules
 * errors surface after the host's submit/trigger, per the useForm mode.
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
  /**
   * The form control. Optional when the field renders inside FormLayout
   * (its FormProvider supplies the context); otherwise required.
   */
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
  /**
   * Convenience switch: injects the 'form.required' rule (unless rules
   * already define one) and marks the render state. Mark the host
   * label's asterisk from the state.
   */
  readonly required?: boolean
  /** Renders the host control from the bound state (see header example). */
  readonly render: (
    state: FormFieldRenderState<TFieldValues, TName>,
  ) => ReactElement
}

/**
 * The RHF field adapter: Controller plumbing plus the resolved-error
 * state contract for the host control. The component renders no chrome
 * of its own, so `sx`, `className` and the ref apply to a box placed
 * around the host control only when the host passes one of them; with
 * none passed, the rendered tree is exactly the host control.
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

  // The convenience switch only ever fills an absent required rule; an
  // explicit host rule (with its own message) always wins.
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
        // Only a host that asked for a root prop gets the box; the
        // ordinary rendering stays the bare host control, so a field's
        // place in FormLayout's flow never gains an element it did not
        // have.
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
 * The RHF field adapter (see `FormFieldInner`). The generic signature is
 * preserved across `forwardRef` by the cast below: React 18 forwards a
 * ref only through `forwardRef`, which erases the component's own type
 * parameters.
 */
export const FormField = forwardRef(FormFieldInner) as <
  TFieldValues extends FieldValues = FieldValues,
  TName extends FieldPath<TFieldValues> = FieldPath<TFieldValues>,
>(
  props: FormFieldProps<TFieldValues, TName> & {
    readonly ref?: Ref<HTMLDivElement>
  },
) => ReactElement
