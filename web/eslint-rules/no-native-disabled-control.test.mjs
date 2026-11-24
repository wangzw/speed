import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { Linter, RuleTester } from 'eslint'
import tseslint from 'typescript-eslint'
import { describe, expect, it } from 'vitest'
import {
  NATIVE_DISABLED_ELEMENTS,
  NATIVE_DISABLED_EXEMPTIONS,
  noNativeDisabledControlRule,
} from './no-native-disabled-control.js'

const ruleTester = new RuleTester({
  languageOptions: {
    parserOptions: {
      ecmaFeatures: { jsx: true },
      sourceType: 'module',
    },
  },
})

const TS_LANGUAGE_OPTIONS = {
  parser: tseslint.parser,
  parserOptions: {
    ecmaFeatures: { jsx: true },
    sourceType: 'module',
  },
}

function runRule(valid, invalid) {
  try {
    ruleTester.run('no-native-disabled-control', noNativeDisabledControlRule, {
      valid,
      invalid,
    })
  } catch (error) {
    expect.fail(`rule tests failed: ${error.message}`)
  }
}

const PACKAGE_FILE = 'packages/admin-ui/src/AccountsPage.tsx'
const HOST_FILE = 'starters/demo/web/src/pages/home-page.tsx'
const TENANCY_FILE = 'packages/tenancy-ui/src/TenantSwitcher.tsx'
const HUMAN_VERIFICATION_FILE =
  'packages/ui-kit/src/components/HumanVerification.tsx'

const BUTTON_IMPORT = "import Button from '@mui/material/Button'\n"

function attributed(code) {
  return { filename: PACKAGE_FILE, code }
}

