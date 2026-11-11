import { usePermissions } from "../../shared/permissions";
import { Tabs } from "antd";
import { useLocation } from "react-router";
import {
  maintenanceSection,
  sectionURL,
  taskID,
} from "../../shared/consoleRoutes.mjs";
import { InstanceWorkspace } from "./InstanceWorkspace";
import { HealthWorkspace } from "./HealthWorkspace";
import { TasksPage } from "./Tasks";
import { StorageWorkspace } from "./StorageWorkspace";
import { MaintenancePanel } from "./Maintenance";
import { CurrentLibraryGate } from "../CurrentLibrary";
export function MaintenanceCenter({
  navigate,
}: {
  navigate: (path: string) => void;
}) {
  const admin = usePermissions().admin;
  const location = useLocation(),
    section = admin ? maintenanceSection(location.search) : "tasks";
  const id = taskID(location.pathname, location.search);
  return (
    <section className="maintenance-center">
      <h1 className="sr-only">维护中心</h1>
      <Tabs
        activeKey={section}
        onChange={(next) =>
          navigate(sectionURL(location.pathname, location.search, next))
        }
        destroyOnHidden
        items={Object.entries({
          tasks: "任务",
          health: "数据健康",
          recovery: "备份与恢复",
          trash: "回收站",
          storage: "存储与清理",
        })
          .filter(([key]) => admin || key === "tasks")
          .map(([key, label]) => ({
            key,
            label,
            children:
              section !== key ? null : key === "recovery" ? (
                <InstanceWorkspace />
              ) : key === "storage" ? (
                <StorageWorkspace />
              ) : key === "health" ? (
                <HealthWorkspace />
              ) : (
                <CurrentLibraryGate>
                  {key === "tasks" ? (
                    <TasksPage
                      key={id}
                      id={id || undefined}
                      navigate={navigate}
                    />
                  ) : (
                    <MaintenancePanel
                      key={key}
                      view="trash"
                      navigate={navigate}
                    />
                  )}
                </CurrentLibraryGate>
              ),
          }))}
      />
    </section>
  );
}
