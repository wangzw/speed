// pnpm finds its workspace by walking up from the directory it runs in, so the build runs at the workspace root and selects the host by name. `--fail-if-no-match` must stand before the command word, where pnpm reads it.
import { WEB_WORKSPACE_DIR } from './paths'

export const FRONT_END_PACKAGE = 'demo-web'

export const FRONT_END_BUILD_ARGS: readonly string[] = [
  '-C',
  WEB_WORKSPACE_DIR,
  '--filter',
  FRONT_END_PACKAGE,
  '--fail-if-no-match',
  'run',
  'build',
]
