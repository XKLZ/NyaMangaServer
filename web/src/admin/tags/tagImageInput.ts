import type { TagImage } from "../../shared/tagImages";
export const tagImageLimit = 10 * 1024 * 1024;
export const imageRoles = ["foreground", "background"] as const;
export const imageRoleNames = {
  foreground: "主体图",
  background: "背景图",
} as const;
/** Inspect bounded bytes, never trust File.type or a filename extension. The
 * server validates the full decoder, dimensions, memory budget and MIME again. */
export async function tagImageMedia(file: Blob): Promise<string> {
  if (!file.size || file.size > tagImageLimit)
    throw new Error("请选择不超过10 MiB的JPEG、PNG、WebP或BMP图片");
  const b = new Uint8Array(await file.slice(0, 16).arrayBuffer());
  if ([137, 80, 78, 71, 13, 10, 26, 10].every((v, i) => b[i] === v))
    return "image/png";
  if (b[0] === 255 && b[1] === 216 && b[2] === 255) return "image/jpeg";
  const text = new TextDecoder("ascii").decode(b);
  if (text.startsWith("RIFF") && text.slice(8, 12) === "WEBP")
    return "image/webp";
  if (b[0] === 66 && b[1] === 77) return "image/bmp";
  throw new Error(
    "图片格式不支持，请选择JPEG、PNG、WebP或BMP；不支持GIF、SVG或AVIF",
  );
}
export function tagImageContent(id: string, image?: TagImage | null) {
  return image?.asset_id &&
    Number.isSafeInteger(image.revision) &&
    image.revision > 0
    ? `/api/v1/tag-catalog/${encodeURIComponent(id)}/images/${image.role}/content?revision=${image.revision}`
    : "";
}
/** Bounded, same-device data URL: CSP permits data images, without blob URLs or
 * external services. Original file is retained separately for raw-byte upload. */
export function tagImagePreview(
  file: File,
  media: string,
  signal: AbortSignal,
): Promise<string> {
  if (file.size > tagImageLimit)
    return Promise.reject(new Error("图片超过10 MiB"));
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    const abort = () => {
      reader.abort();
      reject(new DOMException("Aborted", "AbortError"));
    };
    if (signal.aborted) {
      abort();
      return;
    }
    signal.addEventListener("abort", abort, { once: true });
    const done = () => signal.removeEventListener("abort", abort);
    reader.onload = () => {
      done();
      resolve(String(reader.result));
    };
    reader.onerror = () => {
      done();
      reject(new Error("无法读取图片，请重新选择"));
    };
    reader.onabort = () => {
      done();
      reject(new DOMException("Aborted", "AbortError"));
    };
    reader.readAsDataURL(new Blob([file], { type: media }));
  });
}

/** Only a real version conflict requires adopting another image revision. */
export function imageFailureKind(
  error: unknown,
): "conflict" | "retryable" | "invalid" {
  const status =
    error && typeof error === "object" && "status" in error
      ? error.status
      : undefined;
  return status === 409
    ? "conflict"
    : status === 0 ||
        (typeof status === "number" && status >= 500 && status < 600)
      ? "retryable"
      : "invalid";
}
