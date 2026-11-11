import { Checkbox, Select } from "antd";
import { useState } from "react";
import type { PropertyFilter } from "../shared/propertyFilters";
import { propertyFilterOptions } from "../shared/propertyFilters";
const labels = {
  translated: "翻译状态",
  color_mode: "颜色",
  censorship: "修正",
  extraneous_ads: "额外广告",
};
export function PropertyFilters({
  value = {},
  onChange,
}: {
  value?: PropertyFilter;
  onChange: (value: PropertyFilter | undefined) => void;
}) {
  const appliedKey = JSON.stringify(value);
  const [languageDraft, setLanguageDraft] = useState({
    key: appliedKey,
    values: value.languages?.any_of ?? [],
  });
  const languageValues =
    languageDraft.key === appliedKey
      ? languageDraft.values
      : (value.languages?.any_of ?? []);
  function update(key: keyof PropertyFilter, field: unknown) {
    const next = { ...value };
    if (field === undefined) delete next[key];
    else Object.assign(next, { [key]: field });
    onChange(Object.keys(next).length ? next : undefined);
  }
  function language(any_of: string[], include_unknown: boolean) {
    setLanguageDraft({ key: appliedKey, values: any_of });
    update(
      "languages",
      any_of.length || include_unknown
        ? {
            ...(any_of.length ? { any_of } : {}),
            ...(include_unknown ? { include_unknown } : {}),
          }
        : undefined,
    );
  }
  return (
    <div className="metadata-grid full-width">
      <div>
        <label htmlFor="filter-languages">语言</label>
        <Select
          id="filter-languages"
          aria-label="筛选语言"
          className="full-width"
          mode="tags"
          maxCount={200}
          value={languageValues}
          options={[
            { value: "chinese", label: "中文" },
            { value: "japanese", label: "日语" },
            { value: "english", label: "英语" },
          ]}
          showSearch={{ optionFilterProp: "label" }}
          onChange={(v: string[]) =>
            language(v, value.languages?.include_unknown ?? false)
          }
          placeholder="任一语言；支持自定义"
        />
        <Checkbox
          checked={value.languages?.include_unknown ?? false}
          onChange={(e) => language(languageValues, e.target.checked)}
        >
          包括未知语言
        </Checkbox>
      </div>
      {(
        Object.keys(
          propertyFilterOptions,
        ) as (keyof typeof propertyFilterOptions)[]
      ).map((key) => (
        <div key={key}>
          <label htmlFor={`filter-${key}`}>{labels[key]}</label>
          <Select
            id={`filter-${key}`}
            aria-label={`筛选${labels[key]}`}
            className="full-width"
            mode="multiple"
            allowClear
            value={value[key] ?? []}
            options={propertyFilterOptions[key].map(([value, label]) => ({
              value,
              label,
            }))}
            onChange={(v: string[]) => update(key, v.length ? v : undefined)}
            placeholder="不限"
          />
        </div>
      ))}
    </div>
  );
}
