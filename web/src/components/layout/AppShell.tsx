import { Cloud, FolderKanban, History, Hourglass, KeyRound, LayoutDashboard, LogOut, Rocket, Server, Settings, Users, type LucideIcon } from "lucide-react";
import { Link, NavLink, Outlet } from "react-router";
import { initiatorLabel, kindLabel } from "@/features/deploy/status";
import { useEventStream, type StreamStatus } from "@/hooks/events";
import { useJobs, useSession } from "@/hooks/queries";
import { logout } from "@/lib/session";
import { cn } from "@/lib/utils";

const nav: { to: string; label: string; icon: LucideIcon; end?: boolean; adminOnly?: boolean }[] = [
  { to: "/", label: "总览", icon: LayoutDashboard, end: true },
  { to: "/projects", label: "项目", icon: FolderKanban },
  { to: "/jobs", label: "部署记录", icon: History },
  { to: "/servers", label: "服务器", icon: Server },
  { to: "/secrets", label: "密钥与变量", icon: KeyRound },
  { to: "/credentials", label: "凭据", icon: Cloud },
  { to: "/users", label: "账号", icon: Users, adminOnly: true },
  { to: "/settings", label: "设置", icon: Settings },
];

const statusText: Record<StreamStatus, string> = {
  open: "已连接",
  connecting: "连接中",
  closed: "已断开",
};

function StreamIndicator() {
  const { status } = useEventStream();
  return (
    <span className="flex items-center gap-1.5" title="实时事件连接">
      <span
        className={cn(
          "size-2 rounded-full",
          status === "open" && "bg-success",
          status === "connecting" && "animate-pulse bg-warning",
          status === "closed" && "bg-destructive",
        )}
      />
      {statusText[status]}
    </span>
  );
}

/** 待审批的任务：访问令牌（AI、脚本）发起、要你批准才执行的部署和回滚。每个页面顶部都提示。 */
function PendingApprovals() {
  const { data: jobs } = useJobs({ status: "pending_approval" });
  if (!jobs?.length) return null;
  return (
    <div className="mb-6 rounded-lg border border-warning/50 bg-warning/5 px-4 py-3">
      <p className="mb-2 flex items-center gap-2 text-sm font-medium">
        <Hourglass className="size-4 text-warning" />有 {jobs.length} 个任务等你审批
      </p>
      <ul className="space-y-1.5">
        {jobs.map((j) => (
          <li key={j.id}>
            <Link to={`/jobs/${j.id}`} className="flex flex-wrap items-baseline gap-x-2 text-sm hover:underline">
              <span className="font-mono text-xs text-muted-foreground">#{j.id}</span>
              <span>
                {initiatorLabel(j.initiator)}请求{kindLabel[j.kind]} {j.units.map((u) => u.title).join("、")}
              </span>
              <span className="text-xs text-primary">查看并审批</span>
            </Link>
          </li>
        ))}
      </ul>
    </div>
  );
}

export function AppShell() {
  const { data: session } = useSession();
  const items = nav.filter((n) => !n.adminOnly || session?.user.admin);
  return (
    <div className="flex min-h-svh flex-col md:flex-row">
      <aside className="flex shrink-0 flex-col border-b border-sidebar-border bg-sidebar text-sidebar-foreground md:sticky md:top-0 md:h-svh md:w-56 md:border-r md:border-b-0">
        <div className="flex items-center gap-2 px-4 py-3 md:py-5">
          <span className="flex size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
            <Rocket className="size-4" />
          </span>
          <span className="text-base font-semibold tracking-tight">deployhub</span>
        </div>
        <nav aria-label="主导航" className="overflow-x-auto px-2 pb-2 md:flex-1 md:overflow-visible md:pb-0">
          <ul className="flex gap-1 md:flex-col">
            {items.map(({ to, label, icon: Icon, end }) => (
              <li key={to}>
                <NavLink
                  to={to}
                  end={end}
                  className={({ isActive }) =>
                    cn(
                      "flex items-center gap-2.5 rounded-md px-3 py-2 text-sm whitespace-nowrap text-sidebar-foreground/75 transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
                      isActive && "bg-sidebar-accent font-medium text-sidebar-accent-foreground",
                    )
                  }
                >
                  <Icon className="size-4" />
                  {label}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>
        {/* 手机上导航是横排，退出登录在「设置」页里 */}
        <div className="hidden items-center justify-between gap-2 border-t border-sidebar-border px-4 py-3 text-sm md:flex">
          <span className="min-w-0 truncate font-medium" title={session?.user.admin ? "管理员" : undefined}>
            {session?.user.username}
          </span>
          <button
            type="button"
            onClick={() => void logout()}
            className="flex shrink-0 items-center gap-1 rounded-md px-2 py-1 text-xs text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
          >
            <LogOut className="size-3.5" />
            退出
          </button>
        </div>
        <div className="hidden items-center justify-between border-t border-sidebar-border px-4 py-3 text-xs text-muted-foreground md:flex">
          <StreamIndicator />
          <span className="font-mono">{session?.version}</span>
        </div>
      </aside>
      <main className="min-w-0 flex-1 px-4 py-6 md:px-8 md:py-8">
        <PendingApprovals />
        <Outlet />
      </main>
    </div>
  );
}
