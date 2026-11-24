export interface VerificationProfile {
  readonly verification?: {
    readonly state?: string
  }
}

export function contactVerificationPending(
  profile: VerificationProfile,
): boolean {
  return profile.verification?.state === 'pending'
}
