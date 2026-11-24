import { displayFile } from './no-double-feedback-region.js'

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

/** The registered exemptions: file, declaring component, and reason. */
export const NATIVE_DISABLED_EXEMPTIONS = Object.freeze([
  Object.freeze({
    file: 'packages/tenancy-ui/src/TenantSwitcher.tsx',
    component: 'TenantSwitcher',
    reason:
      'The trigger becomes unavailable only while the current tenant is unknown, a state in which it has never been focused or activated.',
  }),
])

const COMPONENT_WRAPPERS = new Set(['forwardRef', 'memo'])

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

function isExempt(file, component) {
  return NATIVE_DISABLED_EXEMPTIONS.some(
    (entry) => entry.file === file && entry.component === component,
  )
}

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
