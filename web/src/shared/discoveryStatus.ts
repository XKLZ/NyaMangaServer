// Codes are deliberately finite: private worker output never reaches the UI.
export function discoveryFailure(reason: string): string {
  const reasons: Record<string, string> = {
    discovery_failed: "查找中断；旧记录未保存具体原因，已提交的进度仍保留。",
    discovery_worker_unavailable:
      "本地计算进程中断，已保留检查点；可恢复任务继续。",
    discovery_resource_budget_exceeded:
      "达到计算资源预算，已保留检查点；请检查资源后恢复。",
    discovery_inputs_changed: "匹配资料已变化，请重新查找以冻结新的输入。",
    discovery_invalid_checkpoint:
      "检查点或计算响应校验失败，请重新查找；旧结果不会强行写入。",
  };
  return (
    reasons[reason] ?? (reason ? "任务暂时无法继续，请查看任务中心。" : "")
  );
}
