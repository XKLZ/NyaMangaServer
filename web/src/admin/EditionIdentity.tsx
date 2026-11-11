import { App as AntApp } from "antd";
import { useLocation, useNavigate } from "react-router";
import { BookEditor } from "./books/BookEditor";
import {
  cleanBookTitle,
  copyBookTitle,
  originalTitle,
} from "./books/bookTitle";
import { Cover } from "./versions/versionManagementViews";
export function EditionCover({
  member,
  onOpen,
  disabled = false,
}: {
  member: { id: string; content_revision: number; cover_revision?: number };
  onOpen?: (id: string) => void;
  disabled?: boolean;
}) {
  const location = useLocation(),
    navigate = useNavigate();
  function open() {
    if (onOpen) {
      onOpen(member.id);
      return;
    }
    const params = new URLSearchParams(location.search);
    params.set("book", member.id);
    void navigate({ search: params.toString() }, { preventScrollReset: true });
  }
  return (
    <button
      type="button"
      className="version-member-cover"
      disabled={disabled}
      aria-label="打开漫画详情"
      onClick={open}
    >
      <Cover
        id={member.id}
        content={member.content_revision}
        revision={member.cover_revision}
        preview={false}
      />
    </button>
  );
}
export function EditionTitle({
  member,
}: {
  member: { title?: string; title_original?: string; name?: string };
}) {
  const { message } = AntApp.useApp();
  const title = originalTitle(member);
  return (
    <>
      <button
        className="version-member-title"
        type="button"
        aria-label={`复制标题 ${title}`}
        onClick={() =>
          void copyBookTitle(title, navigator.clipboard).then((result) =>
            message[result.type](result.content),
          )
        }
      >
        {cleanBookTitle(title)}
      </button>
      <div className="version-subtitle">{title}</div>
    </>
  );
}

export function WorkspaceBookDetails({ onChanged }: { onChanged: () => void }) {
  const location = useLocation(),
    navigate = useNavigate();
  const id = new URLSearchParams(location.search).get("book");
  if (!id) return null;
  return (
    <BookEditor
      key={id}
      id={id}
      onClose={() => {
        const params = new URLSearchParams(location.search);
        params.delete("book");
        params.delete("preview");
        void navigate(
          { search: params.toString() },
          { preventScrollReset: true },
        );
      }}
      onChanged={onChanged}
      navigate={(path) => void navigate(path)}
    />
  );
}
