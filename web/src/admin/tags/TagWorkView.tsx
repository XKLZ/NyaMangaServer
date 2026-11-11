/** Work navigation resolves to a real non-recycled Edition, then opens the
 * existing full editor. No overview page and no preferred-metadata projection. */
import { Alert, Button, Modal, Spin } from "antd";
import { useLocation, useNavigate } from "react-router";
import type { Book } from "../../shared/api";
import type { Work } from "../../shared/organization";
import type { TagWork } from "../../shared/tagCatalog";
import { BookEditor } from "../books/BookEditor";
import { ErrorNotice } from "../common";
import { useRetainedRemote } from "../useRetainedRemote";
import { useTagPage } from "./useTagPage";
type Route = (v: Record<string, string | number | null>) => void;
export function TagComicDetails({
  workID,
  bookID,
  tag,
  library,
  seed,
  onRoute,
  onChanged,
}: {
  workID: string;
  bookID: string;
  tag: string;
  library: string;
  seed: TagWork | null;
  onRoute: Route;
  onChanged: () => void;
}) {
  const location = useLocation(),
    navigate = useNavigate(),
    params = new URLSearchParams(location.search);
  const work = useRetainedRemote<Work>(
      `/works/${encodeURIComponent(workID)}`,
      !!workID,
    ),
    validWork = !!work.data && work.data.library_id === library;
  const fallback = useTagPage<TagWork>(
    `/tag-catalog/${encodeURIComponent(tag)}/works`,
    {
      library_id: library,
      q: work.data?.name || "",
      sort: "name",
      order: "asc",
      page: 1,
      page_size: 100,
    },
    !!workID && validWork && !bookID && !seed,
  );
  const representative =
    seed || fallback.data?.items.find((v) => v.id === workID);
  const resolved =
    bookID ||
    representative?.cover.edition_id ||
    (fallback.data || fallback.error ? work.data?.preferred_edition_id : "") ||
    "";
  const book = useRetainedRemote<Book>(
    `/books/${encodeURIComponent(resolved)}`,
    !!resolved && (!workID || validWork),
  );
  const b = book.data,
    problem =
      workID && work.data && !validWork
        ? "此作品不属于当前书库，请返回标签重新选择。"
        : b
          ? b.library_id !== library
            ? "此漫画不属于当前书库"
            : workID && b.work_id !== workID
              ? "此版本已不属于当前作品，请返回重新核对。"
              : b.trashed_at
                ? "此版本已在回收站，不能在此编辑。"
                : b.library_disabled
                  ? "书库已停用，不能编辑此版本。"
                  : ""
          : "";
  const close = () =>
    onRoute({ work: null, book: null, preview: null, details: null });
  if (!b || problem)
    return (
      <Modal
        open
        getContainer={false}
        centered
        title="漫画详情"
        aria-label="漫画详情"
        className="tag-content-modal"
        onCancel={close}
        footer={<Button onClick={close}>返回标签</Button>}
      >
        <ErrorNotice
          error={work.error || book.error || fallback.error}
          retry={() => {
            work.reload();
            book.reload();
            fallback.restart();
          }}
        />
        {problem ? (
          <Alert type="warning" title={problem} />
        ) : (
          !(work.error || book.error || fallback.error) && <Spin />
        )}
      </Modal>
    );
  return (
    <BookEditor
      key={b.id}
      id={b.id}
      onClose={close}
      onChanged={() => {
        book.reload();
        if (workID) work.reload();
        onChanged();
      }}
      navigate={(path) => void navigate(path)}
      workContext={validWork ? work.data! : undefined}
      versionsOpen={params.get("details") === "versions"}
      onVersionsOpen={(open) => onRoute({ details: open ? "versions" : null })}
      onVersionQuery={onRoute}
      onEditionChange={(book) =>
        onRoute({ book, details: null, preview: null })
      }
    />
  );
}
