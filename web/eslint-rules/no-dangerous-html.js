function memberName(node) {
  if (node.type !== 'MemberExpression') {
    return undefined
  }
  if (!node.computed) {
    return node.property.type === 'Identifier' ? node.property.name : undefined
  }
  return node.property.type === 'Literal' &&
    typeof node.property.value === 'string'
    ? node.property.value
    : undefined
}

// A name the host merely declares as a global has a scope variable with no
// definitions, and must not silence the guard.
function isBound(name, node, sourceCode) {
  for (
    let scope = sourceCode.getScope(node);
    scope !== null && scope !== undefined;
    scope = scope.upper
  ) {
    if (
      scope.variables.some(
        (variable) => variable.name === name && variable.defs.length > 0,
      )
    ) {
      return true
    }
  }
  return false
}

const HTML_WRITE_MEMBERS = new Set(['innerHTML', 'outerHTML'])

export const noDangerousHtmlRule = {
  meta: {
    type: 'problem',
    docs: {
      description:
        'Disallow HTML sinks in runtime code; no layer in this workspace consumes HTML (interpolation.escapeValue is false, so values enter a translated string verbatim).',
    },
    messages: {
      dangerousProp:
        'dangerouslySetInnerHTML renders a string as markup; no layer here may consume HTML (interpolation.escapeValue is false, so interpolated values enter the text verbatim). Render the value as text through React.',
      htmlMemberWrite:
        'Assigning to {{member}} writes markup into the page; no layer here may consume HTML (interpolation.escapeValue is false, so interpolated values enter the text verbatim). Set textContent or render the value through React instead.',
      insertAdjacentHtml:
        'insertAdjacentHTML writes markup into the page; no layer here may consume HTML (interpolation.escapeValue is false, so interpolated values enter the text verbatim). Build the nodes through React instead.',
      documentWrite:
        'document.write writes markup into the page; no layer here may consume HTML (interpolation.escapeValue is false, so interpolated values enter the text verbatim). Render the value through React instead.',
    },
    schema: [],
  },
  create(context) {
    const sourceCode = context.sourceCode

    return {
      JSXAttribute(node) {
        if (
          node.name.type === 'JSXIdentifier' &&
          node.name.name === 'dangerouslySetInnerHTML'
        ) {
          context.report({ node, messageId: 'dangerousProp' })
        }
      },
      AssignmentExpression(node) {
        const member = memberName(node.left)
        if (member !== undefined && HTML_WRITE_MEMBERS.has(member)) {
          context.report({
            node: node.left,
            messageId: 'htmlMemberWrite',
            data: { member },
          })
        }
      },
      CallExpression(node) {
        const callee = node.callee
        if (callee.type !== 'MemberExpression') {
          return
        }
        const member = memberName(callee)
        if (member === 'insertAdjacentHTML') {
          context.report({ node: callee, messageId: 'insertAdjacentHtml' })
          return
        }
        if (
          member === 'write' &&
          callee.object.type === 'Identifier' &&
          callee.object.name === 'document' &&
          !isBound('document', callee.object, sourceCode)
        ) {
          context.report({ node: callee, messageId: 'documentWrite' })
        }
      },
    }
  },
}
