
import { readFileSync } from 'node:fs'
import { dirname, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import ts from 'typescript'

const srcDir = resolve(dirname(fileURLToPath(import.meta.url)))

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
    true,
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

const PUBLIC_EXPORTS = [
  'ActionButton',
  'ActionButtonProps',
  'ActionButtonUnavailableExplanation',
  'ActionEmphasis',
  'ActionRow',
  'ActionRowProps',
  'ActionRowSize',
  'ActionTone',
  'ActivationGuardState',
  'AnnouncementRegion',
  'AnnouncementRegionProps',
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
  'HumanCaptchaChallenge',
  'HumanCaptchaForm',
  'HumanVerification',
  'HumanVerificationGate',
  'HumanVerificationGateSet',
  'HumanVerificationProps',
  'HumanVerificationScope',
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
  'UseFormatInstantOptions',
  'WriteFeedback',
  'WriteFeedbackPageCoordination',
  'WriteFeedbackPageProvider',
  'WriteFeedbackRegion',
  'WriteFeedbackRegionProps',
  'WriteOutcome',
  'createAppTheme',
  'defaultPhoneCountry',
  'errorCodeOf',
  'focusPageLanding',
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
  'useActivationGuard',
  'useAppThemeMode',
  'useAppThemePreset',
  'useDisplayTimeZone',
  'useFormatInstant',
  'usePageRefresh',
  'usePhoneCountryDefault',
  'useResolvedTimeZone',
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
  })

  it('exports exactly the fixed public list, types included', () => {
    const { program, entryFile } = createEntryProgram()
    expect(exportedNames(program, entryFile).sort()).toEqual(
      [...PUBLIC_EXPORTS].sort(),
    )
  })

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
  })
})
