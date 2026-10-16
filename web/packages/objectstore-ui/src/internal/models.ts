/**
 * The wire shapes the object page renders, aliased onto the generated
 * response models of @speed/api-sdk.
 *
 * The objectstore fragments declare their 2xx response schemas, so orval
 * emits the response models and this package does not own them: each name
 * below is an alias to the generated type of the operation the page
 * reads. The narrowing functions stay because they carry the one runtime
 * rule the generated surface cannot -- an absent body reads as an empty
 * object, so a 2xx whose body omitted a member reads as `undefined` (the
 * caller's error variant) rather than as a valid empty page. They are
 * shape assertions, not guards: nothing here throws, because this package
 * does not depend on @speed/api-client and cannot mint its client.protocol
 * ApiError.
 */

import type {
  ObjectstoreMetadataGetObject200,
  ObjectstoreMetadataListObjects200,
  ObjectstoreObject,
  ObjectstoreObjectState,
} from '@speed/api-sdk'

/** One stored object, the object view the routes answer with. */
export type StoredObject = ObjectstoreObject

/** One object's lifecycle state (pending / uploaded / completed / deleted). */
export type ObjectState = ObjectstoreObjectState

/** One cursor page of objects: the rows and the next page's cursor. */
export type ObjectPage = ObjectstoreMetadataListObjects200

/** One object's metadata, the body of GET /objects/{id}. */
export type ObjectDetail = ObjectstoreMetadataGetObject200

/** Narrows a list read to its shape; an absent body reads as empty. */
export function asObjectPage(raw: unknown): ObjectPage {
  return (typeof raw === 'object' && raw !== null ? raw : {}) as ObjectPage
}

/** Narrows a detail read to its shape; an absent body reads as empty. */
export function asObjectDetail(raw: unknown): ObjectDetail {
  return (typeof raw === 'object' && raw !== null ? raw : {}) as ObjectDetail
}
