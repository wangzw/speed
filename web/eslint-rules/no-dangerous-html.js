/**
 * no-dangerous-html: no runtime layer consumes HTML.
 *
 * The i18n instance pins `interpolation.escapeValue: false`, so a
 * translated string carries its interpolated values verbatim: React
 * escapes the string once when it renders it as a text node, and the
 * engine must not escape it again. That trade is safe only while no
 * layer treats a rendered string as markup. An HTML sink introduced
 * later would turn every interpolated value -- a tenant alias, a
 * display name, a path -- into markup, silently reopening an XSS
 * channel that the instance option deliberately closed. This rule
 * makes the premise executable instead of a grep habit.
 *
 * The rule rejects the direct sinks:
 *
 *   - the `dangerouslySetInnerHTML` JSX prop (`<div
 *     dangerouslySetInnerHTML={{ __html: value }} />`),
 *   - an assignment to an element's `innerHTML` or `outerHTML` member
 *     (`node.innerHTML = value`, `node['outerHTML'] = value`, compound
 *     assignment included),
 *   - an `insertAdjacentHTML` call on any receiver, and
 *   - `document.write(...)` (`document['write'](...)` included).
 *
 * Reading `innerHTML`/`outerHTML` is not a sink and stays legal, and a
 * different object's `write`/`insertAdjacentHTML`-shaped member is
 * judged by name (the rule cannot prove what an arbitrary receiver is;
 * the sink names are the check). A `document` that resolves to a real
 * local binding is not the environment document, so the same shadowing
 * rule the no-direct-http guard applies holds here. Dataflow the rule
 * does not chase -- aliasing an HTML sink method before calling it --
 * stays code review's residue.
 *
 * Scope is applied in eslint.config.mjs over runtime source; test files
 * and test utilities are excluded by the config, not by the rule --
 * keep the rule itself free of path assumptions.
 */

/** The member name an expression reads or writes, or undefined. A
 * computed member counts when its key is a string literal: the
 * `node['innerHTML']` spelling names the same member as the dot form. */
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

/** Whether any scope from the node upward binds `name` with a real
 * declaration. A host config merely declaring `document` as a global
 * creates a scope variable with no definitions behind it, and declaring
 * one must not silence the guard. */
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

/** The HTML members whose write puts markup into the page. */
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
