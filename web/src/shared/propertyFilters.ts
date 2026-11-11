export type PropertyFilter = {
  languages?: { any_of?: string[]; include_unknown?: boolean };
  translated?: ("yes" | "no" | "unknown")[];
  color_mode?: ("monochrome" | "color" | "unknown")[];
  censorship?: ("uncensored" | "mosaic" | "full" | "unknown")[];
  extraneous_ads?: ("yes" | "no" | "unknown")[];
};
export const propertyFilterOptions = {
  translated: [
    ["yes", "已翻译"],
    ["no", "未翻译"],
    ["unknown", "未知"],
  ],
  color_mode: [
    ["monochrome", "黑白"],
    ["color", "彩色"],
    ["unknown", "未知"],
  ],
  censorship: [
    ["uncensored", "无修正"],
    ["mosaic", "马赛克修正"],
    ["full", "完全修正"],
    ["unknown", "未知"],
  ],
  extraneous_ads: [
    ["yes", "有额外广告"],
    ["no", "无额外广告"],
    ["unknown", "未知"],
  ],
} as const;
function object(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === "object" && !Array.isArray(value);
}
export function validateProperties(value: unknown): PropertyFilter | undefined {
  if (value === undefined) return undefined;
  if (!object(value)) throw new Error("属性筛选必须为 JSON 对象。");
  const result: PropertyFilter = {};
  for (const [key, field] of Object.entries(value)) {
    if (key === "languages") {
      if (
        !object(field) ||
        Object.keys(field).some(
          (k) => !["any_of", "include_unknown"].includes(k),
        ) ||
        (field.include_unknown !== undefined &&
          typeof field.include_unknown !== "boolean") ||
        (field.any_of !== undefined &&
          (!Array.isArray(field.any_of) ||
            field.any_of.length > 200 ||
            field.any_of.some(
              (s) =>
                typeof s !== "string" ||
                !s.trim() ||
                s.includes("\0") ||
                [...s].length > 64 ||
                new TextEncoder().encode(s).length > 256,
            )))
      )
        throw new Error("语言筛选格式无效。");
      const any_of = field.any_of as string[] | undefined;
      if (
        any_of?.some((v) =>
          ["translated", "rewrite"].includes(
            v.normalize("NFKC").trim().replace(/\s+/g, " ").toLowerCase(),
          ),
        )
      )
        throw new Error(
          "translated / rewrite 是状态词，不能作为语言筛选；请修正输入。",
        );
      if (!any_of?.length && !field.include_unknown)
        throw new Error("语言筛选至少选择一种语言或未知。");
      result.languages = {
        ...(any_of?.length ? { any_of } : {}),
        ...(field.include_unknown ? { include_unknown: true } : {}),
      };
    } else {
      if (!(key in propertyFilterOptions))
        throw new Error(`不支持的属性筛选：${key}`);
      const allowed = propertyFilterOptions[
        key as keyof typeof propertyFilterOptions
      ].map(([v]) => v) as readonly string[];
      if (
        !Array.isArray(field) ||
        field.length > allowed.length ||
        field.some((v) => typeof v !== "string" || !allowed.includes(v)) ||
        new Set(field).size !== field.length
      )
        throw new Error(`属性筛选 ${key} 的值无效。`);
      if (field.length) Object.assign(result, { [key]: field });
    }
  }
  return Object.keys(result).length ? result : undefined;
}
export function propertiesFromURL(params: URLSearchParams) {
  if (!params.has("properties")) return undefined;
  let parsed: unknown;
  try {
    parsed = JSON.parse(params.get("properties")!);
  } catch {
    throw new Error("属性筛选 JSON 无效，请重置筛选或修正地址栏。");
  }
  return validateProperties(parsed);
}
export function writeSearchParams(
  params: URLSearchParams,
  values: Record<string, unknown>,
) {
  for (const [key, value] of Object.entries(values)) {
    if (key === "properties") {
      const properties = validateProperties(value);
      if (properties) params.set(key, JSON.stringify(properties));
      else params.delete(key);
    } else if (value === undefined) params.delete(key);
    else params.set(key, String(value));
  }
  return params;
}
export function savedConditions<
  T extends {
    page: number;
    snapshot_revision?: string;
    properties?: PropertyFilter;
  },
>(conditions: T): T {
  return {
    ...conditions,
    page: 1,
    snapshot_revision: undefined,
    properties: validateProperties(conditions.properties),
  };
}
