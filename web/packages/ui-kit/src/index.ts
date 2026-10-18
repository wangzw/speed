/**
 * Public entry of @speed/ui-kit.
 *
 * Two kinds of things ship here: the theme factory (createAppTheme,
 * AppThemeProvider) that turns @speed/tokens into a MUI v9 theme, and the
 * fully controlled components (PageHeader, EmptyState, ConfirmDialog,
 * SidePanel, FormField, FormLayout, DataTable, FileUploader, InlineError,
 * InlineSuccess, WriteFeedbackRegion, ListSkeleton, AsyncSection) with
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
 * Everything else stays internal: helpers shared between components live
 * in src/internal/ and are deliberately not exported.
 */

export { UI_KIT_NAMESPACE, uiKitResources } from './resources.js'
export {
  createAppTheme,
  type AppTheme,
  type AppThemeMode,
} from './theme/createAppTheme.js'
export {
  AppThemeProvider,
  useAppThemeMode,
  type AppThemeModeControl,
  type AppThemeProviderProps,
} from './theme/AppThemeProvider.js'
export {
  PageHeader,
  type PageHeaderBreadcrumb,
  type PageHeaderProps,
} from './components/PageHeader.js'
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
  FormLayout,
  type FormLayoutColumns,
  type FormLayoutProps,
} from './components/FormLayout.js'
export {
  zodFormResolver,
  INVALID_ERROR_KEY,
} from './zod-form-resolver.js'
export {
  DataTable,
  PRIMARY_COLUMN_MAX_WIDTH,
  primaryColumnMaxWidthSx,
  type DataTableColumn,
  type DataTableColumnCellWrap,
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
  type InlineErrorProps,
} from './components/InlineError.js'
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
  ListSkeleton,
  type ListSkeletonProps,
} from './components/ListSkeleton.js'
export {
  AsyncSection,
  type AsyncSectionEmptyState,
  type AsyncSectionErrorState,
  type AsyncSectionProps,
  type AsyncSectionStaleState,
} from './components/AsyncSection.js'
export { visuallyHiddenSx } from './visually-hidden.js'
export { touchTargetSx } from './touch-target.js'
