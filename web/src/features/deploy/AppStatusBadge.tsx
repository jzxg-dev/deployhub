import { ArrowUpCircle, CheckCircle2, CircleDashed, Loader2, XCircle } from "lucide-react";
import { Link } from "react-router";
import type { AppStatus } from "@/lib/types";
import { cn } from "@/lib/utils";

const meta = {
  up_to_date: { label: "已是最新", icon: CheckCircle2, cls: "text-success" },
  has_update: { label: "有更新", icon: ArrowUpCircle, cls: "text-primary" },
  not_deployed: { label: "未部署", icon: CircleDashed, cls: "text-muted-foreground" },
  unreachable: { label: "读取失败", icon: XCircle, cls: "text-destructive" },
  busy: { label: "部署中", icon: Loader2, cls: "text-primary" },
} as const;

/** 应用的部署状态：已是最新 / 有更新 / 未部署 / 读取失败 / 部署中。 */
export function AppStatusBadge({ status }: { status: AppStatus }) {
  const m = meta[status.state];
  const body = (
    <span className={cn("inline-flex items-center gap-1 text-xs font-medium whitespace-nowrap", m.cls)} title={status.error}>
      <m.icon className={cn("size-3.5", status.state === "busy" && "animate-spin")} />
      {m.label}
    </span>
  );
  return status.busy ? (
    <Link to={`/jobs/${status.busy}`} className="hover:underline" onClick={(e) => e.stopPropagation()}>
      {body}
    </Link>
  ) : (
    body
  );
}
