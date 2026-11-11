import { Link } from "react-router";
import type { CatalogTag } from "../../shared/tagCatalog";
import { tagName } from "./tagCatalogFlow";
import { TagArtwork } from "./TagArtwork";
import { TagFollowButton } from "./TagFollow";
export function TagCard({
  tag,
  to,
  count = tag.work_count,
  onOpen,
  state,
  replace = false,
  same = false,
}: {
  tag: CatalogTag;
  to: string;
  count?: number;
  onOpen?: (element: HTMLAnchorElement) => void;
  state?: unknown;
  replace?: boolean;
  same?: boolean;
}) {
  return (
    <article
      className={`tag-catalog-card ${["parody", "character"].includes(tag.namespace) ? `tag-image-card tag-image-card-${tag.namespace}` : "tag-compact-card"}`}
      data-tag-id={tag.id}
    >
      <Link
        className="tag-card-link"
        aria-label={tagName(tag)}
        to={to}
        state={state}
        replace={replace}
        preventScrollReset
        onClick={(e) => {
          if (same) e.preventDefault();
          else onOpen?.(e.currentTarget);
        }}
      >
        {["parody", "character"].includes(tag.namespace) && (
          <TagArtwork
            id={tag.id}
            namespace={tag.namespace}
            images={tag.images}
          />
        )}
        <div className="tag-card-text">
          <strong className="tag-catalog-name">{tagName(tag)}</strong>
          <span className="tag-card-count">{count} 个作品</span>
        </div>
      </Link>
      <TagFollowButton tag={tag} />
    </article>
  );
}
