/** One specifier -> source-file entry of the workspace package map. */
export interface SpeedPackageEntry {
  /** The @speed/* import specifier (subpaths included). */
  readonly specifier: string
  /** The src file it resolves to, workspace-root-relative. */
  readonly entry: string
}

/** The workspace's @speed specifiers and their src files, in resolution order. */
export declare const SPEED_PACKAGE_MAP: readonly SpeedPackageEntry[]

/** The map as vite/vitest `resolve.alias` entries with absolute replacements. */
export declare function speedAliases(
  map?: readonly SpeedPackageEntry[],
): Array<{ find: string; replacement: string }>

/** The map as a tsconfig `paths` object relative to the consumer's directory. */
export declare function speedTsconfigPaths(
  consumerDir: string,
  map?: readonly SpeedPackageEntry[],
): Record<string, string[]>
