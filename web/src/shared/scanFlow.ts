export type ScanIssue = {
  stage: string;
  code: string;
  resource?: string;
  actual?: number;
  limit?: number;
};
export type ScanResult = {
  classification: "completed" | "partial" | "blocked";
  stages: Record<string, string>;
  issues: ScanIssue[];
  parsing_revision: number;
  warning_count: number;
};
export const scanClassifications = {
  completed: "已完成",
  partial: "已入库，部分处理未完成",
  blocked: "无法完成文件检查",
};
export const scanStages: Record<string, string> = {
  directory: "文件目录",
  cover: "封面处理",
  lookup: "代表图摘要",
  decode: "图片解码",
  read: "图片读取",
};
export const scanResources: Record<string, string> = {
  entries: "归档条目数",
  directory_bytes: "目录大小",
  manifest_bytes: "索引大小",
  page_bytes: "单页大小",
  book_bytes: "归档解压大小",
  pixels: "图片像素",
  estimated_memory_bytes: "估算解码内存",
};
export type Encoding = "utf8" | "gb18030" | "shift_jis";
export const encodingNames: Record<string, string> = {
  utf8: "UTF-8",
  gb18030: "GB18030",
  shift_jis: "Shift-JIS",
};
export type EncodingCandidate = {
  encoding: Encoding;
  valid: boolean;
  page_count: number;
  sample: { page_index: number; display_name: string }[];
  directory_digest: string;
  error_code?: string;
};
export type EncodingPreview = {
  edition_id: string;
  revision: number;
  content_revision: number;
  parsing_revision: number;
  inherited_encoding: string;
  requires_choice: boolean;
  candidates: EncodingCandidate[];
};
export function encodingCommand(
  base: EncodingPreview,
  value: Encoding | "inherit",
) {
  const candidate = base.candidates.find(
    (v) =>
      v.encoding === (value === "inherit" ? base.inherited_encoding : value),
  );
  if (!candidate?.valid)
    throw new Error("所选编码不能解析此文件，请核对其他候选。");
  return {
    expected_revision: base.revision,
    content_revision: base.content_revision,
    parsing_revision: base.parsing_revision,
    encoding: value === "inherit" ? null : value,
    directory_digest: candidate.directory_digest,
  };
}
export function parsingRevision(book: {
  file?: { parsing_revision?: number };
}) {
  return book.file?.parsing_revision ?? 1;
}
export function pageIdentity(book: {
  content_revision: number;
  file?: { parsing_revision?: number };
}) {
  return `content_revision=${book.content_revision}&parsing_revision=${parsingRevision(book)}`;
}

export function scanLimitMessage(issue: ScanIssue): string | null {
  if (
    issue.actual === undefined ||
    issue.limit === undefined ||
    !issue.resource
  )
    return null;
  const stage =
    issue.stage === "cover"
      ? "封面"
      : issue.stage === "lookup"
        ? "代表图摘要"
        : "文件";
  const messages: Record<string, string> = {
    estimated_memory_bytes: `${stage}解码所需内存超过限制`,
    pixels: `${stage}图片像素超过限制`,
    page_bytes: `${stage}单页大小超过限制`,
    book_bytes: "归档解压大小超过限制",
    directory_bytes: "归档目录大小超过限制",
    manifest_bytes: "归档索引大小超过限制",
    entries: "归档条目数超过限制",
  };
  return messages[issue.resource] || null;
}
