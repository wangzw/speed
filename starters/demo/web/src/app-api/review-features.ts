// The review assembly publishes this feature; no other assembly does. The
// module that declares it is compiled only under the review build tag, so the
// baseline `gen --features` never sees it and the generated vocabulary
// (src/app-api/features.ts, which must not be edited) cannot carry it. This
// hand-written augmentation is what lets the tenant control name the gate in a
// typed position. Rendering is still decided at run time from the feature
// document, so a baseline binary serves no such name and shows nothing.
declare module '@speed/api-sdk/features' {
  interface FeatureNameRegistry {
    'review.notification.control': true
    'review.sms.control': true
  }
}

export {}
