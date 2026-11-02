/**
 * Public entry of @speed/ui-kit.
 *
 * Two kinds of things ship here: the theme factory (createAppTheme,
 * AppThemeProvider) that turns @speed/tokens into a MUI v9 theme, and the
 * fully controlled components (PageLayout, PageHeader, SectionCard, EmptyState,
 * ConfirmDialog, SidePanel, FormField, FormLayout, PhoneNumberField,
 * DataTable, FileUploader, InlineError, InlineSuccess,
 * WriteFeedbackRegion, ListSkeleton, AsyncSection, HumanVerification) with
 * their ui-kit-namespace translations (UI_KIT_NAMESPACE + uiKitResources)
 * for the host to register —
 * FileUploader renders the queue the host owns as `rows` props while
 * uploads run in the host's own transport code. The form family's zod
 * bridge (zodFormResolver) ships beside the components so a host drives
 * RHF validation from one zod schema. The cross-surface state
 * primitives ship beside them: InlineError + errorCodeOf (the
 * whole-attempt failure banner), InlineSuccess (its success twin, the
 * mount-empty-then-fill role="status" notice), useWriteFeedback +
 * WriteFeedbackRegion (one feedback scope's slot and the region that
 * renders it), ListSkeleton and AsyncSection (the pending/error/empty/
 * content branches of an async read, with `staleState` marking a failed
 * refresh that kept its data) and
 * visuallyHiddenSx (the clip recipe for hidden live regions and file
 * inputs) and touchTargetSx (the coarse-pointer 44px floor for an
 * interactive target the theme's own control overrides cannot reach).
 * The refresh pair (RefreshButton + usePageRefresh) re-reads a surface's
 * own reads through the handles the host passes; the clipboard and
 * hand-off pair (CopyButton + OneTimeValuePanel) covers a value handed
 * out once; the TOTP enrolment pair (TotpQrCode + TotpEnrollmentPanel)
 * draws the symbol, the manual secret and the code field from values the
 * caller parsed and translated, reading no bundle of its own; the two
 * display contexts (DisplayTimeZoneProvider,
 * PhoneCountryDefaultProvider) carry a host preference read by surfaces
 * that fall back to their own defaults when it is absent; and
 * SectionSurfaceProvider tells the sections below it whether the
 * enclosing surface already supplies the card, so a nested section can
 * contribute its content alone; and ActionRow + ActionButton are the one
 * right-aligned action row, with the button's weight, colour and size
 * derived from the row's emphasis, tone and size rather than from a
 * call-site variant.
 * Everything else stays internal: helpers shared between components live
 * in src/internal/ and are deliberately not exported.
 */

