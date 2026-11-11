import path from "node:path";
export const repository = path.resolve(import.meta.dirname, "../..");
export const runtime = path.join(repository, ".runtime");
export const dev = path.join(runtime, "dev");
export const tests = path.join(runtime, "tests");
export const cache = path.join(runtime, "cache");
export const bin = path.join(runtime, "bin");
