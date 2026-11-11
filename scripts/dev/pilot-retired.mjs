console.error(
  "试用实例已迁至 .runtime/dev。请使用 make dev-api / make dev-web；迁移记录见 docs/project-maintenance.md。旧启动入口已停用。",
);
process.exitCode = 1;
