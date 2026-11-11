import { request } from "./api";
import type {
  GalleryConnection,
  GalleryConnections,
  GalleryConnectionSecret,
} from "./galleryHighlightFlow";
const path = "/settings/gallery-highlight/connections";
export const galleryHighlight = {
  path: (page: number) => `${path}?page=${page}&page_size=10`,
  list: (page: number, signal?: AbortSignal) =>
    request<GalleryConnections>(
      `${path}?page=${page}&page_size=10`,
      "GET",
      undefined,
      false,
      signal,
    ),
  create: () => request<GalleryConnectionSecret>(path, "POST", {}),
  revoke: (value: GalleryConnection) => {
    if (
      !value.id ||
      !Number.isSafeInteger(value.revision) ||
      value.revision < 1 ||
      value.revision >= Number.MAX_SAFE_INTEGER
    )
      throw Error("缺少可提交的连接版本，请重新读取");
    return request<GalleryConnection>(
      `${path}/${encodeURIComponent(value.id)}`,
      "DELETE",
      { expected_revision: value.revision },
    );
  },
};
