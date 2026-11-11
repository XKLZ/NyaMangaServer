export function versionLabels(member: {
  version_labels?: string[];
  version_label?: string;
}): string[] {
  return (
    member.version_labels ??
    (member.version_label ? [member.version_label] : [])
  );
}

export function validateVersionLabels(values: string[]): string[] {
  if (values.length > 20) throw new Error("每个版本最多 20 个标签");
  for (const value of values) {
    if (
      !value.trim() ||
      value.includes("\0") ||
      Array.from(value).length > 128 ||
      new TextEncoder().encode(value).length > 512
    )
      throw new Error("每个标签须为 1–128 个字符");
  }
  return [...new Set(values)];
}