describe('no-native-disabled-control rule', () => {
  it('flags every non-literal disabled expression', () => {
    runRule(
      [],
      [
        attributed(
          `${BUTTON_IMPORT}function SaveButton() { return <Button disabled={pending} /> }`,
        ),
        attributed(
          `${BUTTON_IMPORT}function SaveButton() { return <Button disabled={!canSave} /> }`,
        ),
        attributed(
          `${BUTTON_IMPORT}function SaveButton() { return <Button disabled={a || b} /> }`,
        ),
        attributed(
          `${BUTTON_IMPORT}function SaveButton({ disabled }) { return <Button disabled={disabled} /> }`,
        ),
        attributed(
          `${BUTTON_IMPORT}function SaveButton() { return <Button disabled={undefined} /> }`,
        ),
      ].map((entry) => ({
        ...entry,
        errors: [
          {
            messageId: 'nonLiteralDisabled',
            data: { element: 'Button', component: 'SaveButton' },
          },
        ],
      })),
    )
  })

  it('flags every loading value that is not the literal false', () => {
    runRule(
      [],
      [
        attributed(
          `${BUTTON_IMPORT}function SaveButton() { return <Button loading /> }`,
        ),
        attributed(
          `${BUTTON_IMPORT}function SaveButton() { return <Button loading={true} /> }`,
        ),
        attributed(
          `${BUTTON_IMPORT}function SaveButton() { return <Button loading={pending} /> }`,
        ),
        attributed(
          `${BUTTON_IMPORT}function SaveButton() { return <Button loading={undefined} /> }`,
        ),
        attributed(
          `${BUTTON_IMPORT}function SaveButton() { return <Button loading="false" /> }`,
        ),
      ].map((entry) => ({
        ...entry,
        errors: [
          {
            messageId: 'loadingProp',
            data: { element: 'Button', component: 'SaveButton' },
          },
        ],
      })),
    )
  })

  it('flags loading and disabled on one element as two reports', () => {
    runRule([], [
      attributed(
        `${BUTTON_IMPORT}function SaveButton() { return <Button loading={pending} disabled={pending} /> }`,
      ),
    ].map((entry) => ({
      ...entry,
      errors: [
        {
          messageId: 'loadingProp',
          data: { element: 'Button', component: 'SaveButton' },
        },
        {
          messageId: 'nonLiteralDisabled',
          data: { element: 'Button', component: 'SaveButton' },
        },
      ],
    })))
  })

  it('follows default and named imports through aliases', () => {
    runRule(
      [],
      [
        attributed(
          "import Btn from '@mui/material/Button'\nfunction SaveButton() { return <Btn disabled={pending} /> }",
        ),
        attributed(
          "import { Button as MuiButton } from '@mui/material'\nfunction SaveButton() { return <MuiButton disabled={pending} /> }",
        ),
        attributed(
          "import { default as B } from '@mui/material/Button'\nfunction SaveButton() { return <B disabled={pending} /> }",
        ),
      ].map((entry, index) => ({
        ...entry,
        errors: [
          {
            messageId: 'nonLiteralDisabled',
            data: {
              element: ['Btn', 'MuiButton', 'B'][index],
              component: 'SaveButton',
            },
          },
        ],
      })),
    )
  })

  it('flags ListItemButton and Radio as covered controls', () => {
    runRule([], [
      attributed(
        "import ListItemButton from '@mui/material/ListItemButton'\nfunction Nav() { return <ListItemButton disabled={feedback.pending} /> }",
      ),
      attributed(
        "import { Radio } from '@mui/material'\nfunction Option() { return <Radio disabled={disabled} /> }",
      ),
    ].map((entry, index) => ({
      ...entry,
      errors: [
        {
          messageId: 'nonLiteralDisabled',
          data: {
            element: ['ListItemButton', 'Radio'][index],
            component: ['Nav', 'Option'][index],
          },
        },
      ],
    })))
  })

  it('flags every element the covered-element map lists', () => {
    const cases = []
    for (const [moduleName, exportNames] of Object.entries(
      NATIVE_DISABLED_ELEMENTS,
    )) {
      for (const exportName of exportNames) {
        const localName =
          exportName === 'default'
            ? moduleName.slice(moduleName.lastIndexOf('/') + 1)
            : exportName
        const importStatement =
          exportName === 'default'
            ? `import ${localName} from '${moduleName}'`
            : `import { ${exportName} as ${localName} } from '${moduleName}'`
        cases.push({
          filename: PACKAGE_FILE,
          code:
            `${importStatement}\n` +
            `function SaveButton() { return <${localName} disabled={pending} /> }`,
          errors: [
            {
              messageId: 'nonLiteralDisabled',
              data: { element: localName, component: 'SaveButton' },
            },
          ],
        })
      }
    }
    expect(cases).toHaveLength(21)
    runRule([], cases)
  })

  it('flags elements inside render callbacks and map callbacks', () => {
    runRule(
      [],
      [
        attributed(
          `${BUTTON_IMPORT}function Table() { return <AsyncSection>{() => <Button disabled={pending} />}</AsyncSection> }`,
        ),
        attributed(
          `${BUTTON_IMPORT}function List({ rows }) { return <ul>{rows.map((row) => <li key={row.id}><Button disabled={row.pending} /></li>)}</ul> }`,
        ),
      ].map((entry, index) => ({
        ...entry,
        errors: [
          {
            messageId: 'nonLiteralDisabled',
            data: {
              element: 'Button',
              component: index === 0 ? 'Table' : 'List',
            },
          },
        ],
      })),
    )
  })

  it('reports the same under a package path and a demo host path', () => {
    runRule([], [
      {
        filename: PACKAGE_FILE,
        code: `${BUTTON_IMPORT}function SaveButton() { return <Button disabled={pending} /> }`,
        errors: [{ messageId: 'nonLiteralDisabled' }],
      },
      {
        filename: HOST_FILE,
        code: `${BUTTON_IMPORT}function SaveButton() { return <Button disabled={pending} /> }`,
        errors: [{ messageId: 'nonLiteralDisabled' }],
      },
    ])
  })

  it('spells the element and the component in the message', () => {
    runRule([], [
      {
        filename: PACKAGE_FILE,
        code: `${BUTTON_IMPORT}function SaveButton() { return <Button disabled={pending} /> }`,
        errors: [
          {
            message:
              'Button in component SaveButton is disabled by a non-literal expression. A control that holds keyboard focus and becomes natively disabled drops focus to the document body. Use ActionButton with busy or unavailable, or aria-disabled with useActivationGuard, or register this component in NATIVE_DISABLED_EXEMPTIONS with a reason.',
          },
        ],
      },
    ])
  })

  it('accepts literal, bare and absent values', () => {
    runRule(
      [
        attributed(
          `${BUTTON_IMPORT}function SaveButton() { return <Button disabled /> }`,
        ),
        attributed(
          `${BUTTON_IMPORT}function SaveButton() { return <Button disabled={true} /> }`,
        ),
        attributed(
          `${BUTTON_IMPORT}function SaveButton() { return <Button disabled={false} /> }`,
        ),
        attributed(
          `${BUTTON_IMPORT}function SaveButton() { return <Button disabled="yes" /> }`,
        ),
        attributed(
          `${BUTTON_IMPORT}function SaveButton() { return <Button loading={false} /> }`,
        ),
        attributed(
          "import ListItemButton from '@mui/material/ListItemButton'\nfunction Nav() { return <ListItemButton disabled /> }",
        ),
        attributed(
          "import ListItemButton from '@mui/material/ListItemButton'\nfunction Nav() { return <ListItemButton disabled={false} /> }",
        ),
        attributed(
          "import Radio from '@mui/material/Radio'\nfunction Option() { return <Radio disabled /> }",
        ),
        attributed(
          "import Radio from '@mui/material/Radio'\nfunction Option() { return <Radio disabled={true} /> }",
        ),
      ],
      [],
    )
  })

  it('leaves elements outside the covered set alone', () => {
    runRule(
      [
        attributed(
          "import TextField from '@mui/material/TextField'\nfunction Field() { return <TextField disabled={pending} /> }",
        ),
        attributed(
          "import Select from '@mui/material/Select'\nfunction Field() { return <Select disabled={pending} /> }",
        ),
        attributed(
          "import Autocomplete from '@mui/material/Autocomplete'\nfunction Field() { return <Autocomplete disabled={pending} /> }",
        ),
        attributed(
          "import MenuItem from '@mui/material/MenuItem'\nfunction Item() { return <MenuItem disabled={pending} /> }",
        ),
      ],
      [],
    )
  })

  it('leaves same-named components from other sources alone', () => {
    runRule(
      [
        attributed(
          "import { ActionButton } from '@speed/ui-kit'\nfunction SaveButton() { return <ActionButton disabled={pending} loading={pending} /> }",
        ),
        attributed(
          'function Button() { return null }\nfunction SaveButton() { return <Button disabled={pending} /> }',
        ),
        attributed(
          `${BUTTON_IMPORT}function SaveButton({ Button }) { return <Button disabled={pending} /> }`,
        ),
        attributed(
          `${BUTTON_IMPORT}function SaveButton() { const Button = () => null; return <Button disabled={pending} /> }`,
        ),
      ],
      [],
    )
  })

  it('leaves native lowercase tags, spreads and createElement alone', () => {
    runRule(
      [
        attributed(
          'function SaveButton() { return <button disabled={pending}>Save</button> }',
        ),
        attributed(
          'function Field() { return <input disabled={pending} /> }',
        ),
        attributed(
          `${BUTTON_IMPORT}function SaveButton() { return <Button aria-disabled={busy} /> }`,
        ),
        attributed(
          `${BUTTON_IMPORT}function SaveButton(props) { return <Button {...props} /> }`,
        ),
        attributed(
          `${BUTTON_IMPORT}function SaveButton() { return createElement(Button, { disabled: pending }) }`,
        ),
      ],
      [],
    )
  })

  it('does not bind a type-only import', () => {
    runRule(
      [
        {
          filename: PACKAGE_FILE,
          languageOptions: TS_LANGUAGE_OPTIONS,
          code: "import type Button from '@mui/material/Button'\nfunction Page() { return <Button disabled={pending} /> }",
        },
        {
          filename: PACKAGE_FILE,
          languageOptions: TS_LANGUAGE_OPTIONS,
          code: "import { type Button } from '@mui/material'\nfunction Page() { return <Button disabled={pending} /> }",
        },
      ],
      [],
    )
  })

  it('judges TypeScript expressions on covered elements', () => {
    runRule(
      [],
      [
        {
          filename: PACKAGE_FILE,
          languageOptions: TS_LANGUAGE_OPTIONS,
          code: `${BUTTON_IMPORT}function SaveButton() { return <Button disabled={pending as boolean} /> }`,
          errors: [
            {
              messageId: 'nonLiteralDisabled',
              data: { element: 'Button', component: 'SaveButton' },
            },
          ],
        },
        {
          filename: PACKAGE_FILE,
          languageOptions: TS_LANGUAGE_OPTIONS,
          code: `${BUTTON_IMPORT}function SaveButton<T extends string>(props: { value: T }) { return <Button disabled={!props.value} /> }`,
          errors: [
            {
              messageId: 'nonLiteralDisabled',
              data: { element: 'Button', component: 'SaveButton' },
            },
          ],
        },
      ],
    )
  })
})

