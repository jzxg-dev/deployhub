import { Cloud, Server } from "lucide-react";
import { cn } from "@/lib/utils";

/** 部署目标：服务器名；不需要服务器的部署方式（如 Workers）显示云平台账号。 */
export function DeployTarget({ server, account, className }: { server: string; account?: unknown; className?: string }) {
  const Icon = server ? Server : Cloud;
  const label = server || (typeof account === "string" && account) || "云平台";
  return (
    <span className={cn("flex min-w-0 items-center gap-1", className)}>
      <Icon className="size-3.5 shrink-0" />
      <span className="truncate">{label}</span>
    </span>
  );
}
