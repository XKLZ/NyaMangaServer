import type { HealthState } from "./healthFlow";
type Access = {
  supported: boolean | null;
  state: HealthState | null;
  error: boolean;
  instanceSupported: boolean;
  instanceState: import("./instanceFlow").InstanceState | null;
  instanceError: boolean;
};
export function healthAccess(): Access;
export function subscribeHealth(fn: () => void): () => void;
export function updateHealth(value: Partial<Access>): void;
export function resetHealth(): void;
export function registerHealthJob(id: string): void;
export function maintenanceProtected(): boolean;
export function readOnlyPost(path: string, method: string): boolean;
export function healthControl(path: string, method: string): boolean;
export function healthWriteAllowed(path: string, method: string): boolean;
