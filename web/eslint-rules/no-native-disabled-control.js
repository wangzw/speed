/**
 * no-native-disabled-control: button-shaped MUI controls keep focus.
 *
 * A focused element that becomes natively disabled is made unfocusable by
 * the browser, whose focus fixup moves focus to the document body. The
 * keyboard or screen-reader user who activated the control therefore loses
 * their place; after a save they must tab from the top of the page again.
 * MUI's `loading` prop compiles to a native `disabled` attribute on the
 * button, so it loses focus the same way. The replacement keeps the control
 * natively focusable, reports unavailability with `aria-disabled="true"`
 * and refuses activation through a guard: `ActionButton`'s `busy` and
 * `unavailable` props, or, for a control that cannot be an `ActionButton`,
 * `aria-disabled` plus `useActivationGuard`. This rule reports the two
 * attributes that turn a covered control natively disabled when their value
 * is not a literal.
 *
 * Judged elements are the button-shaped MUI controls, resolved through the
 * file's own import bindings (a renamed import counts, `import { default as
 * B }` counts as a default import) and through scope analysis, so a local
 * binding that shadows the import is not judged:
 *
 *   - the default export of `@mui/material/Button`,
 *     `@mui/material/IconButton`, `@mui/material/ButtonBase`,
 *     `@mui/material/Fab`, `@mui/material/ToggleButton`,
 *     `@mui/material/Switch`, `@mui/material/Checkbox`,
 *     `@mui/material/FormControlLabel`, `@mui/material/ListItemButton`,
 *     `@mui/material/Radio` and `@mui/lab/LoadingButton`;
 *   - the same names imported from the `@mui/material` barrel.
 *
 * `ListItemButton` is an action row that keeps focus after activation, and
 * `Radio` is an instant-save choice; unlike a `Select`, an `Autocomplete` or
 * a `TextField`, neither is a selection- or input-shaped context control, so
 * both hold focus when a write makes them unavailable.
 *
 * Only a `JSXIdentifier` tag is judged: a member expression, a lowercase
 * native tag (`<button>`, `<input>`) and a same-named component from another
 * source (`@speed/ui-kit`'s `ActionButton`, a file-local `Button`) are not
 * covered.
 *
 * Not judged, and why:
 *
 *   - `ActionButton` accepts neither `disabled` nor `loading`; the type
 *     checker reports either, so lint does not need to.
 *   - `Select`, `Autocomplete` and `TextField` are selection- or input-shaped
 *     controls. The coding standard keeps them natively disabled while a
 *     write is in flight (or read-only when they start the write), and each
 *     case is code review's judgement.
 *   - `MenuItem` closes its menu on activation and MUI returns focus to the
 *     menu button, so it cannot hold focus when it becomes unavailable.
 *   - Attributes passed through a spread (`{...props}`) or `createElement`
 *     are invisible to the rule; they remain code review's residue.
 *
 * An exception exists only when the control cannot hold focus at the moment
 * it becomes unavailable; that is the whole criterion. Register the file
 * and the declaring component in `NATIVE_DISABLED_EXEMPTIONS` with one
 * English sentence stating why. There is no inline escape: the rule reads no
 * comments, and the workspace scan in
 * no-native-disabled-control.test.mjs rejects an `eslint-disable` comment
 * that names this rule and runs with `noInlineConfig`, so no inline
 * directive takes effect. An exemption suppresses
 * only `nonLiteralDisabled`; `loading` always has `ActionButton`'s `busy` as
 * its replacement.
 *
 * Scope is set by the eslint configuration (each package's runtime `src`
 * and the demo host's `src`; tests and test utilities are excluded there);
 * the rule does not judge paths itself.
 */

import { displayFile } from './no-double-feedback-region.js'

/** The covered elements: module specifier to the export names judged. */
export const NATIVE_DISABLED_ELEMENTS = Object.freeze({
  '@mui/material/Button': Object.freeze(['default']),
  '@mui/material/IconButton': Object.freeze(['default']),
  '@mui/material/ButtonBase': Object.freeze(['default']),
  '@mui/material/Fab': Object.freeze(['default']),
  '@mui/material/ToggleButton': Object.freeze(['default']),
  '@mui/material/Switch': Object.freeze(['default']),
  '@mui/material/Checkbox': Object.freeze(['default']),
  '@mui/material/FormControlLabel': Object.freeze(['default']),
  '@mui/material/ListItemButton': Object.freeze(['default']),
  '@mui/material/Radio': Object.freeze(['default']),
  '@mui/lab/LoadingButton': Object.freeze(['default']),
  '@mui/material': Object.freeze([
    'Button',
    'IconButton',
    'ButtonBase',
    'Fab',
    'ToggleButton',
    'Switch',
    'Checkbox',
    'FormControlLabel',
    'ListItemButton',
    'Radio',
  ]),
})

