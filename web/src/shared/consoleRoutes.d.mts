export type MaintenanceSection =
  "tasks" | "trash" | "storage" | "health" | "recovery";
export type SettingsSection =
  "overview" | "enhancements" | "metadata" | "security";
export function maintenanceSection(search: string): MaintenanceSection;
export function settingsSection(search: string): SettingsSection;
export function taskID(path: string, search: string): string;
export function taskURL(id?: string, search?: string): string;
export function sectionURL(
  path: string,
  search: string,
  section: string,
): string;
export function consoleURL(value: string, origin?: string): string;
