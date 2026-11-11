import { useState } from "react";
import { Select, Typography } from "antd";
import { validateVersionLabels } from "../shared/versionLabels";

export function VersionLabelsInput({
  value,
  onChange,
  label,
}: {
  value: string[];
  onChange: (value: string[]) => void;
  label: string;
}) {
  const [error, setError] = useState("");
  return (
    <div className="full-width">
      <Select
        mode="tags"
        notFoundContent={null}
        className="full-width"
        aria-label={label}
        value={value}
        allowClear
        placeholder="输入标签后按 Enter，可添加多个"
        onChange={(next: string[]) => {
          try {
            const result = validateVersionLabels(next);
            setError("");
            onChange(result);
          } catch (e) {
            setError(e instanceof Error ? e.message : "标签无效");
          }
        }}
      />
      {error && (
        <Typography.Text type="danger" role="alert">
          {error}
        </Typography.Text>
      )}
    </div>
  );
}
