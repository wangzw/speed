import { RuleTester } from 'eslint'
import { describe, expect, it } from 'vitest'
import { noLiteralTextRule } from './no-literal-text.js'

const ruleTester = new RuleTester({
  languageOptions: {
    parserOptions: {
      ecmaFeatures: { jsx: true },
      sourceType: 'module',
    },
  },
})

function runRule(valid, invalid) {
  try {
    ruleTester.run('no-literal-text', noLiteralTextRule, { valid, invalid })
  } catch (error) {
    expect.fail(`rule tests failed: ${error.message}`)
  }
}

describe('no-literal-text rule', () => {
  it('accepts text routed through t() and computed expressions', () => {
    runRule(
      [
        "<Button>{t('empty.title')}</Button>",
        "<Button aria-label={t('empty.title')}>{t('empty.title')}</Button>",
        '<Button>{row.name}</Button>',
        '<img alt={row.altText} />',
        "<Button aria-label={`row ${row.id}`} />",
        '<div> </div>',
        '<div>\n  \n</div>',
        '<input type="email" value={query} />',
        "<Button>{ok ? t('save.title') : t('wait.title')}</Button>",
        '<Button aria-label={ok ? t(\'a.b\') : `row ${row.id}`} />',
        '<div title="" />',
        '<div placeholder="   " />',
      ],
      [],
    )
  })

  it('flags bare JSX text nodes', () => {
    runRule(
      [],
      [
        { code: '<Button>Save</Button>', errors: [{ messageId: 'literalText' }] },
        {
          code: '<Button>Save your work before leaving</Button>',
          errors: [{ messageId: 'literalText' }],
        },
        { code: '<p>Hello world</p>', errors: [{ messageId: 'literalText' }] },
        {
          code: '<Button>Save <b>now</b></Button>',
          errors: [{ messageId: 'literalText' }, { messageId: 'literalText' }],
        },
      ],
    )
  })

  it('flags string-literal children, including template forms', () => {
    runRule(
      [],
      [
        {
          code: "<Button>{'Save'}</Button>",
          errors: [{ messageId: 'literalText' }],
        },
        {
          code: '<Button>{`Save`}</Button>',
          errors: [{ messageId: 'literalText' }],
        },
      ],
    )
  })

  it('flags text under aria-hidden: aria-hidden hides from assistive technology only, not from sight', () => {
    runRule(
      [],
      [
        {
          code: '<span aria-hidden="true">·</span>',
          errors: [{ messageId: 'literalText' }],
        },
        {
          code: '<span aria-hidden>{`·`}</span>',
          errors: [{ messageId: 'literalText' }],
        },
        {
          code: '<span aria-hidden={true}>{`·`}</span>',
          errors: [{ messageId: 'literalText' }],
        },
        {
          code: '<div aria-hidden={"true"}><span>decorative</span></div>',
          errors: [{ messageId: 'literalText' }],
        },
        {
          code: '<div aria-hidden={true}><span>Status text</span></div>',
          errors: [{ messageId: 'literalText' }],
        },
      ],
    )
  })

  it('flags plain-string branches of conditional expressions in children and attributes', () => {
    runRule(
      [],
      [
        {
          code: "<Button>{ok ? 'Save' : 'Wait'}</Button>",
          errors: [
            { messageId: 'literalText' },
            { messageId: 'literalText' },
          ],
        },
        {
          code: "<Button>{ok ? 'Save' : t('wait.title')}</Button>",
          errors: [{ messageId: 'literalText' }],
        },
        {
          code: "<Button>{ok ? t('save.title') : 'Wait'}</Button>",
          errors: [{ messageId: 'literalText' }],
        },
        {
          code: "<Button>{ok ? (nope ? 'X' : 'Y') : 'Z'}</Button>",
          errors: [
            { messageId: 'literalText' },
            { messageId: 'literalText' },
            { messageId: 'literalText' },
          ],
        },
        {
          code: "<Button>{ok ? `still plain` : t('wait.title')}</Button>",
          errors: [{ messageId: 'literalText' }],
        },
        {
          code: '<Button aria-label={ok ? \'Save\' : \'Wait\'} />',
          errors: [
            { messageId: 'literalAttribute' },
            { messageId: 'literalAttribute' },
          ],
        },
        {
          code: "<img alt={ok ? 'logo' : t('a.placeholder')} />",
          errors: [{ messageId: 'literalAttribute' }],
        },
      ],
    )
  })

  it('flags plain-string operands of logical expressions in children and attributes', () => {
    runRule(
      [
        "<Button>{ok && t('save.title')}</Button>",
        "<Button>{name ?? t('fallback.title')}</Button>",
        '<Button>{ok && row.name}</Button>',
        "<Button aria-label={ok && t('a.b')} />",
      ],
      [
        {
          code: "<Button>{ok && 'Saving...'}</Button>",
          errors: [{ messageId: 'literalText' }],
        },
        {
          code: "<Button>{name ?? 'Saving...'}</Button>",
          errors: [{ messageId: 'literalText' }],
        },
        {
          code: "<Button>{ok || 'Saving...'}</Button>",
          errors: [{ messageId: 'literalText' }],
        },
        {
          code: "<Button>{ok && ready && 'Saving...'}</Button>",
          errors: [{ messageId: 'literalText' }],
        },
        {
          code: "<Button>{ok ? 'Save' : pending && 'Wait'}</Button>",
          errors: [
            { messageId: 'literalText' },
            { messageId: 'literalText' },
          ],
        },
        {
          code: "<Button aria-label={ok && 'Save'} />",
          errors: [{ messageId: 'literalAttribute' }],
        },
        {
          code: "<img alt={name ?? 'logo'} />",
          errors: [{ messageId: 'literalAttribute' }],
        },
      ],
    )
  })

  it('flags literal text-bearing attribute values', () => {
    runRule(
      [],
      [
        {
          code: '<Button aria-label="Save" />',
          errors: [{ messageId: 'literalAttribute' }],
        },
        {
          code: "<Button placeholder={'Save'} />",
          errors: [{ messageId: 'literalAttribute' }],
        },
        {
          code: '<Button title={`Save`} />',
          errors: [{ messageId: 'literalAttribute' }],
        },
        {
          code: '<img alt="logo" />',
          errors: [{ messageId: 'literalAttribute' }],
        },
        {
          code: '<span aria-roledescription="slide" />',
          errors: [{ messageId: 'literalAttribute' }],
        },
      ],
    )
  })

  it('flags the children attribute carrying literal text like the JSXText form', () => {
    runRule(
      [
        "<Button children={t('empty.title')} />",
        '<Button children={row.name} />',
        '<Button children="   " />',
        '<div children="" />',
        "<Button children={`row ${row.id}`} />",
      ],
      [
        {
          code: '<Button children="Save" />',
          errors: [{ messageId: 'literalText' }],
        },
        {
          code: '<Button children={`Save`} />',
          errors: [{ messageId: 'literalText' }],
        },
        {
          code: "<Button children={'Save'} />",
          errors: [{ messageId: 'literalText' }],
        },
        {
          code: "<Button children={ok ? 'Save' : 'Wait'} />",
          errors: [
            { messageId: 'literalText' },
            { messageId: 'literalText' },
          ],
        },
        {
          code: "<Button children={ok && 'Save'} />",
          errors: [{ messageId: 'literalText' }],
        },
        {
          code: '<span children="Save" aria-label="Save" />',
          errors: [
            { messageId: 'literalText' },
            { messageId: 'literalAttribute' },
          ],
        },
      ],
    )
  })

  it('reports the offending text inside the message', () => {
    runRule(
      [],
      [
        {
          code: '<Button>Delete forever</Button>',
          errors: [
            {
              message:
                'User-facing text must come from the i18n namespace, not be written inline. Render "Delete forever" through a t() call (see the i18n section of the frontend coding standards).',
            },
          ],
        },
        {
          code: '<Button aria-label="Save" />',
          errors: [
            {
              message:
                'The aria-label value is user-facing text; pass it through a t() call instead of the literal "Save" (see the i18n section of the frontend coding standards).',
            },
          ],
        },
      ],
    )
  })

  it('reports a location-aware message for each violation site', () => {
    runRule(
      [],
      [
        {
          code: '<Button>Save</Button>',
          errors: [{ messageId: 'literalText', line: 1, column: 9 }],
        },
        {
          code: '<Button title="Save" />',
          errors: [{ messageId: 'literalAttribute', line: 1, column: 15 }],
        },
      ],
    )
  })
})
