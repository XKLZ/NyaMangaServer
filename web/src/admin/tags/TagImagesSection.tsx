import type { CatalogDetail } from "../../shared/tagCatalog";
import { TagImageEditor } from "./TagImageEditor";
export function TagImagesSection({
  tag,
  active,
  onChanged,
}: {
  tag: CatalogDetail;
  active: boolean;
  onChanged: () => void;
}) {
  return active && ["parody", "character"].includes(tag.namespace) ? (
    <TagImageEditor key={tag.id} tag={tag} onChanged={onChanged} />
  ) : null;
}
