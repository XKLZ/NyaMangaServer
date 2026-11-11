export type VersionLayout = "wide" | "drawer" | "mobile";
export function versionLayout(width: number): VersionLayout {
  return width >= 1240 ? "wide" : width >= 800 ? "drawer" : "mobile";
}