/**
 * Components allowed to render a native `disabled` on a covered control,
 * each with the reason it cannot hold focus at the moment it becomes
 * unavailable.
 * @type {ReadonlyArray<{ readonly file: string, readonly component: string, readonly reason: string }>}
 */
export const NATIVE_DISABLED_EXEMPTIONS = Object.freeze([
  Object.freeze({
    file: 'packages/tenancy-ui/src/TenantSwitcher.tsx',
    component: 'TenantSwitcher',
    reason:
      'The trigger becomes unavailable only while the current tenant is unknown, a state in which it has never been focused or activated.',
  }),
])

/** The wrapper calls whose inner function takes the outer `const` name. */
const COMPONENT_WRAPPERS = new Set(['forwardRef', 'memo'])

/** The local names this file imports from the covered modules with the
 * export name the map judges. A type-only import binds no value and is
 * skipped; a namespace import is not judged. */
function coveredBindingsOf(program) {
  const bindings = new Set()
  for (const statement of program.body) {
    if (statement.type !== 'ImportDeclaration') {
      continue
    }
    if (statement.importKind === 'type') {
      continue
    }
    const moduleName = statement.source.value
    if (!Object.hasOwn(NATIVE_DISABLED_ELEMENTS, moduleName)) {
      continue
    }
    const exportNames = NATIVE_DISABLED_ELEMENTS[moduleName]
    for (const specifier of statement.specifiers) {
      if (specifier.type === 'ImportDefaultSpecifier') {
        if (exportNames.includes('default')) {
          bindings.add(specifier.local.name)
        }
        continue
      }
      if (specifier.type !== 'ImportSpecifier' || specifier.importKind === 'type') {
        continue
      }
      const imported = specifier.imported
      const importedName =
        imported.type === 'Identifier' ? imported.name : imported.value
      if (exportNames.includes(importedName)) {
        bindings.add(specifier.local.name)
      }
    }
  }
  return bindings
}

/** Whether the tag name resolves, through scope analysis, to the import
 * binding rather than to a local binding that shadows it. */
function resolvesToImport(sourceCode, nameNode) {
  const scope = sourceCode.getScope(nameNode)
  for (let current = scope; current !== null && current !== undefined; current = current.upper) {
    const variable = current.set.get(nameNode.name)
    if (variable === undefined) {
      continue
    }
    return variable.defs.some((definition) => definition.type === 'ImportBinding')
  }
  return false
}

/** Whether a wrapper call is `forwardRef(...)`, `memo(...)` or the
 * `React.`-prefixed form of either. */
function isComponentWrapper(callee) {
  if (callee.type === 'Identifier') {
    return COMPONENT_WRAPPERS.has(callee.name)
  }
  return (
    callee.type === 'MemberExpression' &&
    !callee.computed &&
    callee.object.type === 'Identifier' &&
    callee.object.name === 'React' &&
    callee.property.type === 'Identifier' &&
    COMPONENT_WRAPPERS.has(callee.property.name)
  )
}

/** The name a function node declares, if any. A function passed to
 * `forwardRef(...)` or `memo(...)` takes the name of the `const` that holds
 * the wrapper's result; a named function expression uses its own name; an
 * anonymous function or arrow takes the name of the `const` it is assigned
 * to. An object property or method declares no component. */
function declaredFunctionName(node, ancestors, index) {
  let wrapped = node
  let wrapperIndex = index - 1
  while (wrapperIndex >= 0) {
    const candidate = ancestors[wrapperIndex]
    if (
      candidate.type !== 'CallExpression' ||
      !candidate.arguments.includes(wrapped) ||
      !isComponentWrapper(candidate.callee)
    ) {
      break
    }
    wrapped = candidate
    wrapperIndex -= 1
  }
  if (wrapped !== node) {
    const owner = wrapperIndex >= 0 ? ancestors[wrapperIndex] : undefined
    if (
      owner !== undefined &&
      owner.type === 'VariableDeclarator' &&
      owner.id !== null &&
      owner.id.type === 'Identifier'
    ) {
      return owner.id.name
    }
    return undefined
  }

  if (
    (node.type === 'FunctionDeclaration' || node.type === 'FunctionExpression') &&
    node.id !== null
  ) {
    return node.id.name
  }
  const parent = index > 0 ? ancestors[index - 1] : undefined
  if (
    parent !== undefined &&
    parent.type === 'VariableDeclarator' &&
    parent.id !== null &&
    parent.id.type === 'Identifier'
  ) {
    return parent.id.name
  }
  return undefined
}

