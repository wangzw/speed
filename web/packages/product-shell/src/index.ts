/**
 * Public entry of @speed/product-shell: the tenant-facing assembly shell,
 * a three-branch view machine over an auth-core session.
 */

export {
  ProductShell,
  type ProductShellProps,
  type ProductShellSessionEndedActions,
  type ProductShellSessionEndedSlot,
} from './components/ProductShell.js'
export {
  PRODUCT_SHELL_NAMESPACE,
  productShellResources,
} from './resources.js'
