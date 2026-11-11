import { EnhancementStatus } from "../EnhancementStatus";
import { Button } from "antd";
import { useNavigate } from "react-router";
import { useRecommendationCapability } from "../../shared/recommendations";
import { usePermissions } from "../../shared/permissions";

/** Old selection entry points now navigate; they never start an Edition task. */
export function RecommendationLauncher({ libraryID }: { libraryID?: string }) {
  const navigate = useNavigate();
  const editable = usePermissions().edit(libraryID || "");
  const { canStartFor } = useRecommendationCapability(editable);
  if (!editable) return null;
  if (!canStartFor("work"))
    return <EnhancementStatus feature="work_recommendations" resources />;
  return (
    <Button
      onClick={() =>
        void navigate(
          `/admin/recommendations${libraryID ? `?library_id=${encodeURIComponent(libraryID)}` : ""}`,
        )
      }
    >
      查找同作品
    </Button>
  );
}