describe('no-native-disabled-control report location', () => {
  const webRoot = fileURLToPath(new URL('..', import.meta.url))
  const linter = new Linter({ configType: 'flat', cwd: webRoot })

  function messagesFor(code) {
    return linter.verify(
      code,
      [
        {
          files: ['**/*.tsx'],
          languageOptions: {
            parserOptions: {
              ecmaFeatures: { jsx: true },
              sourceType: 'module',
            },
          },
          plugins: {
            speed: {
              rules: {
                'no-native-disabled-control': noNativeDisabledControlRule,
              },
            },
          },
          rules: { 'speed/no-native-disabled-control': 'error' },
        },
      ],
      PACKAGE_FILE,
    )
  }

  it('reports on the offending attribute, one report per attribute', () => {
    const disabledCode = `${BUTTON_IMPORT}function SaveButton() { return <Button disabled={pending} /> }`
    const disabled = messagesFor(disabledCode)
    expect(disabled).toHaveLength(1)
    expect(disabled[0].line).toBe(2)
    const disabledLine = disabledCode.split('\n')[1]
    expect(disabled[0].column).toBe(disabledLine.indexOf('disabled') + 1)
    expect(
      disabledLine.slice(disabled[0].column - 1, disabled[0].endColumn - 1),
    ).toBe('disabled={pending}')

    const loadingCode = `${BUTTON_IMPORT}function SaveButton() { return <Button loading={pending} /> }`
    const loading = messagesFor(loadingCode)
    expect(loading).toHaveLength(1)
    expect(loading[0].line).toBe(2)
    const loadingLine = loadingCode.split('\n')[1]
    expect(loading[0].column).toBe(loadingLine.indexOf('loading') + 1)
    expect(
      loadingLine.slice(loading[0].column - 1, loading[0].endColumn - 1),
    ).toBe('loading={pending}')

    const bothCode = `${BUTTON_IMPORT}function SaveButton() { return <Button loading={pending} disabled={pending} /> }`
    const both = messagesFor(bothCode)
    expect(both.map((message) => message.messageId)).toEqual([
      'loadingProp',
      'nonLiteralDisabled',
    ])
    const line = bothCode.split('\n')[1]
    expect(both[0].column).toBe(line.indexOf('loading') + 1)
    expect(both[1].column).toBe(line.indexOf('disabled') + 1)
  })
})

