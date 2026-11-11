import { Tag } from "antd";
import { reason } from "../../shared/catalog";
import { bytesText } from "../../shared/storageFlow";
import {
  scanClassifications,
  scanResources,
  scanStages,
  scanLimitMessage,
} from "../../shared/scanFlow";
import type { ScanResult } from "../../shared/scanFlow";
export function ScanStatus({ result }: { result: ScanResult }) {
  return (
    <Tag
      color={
        result.classification === "completed"
          ? "success"
          : result.classification === "partial"
            ? "warning"
            : "error"
      }
    >
      {scanClassifications[result.classification]}
    </Tag>
  );
}
export function ScanDiagnostics({ result }: { result: ScanResult }) {
  return (
    <div className="scan-issues">
      {result.issues?.map((issue, i) => (
        <div key={i} className="scan-issue">
          <span>
            {scanLimitMessage(issue) ||
              `${scanStages[issue.stage] || "文件处理"}：${reason(issue.code)}`}
          </span>
          {issue.actual !== undefined && issue.limit !== undefined && (
            <small>
              {scanResources[issue.resource || ""] || "资源用量"}：
              {issue.resource?.endsWith("bytes")
                ? bytesText(issue.actual)
                : issue.actual.toLocaleString()}{" "}
              / 上限{" "}
              {issue.resource?.endsWith("bytes")
                ? bytesText(issue.limit)
                : issue.limit.toLocaleString()}
            </small>
          )}
        </div>
      ))}
    </div>
  );
}
