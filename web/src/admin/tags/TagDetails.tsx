import { usePermissions } from "../../shared/permissions";
import { useContext } from "react";
import { BusinessReadContext } from "../../shared/readBoundary";
import { Button, Spin } from "antd";
import type { CatalogDetail, TagWork } from "../../shared/tagCatalog";
import { ErrorNotice } from "../common";
import { TagTranslation } from "./TagTranslation";
import { TagRelations } from "./TagRelations";
import { TagRelationDisplay } from "./TagRelationDisplay";
import { TagWorkCards } from "./TagWorkCards";
import { TagImagesSection } from "./TagImagesSection";
export function TagDetails({
  id,
  search,
  library,
  detail,
  error,
  refresh,
  onChanged,
  onRoute,
  refreshEpoch,
  active,
  imagesMounted = active,
  onOverlay,
  onSelectWork,
  onWorkRows,
  onRemember,
}: {
  id: string;
  search: string;
  library: string;
  detail: CatalogDetail | null;
  error: unknown;
  refresh: () => void;
  onChanged: () => void;
  onRoute: (v: Record<string, string | number | null>) => void;
  refreshEpoch: number;
  active: boolean;
  imagesMounted?: boolean;
  onOverlay: (v: boolean) => void;
  onSelectWork: (v: TagWork) => void;
  onWorkRows: (items: TagWork[]) => void;
  onRemember: () => void;
}) {
  const admin = usePermissions().admin;
  const readable = useContext(BusinessReadContext);
  return (
    <div className="tag-details">
      <ErrorNotice error={error} retry={refresh} />
      {!detail && !error && <Spin />}
      {detail && (
        <>
          <BusinessReadContext.Provider value={readable && active}>
            <TagImagesSection
              tag={detail}
              active={imagesMounted}
              onChanged={onChanged}
            />
          </BusinessReadContext.Provider>
          <div>
            {admin ? (
              <TagTranslation
                tag={detail}
                library={library}
                onChanged={onChanged}
              />
            ) : (
              <section className="tag-translation">
                <h3>译名</h3>
                <p>{detail.translation || detail.name}</p>
              </section>
            )}
            {admin &&
            new URLSearchParams(search).get("relations") === "edit" &&
            ["parody", "character"].includes(detail.namespace) ? (
              <>
                <div className="tag-section-heading">
                  <h3>角色归属管理</h3>
                  <Button onClick={() => onRoute({ relations: null })}>
                    返回关联展示
                  </Button>
                </div>
                <TagRelations
                  id={id}
                  library={library}
                  detail={detail}
                  search={search}
                  active={active}
                  onOverlay={onOverlay}
                  onOpenEdition={onRemember}
                  onChanged={onChanged}
                  onRoute={onRoute}
                  refreshEpoch={refreshEpoch}
                />
              </>
            ) : (
              <>
                {["parody", "character"].includes(detail.namespace) && (
                  <TagRelationDisplay
                    tag={detail}
                    library={library}
                    search={search}
                    active={active}
                    refreshEpoch={refreshEpoch}
                    onRoute={onRoute}
                    onOpen={onRemember}
                  />
                )}
                <TagWorkCards
                  tag={id}
                  library={library}
                  search={search}
                  active={active}
                  onRoute={onRoute}
                  onSelect={onSelectWork}
                  onRows={onWorkRows}
                  refreshEpoch={refreshEpoch}
                />
              </>
            )}
          </div>
        </>
      )}
    </div>
  );
}
