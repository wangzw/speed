/**
 * @speed/org-create-ui -- the shared organization-creation surface.
 *
 * The public interface is this entry: the shared form and its controlled
 * values, the client-side validation the server remains authoritative
 * over, the self-service creation page, the error-text resolver and the
 * package's bilingual namespace resources. The platform console composes
 * the form inside its own registration page and the demo host composes
 * the page over its org.create operation; neither copies the fields or
 * the rules.
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
