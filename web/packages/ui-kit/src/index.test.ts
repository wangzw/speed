/**
 * The entry's export surface, guarded two ways.
 *
 * The package's public surface is exactly what src/index.ts re-exports.
 * The first assertion is a mechanism: it walks the entry's own
 * export-from declarations, enumerates each resolved source module's
 * exported symbols with the TypeScript compiler API, and asserts the
 * entry re-exports every one of them. A public name declared in a module
 * the entry already exposes and forgotten in the entry fails here.
 *
 * The mechanism is deliberately one-directional: the module list derives
 * from the entry's own declarations, so deleting a whole export block --
 * or a whole module the entry does not expose -- is invisible to it. The
 * second assertion pins the entry's exported name list against a fixed
 * list, so a lost export of either kind (a value, or a type-only name
 * such as WriteOutcome) fails here too.
 *
 * The third assertion guards the module augmentations (`declare module
 * '@mui/material/styles'`): a consumer that compiles against this
 * package's emitted declarations loads only the modules the entry's
 * `.d.ts` graph reaches, so an augmentation declared in an internal
 * module never applies there and the property it adds reads as unknown.
 * Every augmentation therefore has to live in a module the entry
 * re-exports from.
 */

import { readFileSync } from 'node:fs'
import { dirname, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import ts from 'typescript'

const srcDir = resolve(dirname(fileURLToPath(import.meta.url)))

/** The entry's relative from-specifiers (`export { ... } from './x.js'`). */
function exportFromSpecifiers(source: ts.SourceFile): string[] {
  const specifiers: string[] = []
  for (const statement of source.statements) {
    if (!ts.isExportDeclaration(statement)) {
      continue
    }
    const specifier = statement.moduleSpecifier
    if (specifier !== undefined && ts.isStringLiteral(specifier)) {
      specifiers.push(specifier.text)
    }
  }
  return specifiers
}

/** All exported symbol names of one source file (values and types alike). */
function exportedNames(program: ts.Program, source: ts.SourceFile): string[] {
  const checker = program.getTypeChecker()
  const moduleSymbol = checker.getSymbolAtLocation(source)
  if (moduleSymbol === undefined) {
    return []
  }
  return checker
    .getExportsOfModule(moduleSymbol)
    .map((symbol) => symbol.name)
    .filter((name): name is string => name !== undefined)
}

const options: ts.CompilerOptions = {
  module: ts.ModuleKind.NodeNext,
  moduleResolution: ts.ModuleResolutionKind.NodeNext,
  target: ts.ScriptTarget.ES2022,
  skipLibCheck: true,
}

function createEntryProgram(): {
  readonly program: ts.Program
  readonly indexPath: string
  readonly entryFile: ts.SourceFile
  readonly modulePaths: readonly string[]
} {
  const indexPath = resolve(srcDir, 'index.ts')
  const entrySource = ts.createSourceFile(
    indexPath,
    readFileSync(indexPath, 'utf8'),
    ts.ScriptTarget.ES2022,
    /* setParentNodes */ true,
    ts.ScriptKind.TS,
  )
  const moduleHost: ts.ModuleResolutionHost = {
    fileExists: ts.sys.fileExists,
    readFile: ts.sys.readFile,
  }
  const modulePaths = exportFromSpecifiers(entrySource).map((specifier) => {
    const resolved = ts.resolveModuleName(
      specifier,
      indexPath,
      options,
      moduleHost,
    )
    return resolved.resolvedModule?.resolvedFileName
  })
  const program = ts.createProgram({
    rootNames: [
      indexPath,
      ...modulePaths.filter((path): path is string => path !== undefined),
    ],
    options,
  })
  const entryFile = program.getSourceFile(indexPath)
  expect(entryFile).not.toBeUndefined()
  return {
    program,
    indexPath,
    entryFile: entryFile!,
    modulePaths: modulePaths.filter(
      (path): path is string => path !== undefined,
    ),
  }
}

/** Every name a consumer can import from the package entry. */
const PUBLIC_EXPORTS = [
  'ActionButton',
  'ActionButtonProps',
  'ActionEmphasis',
  'ActionRow',
  'ActionRowProps',
  'ActionRowSize',
  'ActionTone',
  'AppTheme',
  'AppThemeMode',
  'AppThemeModeControl',
  'AppThemeModePreference',
  'AppThemePresetControl',
  'AppThemeProvider',
  'AppThemeProviderProps',
  'AsyncSection',
  'AsyncSectionEmptyState',
  'AsyncSectionErrorState',
  'AsyncSectionProps',
  'AsyncSectionRefusedState',
  'AsyncSectionStaleState',
  'CONFIRM_ARM_LOCKOUT_MS',
  'ConfirmDialog',
  'ConfirmDialogProps',
  'ConfirmDialogVariant',
  'CopyButton',
  'CopyButtonProps',
  'DataTable',
  'DataTableColumn',
  'DataTableColumnCellWrap',
  'DataTableColumnContentMaxWidth',
  'DataTableColumnPriority',
  'DataTableFilter',
  'DataTablePagination',
  'DataTableProps',
  'DataTableSort',
  'DataTableSortDirection',
  'DisplayTimeZoneProvider',
  'DisplayTimeZoneProviderProps',
  'EMAIL_ADDRESS_MAX_BYTES',
  'EmptyState',
  'EmptyStateHeadingLevel',
  'EmptyStateNoHeading',
  'EmptyStateProps',
  'EmptyStateVariant',
  'FORM_MAX_WIDTH',
  'FileUploader',
  'FileUploaderProps',
  'FileUploaderRow',
  'FileUploaderRowStatus',
  'FormField',
  'FormFieldProps',
  'FormFieldRenderState',
  'FormLayout',
  'FormLayoutColumns',
  'FormLayoutProps',
  'HumanVerification',
  'HumanVerificationGate',
  'HumanVerificationProps',
  'HumanVerificationStatus',
  'INVALID_ERROR_KEY',
  'InlineError',
  'InlineErrorProps',
  'InlineSuccess',
  'InlineSuccessProps',
  'LONG_TEXT_COLUMN_MAX_WIDTH',
  'ListSkeleton',
  'ListSkeletonProps',
  'OneTimeValuePanel',
  'OneTimeValuePanelDetail',
  'OneTimeValuePanelProps',
  'PRIMARY_COLUMN_MAX_WIDTH',
  'PageHeader',
  'PageRefresh',
  'PageRefreshRead',
  'PageRefreshReadHandle',
  'PageRefreshSettled',
  'PageHeaderBreadcrumb',
  'PageHeaderProps',
  'PageLayout',
  'PageLayoutProps',
  'PhoneCountryDefaultProvider',
  'PhoneCountryDefaultProviderProps',
  'PhoneNumberField',
  'PhoneNumberFieldProps',
  'PhoneNumberParts',
  'PhoneNumberValidation',
  'REQUIRED_ERROR_KEY',
  'RefreshButton',
  'RefreshButtonProps',
  'SectionCard',
  'SectionCardHeadingLevel',
  'SectionCardProps',
  'SectionSurface',
  'SectionSurfaceContext',
  'SectionSurfaceProvider',
  'SectionSurfaceProviderProps',
  'SidePanel',
  'SidePanelProps',
  'SidePanelSize',
  'TotpEnrollmentPanel',
  'TotpEnrollmentPanelProps',
  'TotpEnrollmentParameter',
  'TotpEnrollmentSecret',
  'TotpQrCode',
  'TotpQrCodeProps',
  'UI_KIT_NAMESPACE',
  'WriteFeedback',
  'WriteFeedbackPageCoordination',
  'WriteFeedbackPageProvider',
  'WriteFeedbackRegion',
  'WriteFeedbackRegionProps',
  'WriteOutcome',
  'createAppTheme',
  'defaultPhoneCountry',
  'errorCodeOf',
  'formatNationalPhoneNumber',
  'httpStatusOf',
  'retryAfterSecondsOf',
  'isPhoneCountry',
  'nationalNumberDigits',
  'pageRefreshRead',
  'parseEmailAddress',
  'parseInternationalPhoneNumber',
  'phoneCountries',
  'phoneCountryCallingCode',
  'phoneCountryName',
  'primaryColumnMaxWidthSx',
  'toE164',
  'touchTargetSx',
  'uiKitResources',
  'useAppThemeMode',
  'useAppThemePreset',
  'useDisplayTimeZone',
  'usePageRefresh',
  'usePhoneCountryDefault',
  'useWriteFeedback',
  'useWriteFeedbackPageCoordination',
  'validatePhoneNumber',
  'visuallyHiddenSx',
  'zodFormResolver',
] as const

describe('the entry export surface', () => {
  it('re-exports every public name its source modules declare', () => {
    const { program, entryFile, modulePaths } = createEntryProgram()
    expect(modulePaths.length).toBeGreaterThan(0)
    const entryNames = new Set(exportedNames(program, entryFile))
    const missingAcrossModules: string[] = []
    for (const modulePath of modulePaths) {
      const moduleFile = program.getSourceFile(modulePath)
      if (moduleFile === undefined) {
        continue
      }
      for (const name of exportedNames(program, moduleFile)) {
        if (!entryNames.has(name)) {
          missingAcrossModules.push(
            `${relative(srcDir, modulePath)}: ${name}`,
          )
        }
      }
    }
    expect(
      missingAcrossModules,
      'names a consumer cannot import from the package entry:',
    ).toEqual([])
    // Whole-tree type-checking under coverage instrumentation outruns vitest's 5s default.
  }, 30000)

  it('exports exactly the fixed public list, types included', () => {
    const { program, entryFile } = createEntryProgram()
    expect(exportedNames(program, entryFile).sort()).toEqual(
      [...PUBLIC_EXPORTS].sort(),
    )
  }, 30000)

  it('declares module augmentations in modules the entry re-exports from', () => {
    const { program, modulePaths } = createEntryProgram()
    const exposed = new Set(modulePaths.map((path) => resolve(path)))
    const offenders: string[] = []
    for (const file of program.getSourceFiles()) {
      if (file.isDeclarationFile || !file.fileName.startsWith(`${srcDir}/`)) {
        continue
      }
      const augments = file.statements.some(
        (statement) =>
          ts.isModuleDeclaration(statement) &&
          ts.isStringLiteral(statement.name),
      )
      if (augments && !exposed.has(resolve(file.fileName))) {
        offenders.push(relative(srcDir, file.fileName))
      }
    }
    expect(
      offenders,
      'augmentations a consumer of the emitted declarations cannot see:',
    ).toEqual([])
  }, 30000)
})