describe('no-native-disabled-control declaring component names', () => {
  it('names each enclosing declaration form', () => {
    runRule(
      [],
      [
        {
          filename: PACKAGE_FILE,
          code: `${BUTTON_IMPORT}function SaveButton() { return <Button disabled={pending} /> }`,
          errors: [
            {
              messageId: 'nonLiteralDisabled',
              data: { element: 'Button', component: 'SaveButton' },
            },
          ],
        },
        {
          filename: PACKAGE_FILE,
          code: `${BUTTON_IMPORT}const SaveButton = () => <Button disabled={pending} />`,
          errors: [
            {
              messageId: 'nonLiteralDisabled',
              data: { element: 'Button', component: 'SaveButton' },
            },
          ],
        },
        {
          filename: PACKAGE_FILE,
          code: `${BUTTON_IMPORT}const SaveButton = function () { return <Button disabled={pending} /> }`,
          errors: [
            {
              messageId: 'nonLiteralDisabled',
              data: { element: 'Button', component: 'SaveButton' },
            },
          ],
        },
        {
          filename: PACKAGE_FILE,
          code: `${BUTTON_IMPORT}const SaveButton = function InnerName() { return <Button disabled={pending} /> }`,
          errors: [
            {
              messageId: 'nonLiteralDisabled',
              data: { element: 'Button', component: 'InnerName' },
            },
          ],
        },
        {
          filename: PACKAGE_FILE,
          code: `${BUTTON_IMPORT}const SaveButton = forwardRef((props, ref) => <Button disabled={pending} />)`,
          errors: [
            {
              messageId: 'nonLiteralDisabled',
              data: { element: 'Button', component: 'SaveButton' },
            },
          ],
        },
        {
          filename: PACKAGE_FILE,
          code: `${BUTTON_IMPORT}const SaveButton = React.forwardRef(function (props, ref) { return <Button disabled={pending} /> })`,
          errors: [
            {
              messageId: 'nonLiteralDisabled',
              data: { element: 'Button', component: 'SaveButton' },
            },
          ],
        },
        {
          filename: PACKAGE_FILE,
          code: `${BUTTON_IMPORT}const SaveButton = memo(() => <Button disabled={pending} />)`,
          errors: [
            {
              messageId: 'nonLiteralDisabled',
              data: { element: 'Button', component: 'SaveButton' },
            },
          ],
        },
        {
          filename: PACKAGE_FILE,
          code: `${BUTTON_IMPORT}const SaveButton = React.memo(function InnerName() { return <Button disabled={pending} /> })`,
          errors: [
            {
              messageId: 'nonLiteralDisabled',
              data: { element: 'Button', component: 'SaveButton' },
            },
          ],
        },
        {
          filename: PACKAGE_FILE,
          code: `${BUTTON_IMPORT}function Table() { return <AsyncSection>{() => <Button disabled={pending} />}</AsyncSection> }`,
          errors: [
            {
              messageId: 'nonLiteralDisabled',
              data: { element: 'Button', component: 'Table' },
            },
          ],
        },
      ],
    )
  })
})