export { UI_KIT_NAMESPACE, uiKitResources } from './resources.js'
export {
  createAppTheme,
  type AppTheme,
  type AppThemeMode,
  type AppThemeModePreference,
} from './theme/createAppTheme.js'
export {
  AppThemeProvider,
  useAppThemeMode,
  useAppThemePreset,
  type AppThemeModeControl,
  type AppThemePresetControl,
  type AppThemeProviderProps,
} from './theme/AppThemeProvider.js'
export {
  PageHeader,
  type PageHeaderBreadcrumb,
  type PageHeaderProps,
} from './components/PageHeader.js'
export {
  PageLayout,
  type PageLayoutProps,
} from './components/PageLayout.js'
export {
  SectionCard,
  SectionSurfaceContext,
  SectionSurfaceProvider,
  type SectionCardHeadingLevel,
  type SectionCardProps,
  type SectionSurface,
  type SectionSurfaceProviderProps,
} from './components/SectionCard.js'
export {
  ActionButton,
  ActionRow,
  type ActionButtonProps,
  type ActionEmphasis,
  type ActionRowProps,
  type ActionRowSize,
  type ActionTone,
} from './components/ActionRow.js'
export {
  EmptyState,
  type EmptyStateHeadingLevel,
  type EmptyStateNoHeading,
  type EmptyStateProps,
  type EmptyStateVariant,
} from './components/EmptyState.js'
export {
  ConfirmDialog,
  CONFIRM_ARM_LOCKOUT_MS,
  type ConfirmDialogProps,
  type ConfirmDialogVariant,
} from './components/ConfirmDialog.js'
export {
  SidePanel,
  type SidePanelProps,
  type SidePanelSize,
} from './components/SidePanel.js'
export {
  FormField,
  REQUIRED_ERROR_KEY,
  type FormFieldProps,
  type FormFieldRenderState,
} from './components/FormField.js'
export {
  HumanVerification,
  type HumanVerificationGate,
  type HumanVerificationProps,
  type HumanVerificationStatus,
} from './components/HumanVerification.js'
export {
  FORM_MAX_WIDTH,
  FormLayout,
  type FormLayoutColumns,
  type FormLayoutProps,
} from './components/FormLayout.js'
export {
  PhoneNumberField,
  type PhoneNumberFieldProps,
} from './components/PhoneNumberField.js'
export {
  EMAIL_ADDRESS_MAX_BYTES,
  parseEmailAddress,
} from './email-address.js'
export {
  defaultPhoneCountry,
  formatNationalPhoneNumber,
  isPhoneCountry,
  nationalNumberDigits,
  parseInternationalPhoneNumber,
  phoneCountries,
  phoneCountryCallingCode,
  phoneCountryName,
  toE164,
  validatePhoneNumber,
  type PhoneNumberParts,
  type PhoneNumberValidation,
} from './phone-number.js'
export {
  zodFormResolver,
  INVALID_ERROR_KEY,
} from './zod-form-resolver.js'
export {
  DataTable,
  LONG_TEXT_COLUMN_MAX_WIDTH,
  PRIMARY_COLUMN_MAX_WIDTH,
  primaryColumnMaxWidthSx,
  type DataTableColumn,
  type DataTableColumnCellWrap,
  type DataTableColumnContentMaxWidth,
  type DataTableColumnPriority,
  type DataTableFilter,
  type DataTablePagination,
  type DataTableProps,
  type DataTableSort,
  type DataTableSortDirection,
} from './components/DataTable.js'
export {
  FileUploader,
  type FileUploaderProps,
  type FileUploaderRow,
  type FileUploaderRowStatus,
} from './components/FileUploader.js'
export {
  InlineError,
  errorCodeOf,
  httpStatusOf,
  retryAfterSecondsOf,
  type InlineErrorProps,
} from './components/InlineError.js'
export {
  CopyButton,
  type CopyButtonProps,
} from './components/CopyButton.js'
export {
  TotpQrCode,
  type TotpQrCodeProps,
} from './components/TotpQrCode.js'
export {
  TotpEnrollmentPanel,
  type TotpEnrollmentPanelProps,
  type TotpEnrollmentParameter,
  type TotpEnrollmentSecret,
} from './components/TotpEnrollmentPanel.js'
export {
  OneTimeValuePanel,
  type OneTimeValuePanelDetail,
  type OneTimeValuePanelProps,
} from './components/OneTimeValuePanel.js'
export {
  RefreshButton,
  type RefreshButtonProps,
} from './components/RefreshButton.js'
export {
  InlineSuccess,
  type InlineSuccessProps,
} from './components/InlineSuccess.js'
export {
  WriteFeedbackRegion,
  type WriteFeedbackRegionProps,
} from './components/WriteFeedbackRegion.js'
export {
  useWriteFeedback,
  type WriteFeedback,
  type WriteOutcome,
} from './write-feedback.js'
export {
  WriteFeedbackPageProvider,
  useWriteFeedbackPageCoordination,
  type WriteFeedbackPageCoordination,
} from './write-feedback-page.js'
export {
  ListSkeleton,
  type ListSkeletonProps,
} from './components/ListSkeleton.js'
export {
  AsyncSection,
  type AsyncSectionEmptyState,
  type AsyncSectionErrorState,
  type AsyncSectionProps,
  type AsyncSectionRefusedState,
  type AsyncSectionStaleState,
} from './components/AsyncSection.js'
export {
  usePageRefresh,
  pageRefreshRead,
  type PageRefresh,
  type PageRefreshRead,
  type PageRefreshReadHandle,
  type PageRefreshSettled,
} from './use-page-refresh.js'
export {
  DisplayTimeZoneProvider,
  useDisplayTimeZone,
  type DisplayTimeZoneProviderProps,
} from './display-time-zone.js'
export {
  PhoneCountryDefaultProvider,
  usePhoneCountryDefault,
  type PhoneCountryDefaultProviderProps,
} from './phone-country-default.js'
export { visuallyHiddenSx } from './visually-hidden.js'
export { touchTargetSx } from './touch-target.js'
