/** §7.1: a card uses only the foreground, contained at the tag's own ratio. */
import { useState } from "react";
import type { TagImages } from "../../shared/tagImages";
import { tagImageContent } from "./tagImageInput";
export function TagArtwork({
  id,
  namespace,
  images,
}: {
  id: string;
  namespace: string;
  images?: TagImages;
}) {
  const [failed, setFailed] = useState("");
  const src = tagImageContent(id, images?.foreground);
  return (
    <div className={`tag-artwork tag-artwork-${namespace}`}>
      {src && failed !== src && (
        <img
          src={src}
          className="tag-artwork-foreground"
          alt=""
          width={namespace === "character" ? 512 : 1024}
          height={namespace === "character" ? 768 : 512}
          loading="lazy"
          decoding="async"
          onError={() => setFailed(src)}
        />
      )}
      {src && failed === src && (
        <span className="tag-image-unavailable">图片暂不可用</span>
      )}
    </div>
  );
}