describe('no-native-disabled-control exemptions', () => {
  it('exempts the registered component in the registered file', () => {
    runRule(
      [
        {
          filename: TENANCY_FILE,
          code: `${BUTTON_IMPORT}function TenantSwitcher() { return <Button disabled={currentTenant === null} /> }`,
        },
      ],
      [],
    )
  })

  it('reports an inner capital-named declaration in an exempt file', () => {
    runRule([], [
      {
        filename: TENANCY_FILE,
        code: `${BUTTON_IMPORT}function TenantSwitcher() { function Row() { return <Button disabled={currentTenant === null} /> } return <Row /> }`,
        errors: [
          {
            messageId: 'nonLiteralDisabled',
            data: { element: 'Button', component: 'Row' },
          },
        ],
      },
    ])
  })

  it('names a lowercase helper after the component that declares it', () => {
    runRule(
      [
        {
          filename: TENANCY_FILE,
          code: `${BUTTON_IMPORT}function TenantSwitcher() { const renderRow = () => <Button disabled={currentTenant === null} />; return <div>{renderRow()}</div> }`,
        },
      ],
      [],
    )
  })

  it('records <component> for an anonymous top-level component, unexempted', () => {
    runRule([], [
      {
        filename: TENANCY_FILE,
        code: `${BUTTON_IMPORT}export default function () { return <Button disabled={pending} /> }`,
        errors: [
          {
            messageId: 'nonLiteralDisabled',
            data: { element: 'Button', component: '<component>' },
          },
        ],
      },
      {
        filename: TENANCY_FILE,
        code: `${BUTTON_IMPORT}export default forwardRef((props, ref) => <Button disabled={pending} />)`,
        errors: [
          {
            messageId: 'nonLiteralDisabled',
            data: { element: 'Button', component: '<component>' },
          },
        ],
      },
    ])
  })

  it('matches the registered file through an absolute or backslash path', () => {
    runRule(
      [
        {
          filename:
            '/repo/checkout/web/packages/tenancy-ui/src/TenantSwitcher.tsx',
          code: `${BUTTON_IMPORT}function TenantSwitcher() { return <Button disabled={currentTenant === null} /> }`,
        },
        {
          filename: 'packages\\tenancy-ui\\src\\TenantSwitcher.tsx',
          code: `${BUTTON_IMPORT}function TenantSwitcher() { return <Button disabled={currentTenant === null} /> }`,
        },
      ],
      [],
    )
  })

  it('still reports another component in the exempt file', () => {
    runRule([], [
      {
        filename: TENANCY_FILE,
        code: `${BUTTON_IMPORT}function TenantToolbar() { return <Button disabled={pending} /> }`,
        errors: [
          {
            messageId: 'nonLiteralDisabled',
            data: { element: 'Button', component: 'TenantToolbar' },
          },
        ],
      },
    ])
  })

  it('still reports the same component name in a non-exempt file', () => {
    runRule([], [
      {
        filename: 'packages/admin-ui/src/TenantSwitcher.tsx',
        code: `${BUTTON_IMPORT}function TenantSwitcher() { return <Button disabled={pending} /> }`,
        errors: [
          {
            messageId: 'nonLiteralDisabled',
            data: { element: 'Button', component: 'TenantSwitcher' },
          },
        ],
      },
    ])
  })

  it('does not exempt the loading prop', () => {
    runRule([], [
      {
        filename: TENANCY_FILE,
        code: `${BUTTON_IMPORT}function TenantSwitcher() { return <Button loading={pending} /> }`,
        errors: [
          {
            messageId: 'loadingProp',
            data: { element: 'Button', component: 'TenantSwitcher' },
          },
        ],
      },
      {
        filename: TENANCY_FILE,
        code: `${BUTTON_IMPORT}function TenantSwitcher() { return <Button loading={pending} disabled={pending} /> }`,
        errors: [
          {
            messageId: 'loadingProp',
            data: { element: 'Button', component: 'TenantSwitcher' },
          },
        ],
      },
    ])
  })

  it('reports every unregistered component in a file with no exemption', () => {
    runRule([], [
      {
        filename: HUMAN_VERIFICATION_FILE,
        code: `${BUTTON_IMPORT}function PowBlock({ disabled, onRetry }) { return <Button type="button" size="small" variant="outlined" disabled={disabled} onClick={onRetry}>Retry</Button> }`,
        errors: [
          {
            messageId: 'nonLiteralDisabled',
            data: { element: 'Button', component: 'PowBlock' },
          },
        ],
      },
      {
        filename: HUMAN_VERIFICATION_FILE,
        code: `${BUTTON_IMPORT}function CaptchaForm({ disabled, answer }) { return <Button type="button" variant="outlined" size="small" disabled={disabled || answer.trim() === ''}>Submit</Button> }`,
        errors: [
          {
            messageId: 'nonLiteralDisabled',
            data: { element: 'Button', component: 'CaptchaForm' },
          },
        ],
      },
      {
        filename: HUMAN_VERIFICATION_FILE,
        code: `${BUTTON_IMPORT}function CaptchaForm({ disabled, audioWait }) { return <Button type="button" size="small" disabled={disabled || audioWait !== undefined}>Use audio</Button> }`,
        errors: [
          {
            messageId: 'nonLiteralDisabled',
            data: { element: 'Button', component: 'CaptchaForm' },
          },
        ],
      },
      {
        filename: HUMAN_VERIFICATION_FILE,
        code: `${BUTTON_IMPORT}function CaptchaForm({ disabled }) { return <Button type="button" size="small" disabled={disabled}>Refresh</Button> }`,
        errors: [
          {
            messageId: 'nonLiteralDisabled',
            data: { element: 'Button', component: 'CaptchaForm' },
          },
        ],
      },
      {
        filename: HUMAN_VERIFICATION_FILE,
        code: `${BUTTON_IMPORT}function CaptchaBlock({ disabled, onRetry }) { return <Button type="button" size="small" variant="outlined" disabled={disabled} onClick={onRetry}>Retry</Button> }`,
        errors: [
          {
            messageId: 'nonLiteralDisabled',
            data: { element: 'Button', component: 'CaptchaBlock' },
          },
        ],
      },
    ])
  })
})

