import { request, requestBinary } from "./api";
export type ImageRole = "foreground" | "background";
export type TagImage = {
  role: ImageRole;
  revision: number;
  asset_id: string | null;
  width: number;
  height: number;
  format: "" | "png" | "jpeg" | "webp" | "bmp";
  processing_version: number;
};
export type TagImages = {
  tag_id: string;
  foreground: TagImage;
  background: TagImage;
};
const path = (id: string) => `/tag-catalog/${encodeURIComponent(id)}/images`;
export const tagImages = {
  state: (id: string, signal?: AbortSignal) =>
    request<TagImages>(path(id), "GET", undefined, false, signal),
  save: (
    id: string,
    role: ImageRole,
    expected: number,
    file: File,
    media: string,
  ) =>
    requestBinary<TagImage>(
      `${path(id)}/${role}`,
      "PUT",
      expected,
      file,
      media,
    ),
  remove: (id: string, role: ImageRole, expected: number) =>
    requestBinary<TagImage>(`${path(id)}/${role}`, "DELETE", expected),
};
