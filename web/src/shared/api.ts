import { pageIdentity } from "./scanFlow";
import type { Actor } from "./actorAccess.mjs";
import {
  actorAccess,
  actorEpoch,
  registerActorAbort,
  setActor,
  legacyActor,
  clientAccess,
  rememberResources,
} from "./writeState";
import { healthWriteAllowed, updateHealth } from "./writeState";
export type Session = {
  actor?: Actor;
  account_id: string;
  csrf_token: string;
  expires_at: string;
};
export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public requestId = "",
    public details?: unknown,
  ) {
    super(message);
  }
}
let csrf = "";
export { subscribeWrites, writesPending } from "./writeState";
import { isBusinessWrite, trackWrite } from "./writeState";
export async function request<T>(
  path: string,
  method = "GET",
  body?: unknown,
  quiet = false,
  signal?: AbortSignal,
): Promise<T> {
  return trackWrite(
    () => performRequest<T>(path, method, body, quiet, signal),
    isBusinessWrite(path, method),
  );
}
async function performRequest<T>(
  path: string,
  method = "GET",
  body?: unknown,
  quiet = false,
  signal?: AbortSignal,
): Promise<T> {
  assertHealthAccess(path, method);
  assertActorAccess(path, method, body);
  const epoch = actorEpoch();
  const own = new AbortController();
  const unregister = registerActorAbort(() => own.abort());
  const forward = () => own.abort();
  signal?.addEventListener("abort", forward, { once: true });
  if (signal?.aborted) own.abort();
  let response: Response;
  try {
    response = await fetch(`/api/v1${path}`, {
      method,
      credentials: "same-origin",
      signal: own.signal,
      headers: {
        ...(body === undefined ? {} : { "Content-Type": "application/json" }),
        ...(csrf ? { "X-CSRF-Token": csrf } : {}),
      },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") {
      unregister();
      signal?.removeEventListener("abort", forward);
      throw e;
    }
    unregister();
    signal?.removeEventListener("abort", forward);
    throw new ApiError(0, "network_error", "无法连接服务，请检查网络后重试");
  }
  try {
    const data = await decodeResponse<T>(response, quiet, epoch);
    if (epoch !== actorEpoch())
      throw new DOMException("身份或权限已变化", "AbortError");
    rememberResources(data);
    return data;
  } finally {
    unregister();
    signal?.removeEventListener("abort", forward);
  }
}
async function decodeResponse<T>(
  response: Response,
  quiet = false,
  epoch = actorEpoch(),
): Promise<T> {
  assertActorEpoch(epoch);
  if (!response.ok) {
    const data = await response.json().catch(() => null);
    assertActorEpoch(epoch);
    const error = new ApiError(
      response.status,
      data?.error?.code ?? "request_failed",
      data?.error?.code === "shared_source_requires_admin"
        ? "此来源被其他书库共同使用，需要管理员处理。候选与草稿已保留。"
        : (data?.error?.message ?? "连接失败，请稍后重试"),
      data?.error?.request_id ?? "",
      data?.current_state
        ? { current_state: data.current_state }
        : data?.error?.details,
    );
    if (
      error.code === "maintenance_active" &&
      actorAccess().actor?.role !== "user"
    ) {
      updateHealth({ supported: true, error: true });
      window.dispatchEvent(new Event("health-state-refresh"));
    }
    if (["forbidden", "password_change_required"].includes(error.code))
      window.dispatchEvent(new Event("actor-session-refresh"));
    if (!quiet && response.status === 401 && error.code === "unauthenticated")
      window.dispatchEvent(new Event("session-expired"));
    throw error;
  }
  const data = response.status === 204 ? undefined : await response.json();
  assertActorEpoch(epoch);
  return data as T;
}
function assertActorEpoch(epoch: number) {
  if (epoch !== actorEpoch())
    throw new DOMException("身份或权限已变化", "AbortError");
}
function assertHealthAccess(path: string, method: string) {
  if (!healthWriteAllowed(path, method))
    throw new ApiError(
      503,
      "maintenance_active",
      "全局维护保护中，草稿保留；请查看当前维护与恢复状态",
    );
}
/** Raw role writes share authentication, errors and the global submission lock. */
export async function requestBinary<T>(
  path: string,
  method: "PUT" | "DELETE",
  revision: number,
  body?: Blob,
  contentType?: string,
): Promise<T> {
  if (!Number.isSafeInteger(revision) || revision < 0)
    throw new Error("缺少可提交的图片版本，请重新读取");
  return trackWrite(async () => {
    assertHealthAccess(path, method);
    assertActorAccess(path, method);
    const epoch = actorEpoch();
    const controller = new AbortController();
    const unregister = registerActorAbort(() => controller.abort());
    try {
      let response: Response;
      try {
        response = await fetch(`/api/v1${path}`, {
          method,
          credentials: "same-origin",
          signal: controller.signal,
          headers: {
            "X-Expected-Revision": String(revision),
            ...(csrf ? { "X-CSRF-Token": csrf } : {}),
            ...(body && contentType ? { "Content-Type": contentType } : {}),
          },
          ...(body === undefined ? {} : { body }),
        });
      } catch (error) {
        if (error instanceof DOMException && error.name === "AbortError")
          throw error;
        throw new ApiError(
          0,
          "network_error",
          "无法连接服务，请检查网络后重试",
        );
      }
      const data = await decodeResponse<T>(response, false, epoch);
      if (epoch !== actorEpoch())
        throw new DOMException("身份或权限已变化", "AbortError");
      return data;
    } finally {
      unregister();
    }
  });
}
function assertActorAccess(path: string, method: string, body?: unknown) {
  if (!clientAccess(path, method, body))
    throw new ApiError(
      403,
      "forbidden",
      "当前账号没有此操作权限，请核对书库授权。",
    );
}
async function accept(promise: Promise<Session>) {
  const session = await promise;
  setActor(session.actor || legacyActor(session.account_id));
  csrf = session.csrf_token;
  return session;
}
export const api = {
  status: () => request<{ initialized: boolean }>("/auth/status"),
  session: () =>
    accept(request<Session>("/auth/session", "GET", undefined, true)),
  setup: (setup_token: string, password: string) =>
    accept(request<Session>("/auth/setup", "POST", { setup_token, password })),
  login: (password: string, username = "admin") =>
    accept(
      request<Session>("/auth/login", "POST", {
        password,
        ...(username && username !== "admin" ? { username } : {}),
      }),
    ),
  logout: async () => {
    await request<void>("/auth/logout", "POST");
    csrf = "";
    setActor(null);
  },
  password: async (current_password: string, new_password: string) => {
    await request("/auth/password", "PUT", { current_password, new_password });
    csrf = "";
  },
};

export type Root = { root_key: string; display_name: string };
export type Library = {
  edition_count?: number;
  work_count?: number;
  active_import?: { id: string; stage: string; state: string } | null;
  id: string;
  root_key: string;
  display_name: string;
  legacy_zip_encoding: string;
  scan_interval_minutes: number | null;
  revision: number;
  last_complete_scan_id: string | null;
  error_code: string;
  availability?: string;
  disabled?: boolean;
  trashed_at?: number | null;
};
export type Book = {
  reading_state?: import("./readingFlow").ReadingState;
  title_original?: string;
  id: string;
  library_id: string;
  work_id: string;
  organization_revision: number;
  file: {
    edition_id: string;
    encoding_override?: string | null;
    parsing_revision?: number;
    parser_version?: string;
    directory_digest?: string;
    effective_encoding?: string;
    relative_path: string;
    size: number;
    mtime_ns: number;
    content_revision: number;
    representative_sha1: string | null;
    representative_algorithm: string;
    page_count: number;
    file_state: string;
    error_code: string;
    indexed_at: number;
  };
  relative_path: string;
  title: string;
  size: number;
  mtime_ns: number;
  content_revision: number;
  representative_sha1: string | null;
  representative_algorithm?: string;
  page_count: number;
  file_state: string;
  error_code: string;
  indexed_at: number;
  revision: number;
  cover_revision?: number;
  trashed_at?: number | null;
  library_disabled?: boolean;
  metadata?: import("./metadata").MetadataDetails;
  user_state?: import("./organization").UserState;
  organization?: import("./organization").OrgContext;
};
export type Job = {
  scan_completed?: number;
  scan_partial?: number;
  scan_blocked?: number;
  scan_warning_count?: number;
  actor_id?: string;
  actor_acl_revision?: number;
  mode?: "edition" | "library";
  type:
    | "scan"
    | "metadata"
    | "tag_translation"
    | "maintenance"
    | "cover_rebuild"
    | "metadata_preview"
    | "metadata_search"
    | "work_recommendation"
    | "series_recommendation";
  parent_job_id: string | null;
  id: string;
  library_id: string;
  state: string;
  stage: string;
  generation: number;
  total: number | null;
  done: number;
  failed: number;
  error_code: string;
  created_at: number;
  updated_at: number;
};
export type JobItem = {
  scan_result?: import("./scanFlow").ScanResult;
  preview_id?: string;
  candidates: { url: string; title: string }[];
  attempt: number;
  content_revision: number;
  ordinal: number;
  relative_path: string;
  state: string;
  error_code: string;
  book_id: string | null;
};
export type Page = {
  page_index: number;
  display_name: string;
  media_type: string;
};
export type Listing<T> = {
  items: T[];
  total: number;
  page: number;
  page_size: number;
};
export type LibraryInput = {
  root_key: string;
  display_name: string;
  legacy_zip_encoding: string;
  scan_interval_minutes: number | null;
};
export const catalog = {
  roots: () => request<{ items: Root[] }>("/library-roots"),
  libraries: () => request<{ items: Library[] }>("/libraries"),
  create: (input: LibraryInput) =>
    request<Library>("/libraries", "POST", input),
  update: (id: string, input: LibraryInput & { expected_revision: number }) =>
    request<Library>(`/libraries/${id}`, "PATCH", input),
  scan: (library: Library) =>
    request<{ job_id: string }>(`/libraries/${library.id}/scans`, "POST", {
      expected_revision: library.revision,
    }),
  jobs: () => request<{ items: Job[] }>("/jobs"),
  job: (id: string) => request<Job>(`/jobs/${id}`),
  items: (id: string, page = 1) =>
    request<Listing<JobItem>>(`/jobs/${id}/items?page=${page}&page_size=50`),
  cancel: (id: string) => request(`/jobs/${id}/cancel`, "POST"),
  books: (library: string, state: string, page: number) =>
    request<Listing<Book>>(
      `/books?${new URLSearchParams({ library_id: library, state, page: String(page), page_size: "24" })}`,
    ),
  book: (id: string) => request<Book>(`/editions/${id}`),
  pages: (book: Book, page = 1) =>
    request<Listing<Page>>(
      `/books/${book.id}/pages?${pageIdentity(book)}&page=${page}&page_size=50`,
    ),
  cover: (book: Book) =>
    `/api/v1/books/${book.id}/cover?content_revision=${book.content_revision}`,
};

export async function upload<T>(
  path: string,
  file: File,
  revision: number,
): Promise<T> {
  return trackWrite(() => performUpload<T>(path, file, revision));
}
async function performUpload<T>(
  path: string,
  file: File,
  revision: number,
): Promise<T> {
  assertHealthAccess(path, "POST");
  assertActorAccess(path, "POST");
  const epoch = actorEpoch();
  const controller = new AbortController();
  const unregister = registerActorAbort(() => controller.abort());
  try {
    const response = await fetch(`/api/v1${path}`, {
      method: "POST",
      credentials: "same-origin",
      signal: controller.signal,
      headers: {
        "Content-Type": file.type,
        "X-Expected-Revision": String(revision),
        "X-CSRF-Token": csrf,
      },
      body: file,
    });
    return await decodeResponse<T>(response, false, epoch);
  } finally {
    unregister();
  }
}

/** ZIP uses the File itself as the body; no archive-sized JS buffer or Blob copy. */
export async function uploadInstance<T>(
  file: File,
  requestKey: string,
  signal: AbortSignal,
  progress: (sent: number, total: number) => void,
): Promise<T> {
  return trackWrite(
    () =>
      new Promise<T>((resolve, reject) => {
        try {
          assertHealthAccess("/maintenance/restores", "POST");
          assertActorAccess("/maintenance/restores", "POST");
        } catch (error) {
          reject(error);
          return;
        }
        const epoch = actorEpoch();
        const xhr = new XMLHttpRequest();
        const unbindActor = registerActorAbort(() => xhr.abort());
        xhr.open("POST", "/api/v1/maintenance/restores");
        xhr.withCredentials = true;
        xhr.setRequestHeader("Content-Type", "application/zip");
        xhr.setRequestHeader("Idempotency-Key", requestKey);
        if (csrf) xhr.setRequestHeader("X-CSRF-Token", csrf);
        xhr.upload.onprogress = (e) =>
          progress(e.loaded, e.lengthComputable ? e.total : file.size);
        const abort = () => xhr.abort();
        const clear = () => {
          signal.removeEventListener("abort", abort);
          unbindActor();
        };
        xhr.onload = () => {
          clear();
          void decodeResponse<T>(
            new Response(xhr.responseText, {
              status: xhr.status,
              headers: { "Content-Type": "application/json" },
            }),
            false,
            epoch,
          ).then(resolve, reject);
        };
        xhr.onerror = () => {
          clear();
          reject(
            new ApiError(
              0,
              "network_error",
              "上传结果不明，请核对历史，不会自动重传。",
            ),
          );
        };
        xhr.onabort = () => {
          clear();
          reject(new DOMException("上传已中断，请核对持久状态", "AbortError"));
        };
        signal.addEventListener("abort", abort, { once: true });
        if (signal.aborted) {
          clear();
          reject(new DOMException("Aborted", "AbortError"));
          return;
        }
        xhr.send(file);
      }),
  );
}
/** Probe only the first byte for actionable errors; the browser streams the actual download. */
export async function checkInstanceDownload(id: string) {
  const epoch = actorEpoch();
  const response = await fetch(
    `/api/v1/maintenance/backups/${encodeURIComponent(id)}/download`,
    { credentials: "same-origin", headers: { Range: "bytes=0-0" } },
  );
  if (!response.ok) await decodeResponse(response, false, epoch);
  assertActorEpoch(epoch);
  await response.body?.cancel();
}
