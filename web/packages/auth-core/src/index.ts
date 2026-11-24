/**
 * @speed/auth-core -- the browser session lifecycle as a state machine over the
 * generated authn flow surface. The session is cookie- or token-carried; the
 * tenant-scoped query key, step-up gate and passkey subpath also live here.
 */

export {
  createAuthSession,
  isOperationSuperseded,
  OperationSupersededError,
} from './session.js'
export {
  attachSession,
  featurePresence,
  TenantProvider,
  useAuthState,
  useCurrentTenant,
  useFeature,
  useFeatureState,
  usePermission,
} from './hooks.js'
export {
  evictTenantQueries,
  tenantQueryKey,
  useTenantQueryKey,
} from './query-key.js'
export type { TenantQueryKey } from './query-key.js'
export { useStepUp } from './step-up.js'
export type {
  AuthnFlowAnswer,
  StepUpPhase,
  StepUpState,
} from './step-up.js'
export { useStepUpGate, stepUpReasonOf } from './step-up-gate.js'
export type { StepUpGate, StepUpReason } from './step-up-gate.js'
export {
  humanGateNeedsProof,
  humanSurfaceGate,
  parseCaptchaChallenge,
  powProofFromChallenge,
  requestPowProof,
  useHumanVerification,
} from './human.js'
export type {
  HumanCaptchaChallenge,
  HumanCaptchaForm,
  HumanScope,
  HumanVerification,
  HumanVerificationStatus,
  UseHumanVerificationOptions,
} from './human.js'
export { POW_HASH, solvePowChallenge } from './pow.js'
export { isTenantAdmissionRefusal } from './tenant-refusal.js'
export type {
  AuthDomain,
  AuthPermissionSets,
  AuthSession,
  AuthSessionListener,
  AuthSnapshot,
  AuthnFactorOption,
  AuthnField,
  AuthnFlowState,
  AuthnHumanGate,
  AuthnHumanSurfaceGate,
  AuthnIdentifier,
  AuthnLink,
  AuthnLoginRegisterBody,
  AuthnLoginRegistration,
  AuthnMethodDescriptor,
  AuthnMethodList,
  AuthnPrincipal,
  AuthnSessionView,
  AuthnSsoProvider,
  AuthnStep,
  FeatureState,
  PasswordResetChannel,
} from './session.js'
