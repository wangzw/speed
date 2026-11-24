const TEXT_BEARING_ATTRIBUTES = new Set([
  'aria-label',
  'aria-placeholder',
  'aria-roledescription',
  'placeholder',
  'title',
  'alt',
])

function isPlainStringLiteral(node) {
  if (node.type === 'Literal' && typeof node.value === 'string') {
    return node
  }
  if (
    node.type === 'TemplateLiteral' &&
    node.expressions.length === 0 &&
    node.quasis.length === 1
  ) {
    return node
  }
  return undefined
}

function literalText(node) {
  const literal = isPlainStringLiteral(node)
  if (literal === undefined) {
    return undefined
  }
  const text =
    literal.type === 'Literal'
      ? literal.value
      : literal.quasis[0].value.cooked
  return text !== undefined && text.trim() !== '' ? text : undefined
}

function plainTextLiterals(node) {
  const found = []
  function walk(current) {
    if (current.type === 'ConditionalExpression') {
      walk(current.consequent)
      walk(current.alternate)
      return
    }
    if (current.type === 'LogicalExpression') {
      walk(current.left)
      walk(current.right)
      return
    }
    const text = literalText(current)
    if (text !== undefined) {
      found.push({ node: current, text })
    }
  }
  walk(node)
  return found
}

export const noLiteralTextRule = {
  meta: {
    type: 'suggestion',
    docs: {
      description:
        'Disallow user-facing text written inline in JSX; every visible string must come from the i18n namespace.',
    },
    messages: {
      literalText:
        'User-facing text must come from the i18n namespace, not be written inline. Render "{{text}}" through a t() call (see the i18n section of the frontend coding standards).',
      literalAttribute:
        'The {{name}} value is user-facing text; pass it through a t() call instead of the literal "{{text}}" (see the i18n section of the frontend coding standards).',
    },
    schema: [],
  },
  create(context) {
    return {
      JSXText(node) {
        const text = node.value.trim()
        if (text !== '') {
          context.report({
            node,
            messageId: 'literalText',
            data: { text: text.slice(0, 40) },
          })
        }
      },
      JSXExpressionContainer(node) {
        // Attribute values have this same node shape; the JSXAttribute
        // visitor reports them with a more specific message.
        if (node.parent !== null && node.parent.type === 'JSXAttribute') {
          return
        }
        for (const { node: literal, text } of plainTextLiterals(
          node.expression,
        )) {
          context.report({
            node: literal,
            messageId: 'literalText',
            data: { text: text.slice(0, 40) },
          })
        }
      },
      JSXAttribute(node) {
        const isChildren =
          node.name.type === 'JSXIdentifier' && node.name.name === 'children'
        if (
          !isChildren &&
          (node.name.type !== 'JSXIdentifier' ||
            !TEXT_BEARING_ATTRIBUTES.has(node.name.name))
        ) {
          return
        }
        const value = node.value
        if (value === null || value.type === 'JSXElement') {
          return
        }
        const candidate =
          value.type === 'JSXExpressionContainer' ? value.expression : value
        for (const { node: literal, text } of plainTextLiterals(candidate)) {
          context.report({
            node: literal,
            messageId: isChildren ? 'literalText' : 'literalAttribute',
            data: isChildren
              ? { text: text.slice(0, 40) }
              : { name: node.name.name, text: text.slice(0, 40) },
          })
        }
      },
    }
  },
}
