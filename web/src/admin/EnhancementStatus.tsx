import { usePermissions } from "../shared/permissions";
import { useRecommendationCapability } from "../shared/recommendations";
import { Link } from "react-router";
import { Space, Tag, Typography } from "antd";
import { useFeature } from "../shared/enhancements";
import {
  enhancementSettingsURL,
  featureNames,
  featureStatus,
  resourceLabel,
} from "../shared/enhancementFlow";
import type { FeatureKey } from "../shared/enhancementFlow";
export function EnhancementStatus({
  feature: key,
  resources = false,
}: {
  feature: FeatureKey;
  resources?: boolean;
}) {
  const { admin } = usePermissions();
  const { capability } = useRecommendationCapability(
    !admin && ["work_recommendations", "series_recommendations"].includes(key),
  );
  const { feature, error } = useFeature(key);
  if (!admin)
    return capability &&
      !(key === "series_recommendations"
        ? capability.series_enabled
        : capability.work_enabled) ? (
      <Tag>此推荐功能未启用，请联系管理员</Tag>
    ) : resources && capability ? (
      <Typography.Text type="secondary">
        {resourceLabel(capability.resource_state || "unknown")}
      </Typography.Text>
    ) : null;
  if (!feature)
    return error ? (
      <Link to={enhancementSettingsURL(key)}>读取增强设置失败，前往核对</Link>
    ) : null;
  if (feature.effective_enabled)
    return resources ? (
      <Typography.Text type="secondary">
        {resourceLabel(feature.resource_state)}
      </Typography.Text>
    ) : null;
  return (
    <Space size={8} wrap className="enhancement-status">
      <Tag>
        {featureNames[key]} · {featureStatus(feature)}
      </Tag>
      <Link to={enhancementSettingsURL(key)}>增强设置</Link>
    </Space>
  );
}