describe('no-native-disabled-control exports', () => {
  it('registers exactly one frozen exemption', () => {
    expect(Array.isArray(NATIVE_DISABLED_EXEMPTIONS)).toBe(true)
    expect(Object.isFrozen(NATIVE_DISABLED_EXEMPTIONS)).toBe(true)
    expect(NATIVE_DISABLED_EXEMPTIONS).toEqual([
      {
        file: 'packages/tenancy-ui/src/TenantSwitcher.tsx',
        component: 'TenantSwitcher',
        reason:
          'The trigger becomes unavailable only while the current tenant is unknown, a state in which it has never been focused or activated.',
      },
    ])
    for (const entry of NATIVE_DISABLED_EXEMPTIONS) {
      expect(Object.isFrozen(entry)).toBe(true)
    }
  })

  it('lists exactly the covered modules and exports, frozen', () => {
    expect(NATIVE_DISABLED_ELEMENTS).toEqual({
      '@mui/material/Button': ['default'],
      '@mui/material/IconButton': ['default'],
      '@mui/material/ButtonBase': ['default'],
      '@mui/material/Fab': ['default'],
      '@mui/material/ToggleButton': ['default'],
      '@mui/material/Switch': ['default'],
      '@mui/material/Checkbox': ['default'],
      '@mui/material/FormControlLabel': ['default'],
      '@mui/material/ListItemButton': ['default'],
      '@mui/material/Radio': ['default'],
      '@mui/lab/LoadingButton': ['default'],
      '@mui/material': [
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
      ],
    })
    expect(Object.isFrozen(NATIVE_DISABLED_ELEMENTS)).toBe(true)
    for (const exportNames of Object.values(NATIVE_DISABLED_ELEMENTS)) {
      expect(Object.isFrozen(exportNames)).toBe(true)
    }
  })
})

