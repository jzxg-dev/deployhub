import { CheckCircle2, CircleDashed, CircleSlash, Hourglass, Loader2, RotateCcw, XCircle, type LucideIcon } from "lucide-react";
import type { JobStatus, UnitStatus } from "@/lib/types";
import { cn } from "@/lib/utils";

const meta: Record<JobStatus | UnitStatus, { label: string; icon: LucideIcon; cls: string; spin?: boolean }> = {
  pending_approval: { label: "待审批", icon: Hourglass, cls: "text-warning" },
  rejected: { label: "已拒绝", icon: CircleSlash, cls: "text-muted-foreground" },
  queued: { label: "排队中", icon: CircleDashed, cls: "text-muted-foreground" },
  pending: { label: "等待", icon: CircleDashed, cls: "text-muted-foreground" },
  running: { label: "执行中", icon: Loader2, cls: "text-primary", spin: true },
  success: { label: "成功", icon: CheckCircle2, cls: "text-success" },
  failed: { label: "失败", icon: XCircle, cls: "text-destructive" },
  rolled_back: { label: "已回滚", icon: RotateCcw, cls: "text-warning" },
  canceled: { label: "已取消", icon: CircleSlash, cls: "text-muted-foreground" },
  skipped: { label: "跳过", icon: CircleSlash, cls: "text-muted-foreground" },
};

/** 任务或应用的状态：图标 + 文字。 */
export function StatusBadge({ status, className }: { status: JobStatus | UnitStatus; className?: string }) {
  const m = meta[status] ?? meta.pending;
  return (
    <span className={cn("inline-flex items-center gap-1 text-sm font-medium whitespace-nowrap", m.cls, className)}>
      <m.icon className={cn("size-4", m.spin && "animate-spin")} />
      {m.label}
    </span>
  );
}

export function StatusIcon({ status, className }: { status: JobStatus | UnitStatus; className?: string }) {
  const m = meta[status] ?? meta.pending;
  return <m.icon className={cn("size-4 shrink-0", m.cls, m.spin && "animate-spin", className)} aria-label={m.label} />;
}

export const kindLabel = { deploy: "部署", rollback: "回滚" } as const;

/** 发起方的显示：页面、访问令牌（AI 和脚本）、导入的旧记录。 */
export function initiatorLabel(initiator: string): string {
  if (initiator === "page") return "页面";
  if (initiator.startsWith("token:")) return `访问令牌「${initiator.slice(6)}」`;
  return initiator;
}

export function isActive(status: JobStatus) {
  return status === "queued" || status === "running";
}

export function duration(start?: string, end?: string): string {
  if (!start) return "";
  const ms = (end ? new Date(end).getTime() : Date.now()) - new Date(start).getTime();
  if (ms < 60_000) return `${Math.max(0, Math.round(ms / 1000))} 秒`;
  return `${Math.floor(ms / 60_000)} 分 ${Math.round((ms % 60_000) / 1000)} 秒`;
}
