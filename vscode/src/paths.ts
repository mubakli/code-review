import * as path from "node:path";

export function resolveFindingPath(repositoryRoot: string, file: string): string | undefined {
  if (path.isAbsolute(file)) {
    return undefined;
  }
  const root = path.resolve(repositoryRoot);
  const candidate = path.resolve(root, file);
  const relative = path.relative(root, candidate);
  if (relative === "" || relative === ".." || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative)) {
    return undefined;
  }
  return candidate;
}