function runtimeTsxFiles(directory, found = []) {
  for (const entry of readdirSync(directory)) {
    if (
      entry === 'node_modules' ||
      entry === 'dist' ||
      entry === 'test-utils'
    ) {
      continue
    }
    const full = join(directory, entry)
    if (statSync(full).isDirectory()) {
      runtimeTsxFiles(full, found)
      continue
    }
    if (
      entry.endsWith('.tsx') &&
      !entry.endsWith('.test.tsx') &&
      !entry.endsWith('.spec.tsx')
    ) {
      found.push(full)
    }
  }
  return found
}

function parseProgram(file) {
  const { ast } = tseslint.parser.parseForESLint(readFileSync(file, 'utf8'), {
    filePath: file,
    sourceType: 'module',
    ecmaFeatures: { jsx: true },
    range: true,
    loc: true,
  })
  return ast
}

// Each file is linted under a filename no exemption names, so the scan sees
// every hit; the displayed path is rebuilt from the file's scope instead.
describe('no-native-disabled-control workspace scan', () => {
  const webRoot = fileURLToPath(new URL('..', import.meta.url))
  const repoRoot = join(webRoot, '..')
  const packagesRoot = join(webRoot, 'packages')
  const hostSrcRoot = join(repoRoot, 'starters/demo/web/src')

  const linter = new Linter({ configType: 'flat', cwd: repoRoot })
  const lintConfig = [
    {
      files: ['**/*.tsx'],
      linterOptions: { noInlineConfig: true },
      languageOptions: {
        parser: tseslint.parser,
        parserOptions: {
          ecmaFeatures: { jsx: true },
          sourceType: 'module',
        },
      },
      plugins: {
        speed: {
          rules: { 'no-native-disabled-control': noNativeDisabledControlRule },
        },
      },
      rules: { 'speed/no-native-disabled-control': 'error' },
    },
  ]

  function packageSrcDirs() {
    return readdirSync(packagesRoot).map((name) =>
      join(packagesRoot, name, 'src'),
    )
  }

  function sourceScopes() {
    return [
      packageSrcDirs().map((dir) => ({
        root: dir,
        prefix: `packages/${relative(packagesRoot, dir).split('\\').join('/')}/`,
      })),
      [{ root: hostSrcRoot, prefix: 'starters/demo/web/src/' }],
    ]
  }

  function filesUnder(scope) {
    if (!existsSync(scope.root) || !statSync(scope.root).isDirectory()) {
      expect.fail(`source root ${scope.root} does not exist`)
    }
    return runtimeTsxFiles(scope.root)
  }

  function displayPath(scope, file) {
    return `${scope.prefix}${relative(scope.root, file).split('\\').join('/')}`
  }

  function componentNamedBy(message, tail) {
    const marker = ' in component '
    const start = message.indexOf(marker) + marker.length
    return message.slice(start, message.indexOf(tail, start))
  }

  let cachedScan

  function scanWorkspace() {
    if (cachedScan !== undefined) {
      return cachedScan
    }
    const disabled = []
    const loading = []
    const unparsed = []
    for (const roots of sourceScopes()) {
      let scanned = 0
      for (const scope of roots) {
        const files = filesUnder(scope)
        scanned += files.length
        for (const file of files) {
          const display = displayPath(scope, file)
          const messages = linter.verify(
            readFileSync(file, 'utf8'),
            lintConfig,
            'scan.tsx',
          )
          for (const message of messages) {
            if (message.fatal === true) {
              unparsed.push(`${display}:${message.line} ${message.message}`)
              continue
            }
            if (message.messageId === 'nonLiteralDisabled') {
              disabled.push({
                file: display,
                component: componentNamedBy(
                  message.message,
                  ' is disabled by a non-literal expression.',
                ),
              })
              continue
            }
            if (message.messageId === 'loadingProp') {
              loading.push({
                file: display,
                component: componentNamedBy(
                  message.message,
                  " uses MUI's loading prop.",
                ),
                line: message.line,
              })
            }
          }
        }
      }
      expect(
        scanned,
        'runtime TSX files the scan covered in one config scope',
      ).toBeGreaterThan(0)
    }
    cachedScan = { disabled, loading, unparsed }
    return cachedScan
  }

  it('registers exactly the components the tree disables without a literal', () => {
    const { disabled, unparsed } = scanWorkspace()
    expect(unparsed, 'files the rule could not parse').toEqual([])
    const observed = new Set(
      disabled.map((entry) => `${entry.file}::${entry.component}`),
    )
    const declared = new Set(
      NATIVE_DISABLED_EXEMPTIONS.map(
        (entry) => `${entry.file}::${entry.component}`,
      ),
    )
    const unregistered = [...observed].filter((key) => !declared.has(key)).sort()
    const stale = [...declared].filter((key) => !observed.has(key)).sort()
    expect(
      unregistered,
      'components disabling a covered control without an exemption',
    ).toEqual([])
    expect(stale, 'exemptions no component needs any more').toEqual([])
  }, 60_000)

  it('reports no covered control using the loading prop', () => {
    const { loading } = scanWorkspace()
    expect(
      loading
        .map((entry) => `${entry.file}:${entry.line} (${entry.component})`)
        .sort(),
      'covered controls still natively disabled through the loading prop',
    ).toEqual([])
  }, 60_000)

  it('accepts no inline eslint-disable that names this rule', () => {
    const found = []
    for (const roots of sourceScopes()) {
      let scanned = 0
      for (const scope of roots) {
        const files = filesUnder(scope)
        scanned += files.length
        for (const file of files) {
          const display = displayPath(scope, file)
          for (const comment of parseProgram(file).comments ?? []) {
            if (
              /\beslint-disable(?:-next-line|-line)?\b/.test(comment.value) &&
              comment.value.includes('no-native-disabled-control')
            ) {
              found.push(`${display}:${comment.loc.start.line}`)
            }
          }
        }
      }
      expect(
        scanned,
        'runtime TSX files the scan covered in one config scope',
      ).toBeGreaterThan(0)
    }
    expect(found.sort(), 'inline eslint-disable comments naming this rule').toEqual(
      [],
    )
  }, 60_000)

  it('reports hits an unnamed inline disable would hide', () => {
    const snippets = {
      'next-line': [
        "import Button from '@mui/material/Button'",
        'export function NextLine({ busy }) {',
        '  // eslint-disable-next-line',
        '  return <Button disabled={busy}>Save</Button>',
        '}',
      ].join('\n'),
      'file-level': [
        '/* eslint-disable */',
        "import Button from '@mui/material/Button'",
        'export function WholeFile({ busy }) {',
        '  return <Button disabled={busy}>Save</Button>',
        '}',
      ].join('\n'),
    }
    const reportedLines = {}
    for (const [label, code] of Object.entries(snippets)) {
      reportedLines[label] = linter
        .verify(code, lintConfig, 'scan.tsx')
        .filter((message) => message.messageId === 'nonLiteralDisabled')
        .map((message) => message.line)
    }
    expect(
      reportedLines,
      'hits an unnamed inline disable would hide from the scan',
    ).toEqual({ 'next-line': [4], 'file-level': [4] })
  })
})
