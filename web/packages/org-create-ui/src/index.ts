/**
 * Public entry of @speed/org-create-ui: the shared organization-creation
 * surface.
 */

export {
  ORG_CREATE_UI_NAMESPACE,
  orgCreateUiResources,
} from './resources.js'

export {
  DESCRIPTION_MAX_RUNES,
  DISPLAY_NAME_MAX_RUNES,
  EMPTY_ORGANIZATION_CREATE_VALUES,
  RESERVED_NAMES,
  RESERVED_NAME_PREFIX,
  WEBSITE_MAX_BYTES,
  nameViolation,
  isNameShape,
  isReservedName,
  normalizeOrganizationCreateValues,
  validateOrganizationCreateValues,
  type CreatedOrganization,
  type OrganizationCreateField,
  type OrganizationCreateMode,
  type OrganizationCreateSubmission,
  type OrganizationCreateValues,
  type OrganizationCreateViolation,
  type OrganizationCreateViolations,
} from './validation.js'

export {
  ORGANIZATION_CREATE_ERROR_CODES,
  organizationCreateFailure,
  useOrganizationCreateErrorText,
  type OrganizationCreateFailure,
} from './error-text.js'

export {
  OrganizationCreateForm,
  type OrganizationCreateFormProps,
} from './OrganizationCreateForm.js'

export {
  OrganizationCreatePage,
  type OrganizationCreatePageProps,
} from './OrganizationCreatePage.js'
