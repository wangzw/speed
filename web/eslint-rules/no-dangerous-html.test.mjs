/**
 * Rule tests for no-dangerous-html. Run from the web workspace root:
 *
 *   pnpm exec vitest run eslint-rules/no-dangerous-html.test.mjs
 *
 * The vitest CLI filter keeps this single-file run inside the plain
 * node environment; the packages' own tests (which need their jsdom
 * configs) are not picked up.
 *
 * Test snippets are fixture code for the rule under test, not rendered
 * product text.
 */

import { RuleTester } from 'eslint'
import { describe, expect, it } from 'vitest'
import { noDangerousHtmlRule } from './no-dangerous-html.js'

const ruleTester = new RuleTester({
  languageOptions: {
    parserOptions: {
      ecmaFeatures: { jsx: true },
      sourceType: 'module',
    },
  },
})

/**
 * RuleTester.run throws an aggregate error listing every failing case;
 * surface that error as the test failure instead of swallowing it.
 */
function runRule(valid, invalid) {
  try {
    ruleTester.run('no-dangerous-html', noDangerousHtmlRule, { valid, invalid })
  } catch (error) {
    expect.fail(`rule tests failed: ${error.message}`)
  }
}

describe('no-dangerous-html rule', () => {
  it('accepts rendering values as text', () => {
    runRule(
      [
        // React escapes interpolated values when it renders them as text.
        '<div>{value}</div>',
        '<div title={value} />',
        // Reading an HTML member is not a sink.
        'const markup = node.innerHTML;',
        "const markup = node['outerHTML'];",
        // Other text-writing APIs do not parse markup.
        "node.textContent = value;",
        "node.insertAdjacentText('beforeend', value);",
        // A different object's write is not the environment document.
        'logger.write(value);',
        "registry['write'](value);",
        // A locally bound document shadows the environment object.
        'function render(document) { document.write(value); }',
        'const document = { write() {} }; document.write(value);',
        // insertAdjacentHTML on a locally-shaped object still matches by
        // name -- the rule judges the sink name, not the receiver type --
        // so this case is deliberately absent from the valid set.
      ],
      [],
    )
  })

  it('flags the dangerouslySetInnerHTML prop in every value form', () => {
    runRule(
      [],
      [
        {
          code: '<div dangerouslySetInnerHTML={{ __html: value }} />',
          errors: [{ messageId: 'dangerousProp' }],
        },
        {
          code: '<div dangerouslySetInnerHTML={html} />',
          errors: [{ messageId: 'dangerousProp' }],
        },
        {
          code: '<div dangerouslySetInnerHTML={{ __html: t(\'a.b\') }} />',
          errors: [{ messageId: 'dangerousProp' }],
        },
      ],
    )
  })

  it('flags writes to innerHTML and outerHTML', () => {
    runRule(
      [],
      [
        {
          code: "node.innerHTML = '<b>bold</b>';",
          errors: [{ messageId: 'htmlMemberWrite' }],
        },
        {
          code: 'node.innerHTML += value;',
          errors: [{ messageId: 'htmlMemberWrite' }],
        },
        {
          code: "node['innerHTML'] = value;",
          errors: [{ messageId: 'htmlMemberWrite' }],
        },
        {
          code: "node.outerHTML = '<p>x</p>';",
          errors: [{ messageId: 'htmlMemberWrite' }],
        },
        {
          code: "node['outerHTML'] = value;",
          errors: [{ messageId: 'htmlMemberWrite' }],
        },
        {
          code: 'document.body.innerHTML = value;',
          errors: [{ messageId: 'htmlMemberWrite' }],
        },
      ],
    )
  })

  it('flags insertAdjacentHTML on any receiver', () => {
    runRule(
      [],
      [
        {
          code: "node.insertAdjacentHTML('beforeend', value);",
          errors: [{ messageId: 'insertAdjacentHtml' }],
        },
        {
          code: "document.body.insertAdjacentHTML('afterbegin', value);",
          errors: [{ messageId: 'insertAdjacentHtml' }],
        },
        {
          code: "node['insertAdjacentHTML']('afterend', value);",
          errors: [{ messageId: 'insertAdjacentHtml' }],
        },
      ],
    )
  })

  it('flags document.write, including the computed spelling', () => {
    runRule(
      [],
      [
        {
          code: "document.write('<p>x</p>');",
          errors: [{ messageId: 'documentWrite' }],
        },
        {
          code: "document['write'](value);",
          errors: [{ messageId: 'documentWrite' }],
        },
      ],
    )
  })

  it('reports the offending member name', () => {
    runRule(
      [],
      [
        {
          code: 'node.innerHTML = value;',
          errors: [
            {
              message:
                'Assigning to innerHTML writes markup into the page; no layer here may consume HTML (interpolation.escapeValue is false, so interpolated values enter the text verbatim). Set textContent or render the value through React instead.',
            },
          ],
        },
      ],
    )
  })
})