/** The declaration name of the component a JSX element renders in: the
 * nearest enclosing function whose declared name starts with a capital
 * letter. A lowercase helper (a `renderRow`, a column-definition callback,
 * an object method) is not a declaration component, so the search continues
 * outward: a button rendered from a helper belongs to the component that
 * declares the helper. Anonymous callbacks are skipped for the same reason.
 * When no capital-named ancestor exists the name is `<component>`, which no
 * exemption can match, so the element is reported as usual: an unmatched
 * name fails closed. Three spellings that do name a component still reach
 * it: `memo(function X() {})` not assigned to a `const` (the wrapper result
 * has no owning declarator), `const X = function inner() {}` (the lowercase
 * inner name is what the search reads, and it then continues outward), and
 * a class component's `render` (a method carries no declarator name). The
 * ancestors are the nodes enclosing the JSX element, outermost first. */
function componentNameOf(ancestors) {
  for (let index = ancestors.length - 1; index >= 0; index -= 1) {
    const node = ancestors[index]
    if (
      node.type !== 'FunctionDeclaration' &&
      node.type !== 'FunctionExpression' &&
      node.type !== 'ArrowFunctionExpression'
    ) {
      continue
    }
    const name = declaredFunctionName(node, ancestors, index)
    if (name !== undefined && /^[A-Z]/.test(name)) {
      return name
    }
  }
  return '<component>'
}

/** Whether a `loading` attribute's value is the literal `false`. Bare
 * `loading`, `loading={true}`, an identifier and a string all report. */
function loadingIsLiteralFalse(attribute) {
  const value = attribute.value
  return (
    value !== null &&
    value !== undefined &&
    value.type === 'JSXExpressionContainer' &&
    value.expression.type === 'Literal' &&
    value.expression.value === false
  )
}

/** Whether a `disabled` attribute's value is an expression rather than a
 * literal. Bare `disabled` and a JSX string value are literals and pass;
 * so do `disabled={true}` and `disabled={false}`. */
function disabledIsNonLiteral(attribute) {
  const value = attribute.value
  if (value === null || value === undefined) {
    return false
  }
  if (value.type === 'Literal') {
    return false
  }
  if (value.type !== 'JSXExpressionContainer') {
    return false
  }
  return value.expression.type !== 'Literal'
}

/** Whether this component is registered as allowed to render a native
 * `disabled` on a covered control. */
function isExempt(file, component) {
  return NATIVE_DISABLED_EXEMPTIONS.some(
    (entry) => entry.file === file && entry.component === component,
  )
}

/** The JSX attributes named `loading` and `disabled`, if present. */
function attributeNamed(attributes, name) {
  return attributes.find(
    (attribute) =>
      attribute.type === 'JSXAttribute' &&
      attribute.name.type === 'JSXIdentifier' &&
      attribute.name.name === name,
  )
}

export const noNativeDisabledControlRule = {
  meta: {
    type: 'problem',
    docs: {
      description:
        'Disallow a native `disabled` or MUI `loading` on button-shaped MUI controls; keep them focusable and represent unavailability with `aria-disabled`.',
    },
    messages: {
      loadingProp:
        "{{element}} in component {{component}} uses MUI's loading prop. MUI turns loading into a native disabled attribute, so a button that holds keyboard focus drops it to the document body. Use ActionButton's busy prop instead.",
      nonLiteralDisabled:
        '{{element}} in component {{component}} is disabled by a non-literal expression. A control that holds keyboard focus and becomes natively disabled drops focus to the document body. Use ActionButton with busy or unavailable, or aria-disabled with useActivationGuard, or register this component in NATIVE_DISABLED_EXEMPTIONS with a reason.',
    },
    schema: [],
  },
  create(context) {
    const sourceCode = context.sourceCode
    const file = displayFile(context.filename)
    let coveredBindings = new Set()

    return {
      Program(program) {
        coveredBindings = coveredBindingsOf(program)
      },
      JSXOpeningElement(node) {
        if (
          node.name.type !== 'JSXIdentifier' ||
          !coveredBindings.has(node.name.name) ||
          !resolvesToImport(sourceCode, node.name)
        ) {
          return
        }
        const loadingAttribute = attributeNamed(node.attributes, 'loading')
        const disabledAttribute = attributeNamed(node.attributes, 'disabled')
        if (loadingAttribute === undefined && disabledAttribute === undefined) {
          return
        }
        const component = componentNameOf(sourceCode.getAncestors(node))
        const data = { element: node.name.name, component }
        if (
          loadingAttribute !== undefined &&
          !loadingIsLiteralFalse(loadingAttribute)
        ) {
          context.report({
            node: loadingAttribute,
            messageId: 'loadingProp',
            data,
          })
        }
        if (
          disabledAttribute !== undefined &&
          disabledIsNonLiteral(disabledAttribute) &&
          !isExempt(file, component)
        ) {
          context.report({
            node: disabledAttribute,
            messageId: 'nonLiteralDisabled',
            data,
          })
        }
      },
    }
  },
}
