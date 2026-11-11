import { ArrowRight, CheckCircle2, FolderKanban, KeyRound, RefreshCw, Rocket, Server } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";
import { DeployTarget } from "@/components/common/DeployTarget";
import { PageHeader } from "@/components/layout/Page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { DriverIcon } from "@/features/app-form/DriverPicker";
import { AppStatusBadge } from "@/features/deploy/AppStatusBadge";
import { DeployDialog } from "@/features/deploy/DeployDialog";
import { useAllApps, useCatalog, useProjects, useSecrets, useServers, useStatus } from "@/hooks/queries";
import { cn } from "@/lib/utils";

export function OverviewPage() {
  const { data: catalog } = useCatalog();
  const { data: servers } = useServers();
  const { data: secrets } = useSecrets();
  const { data: projects } = useProjects();
  const { data: apps, isLoading } = useAllApps();
  const status = useStatus();
  const [deploying, setDeploying] = useState<string[] | null>(null);

  const steps = [
    { icon: Server, title: "添加服务器", text: "地址、端口、用户和 SSH 密钥，测试连接时自动检测架构和环境", to: "/servers", done: (servers?.length ?? 0) > 0 },
    { icon: KeyRound, title: "添加密钥与变量", text: "构建和运行需要的敏感值，在参数里用 ${名称} 引用；没有可以跳过", to: "/secrets", done: (secrets?.length ?? 0) > 0 },
    { icon: FolderKanban, title: "创建项目和应用", text: "选择部署方式、填写参数，保存后就能在页面上部署", to: "/projects", done: (apps?.length ?? 0) > 0 },
  ];
  const driverOf = (name: string) => catalog?.drivers.find((d) => d.name === name);
  const statusOf = (project: string, app: string) => status.data?.find((s) => s.project === project && s.app === app);
  const updatable = (status.data ?? []).filter((s) => s.state === "has_update");
  const count = (state: string) => (status.data ?? []).filter((s) => s.state === state).length;

  return (
    <>
      <PageHeader
        title="总览"
        description="所有应用的部署状态：线上版本和本地最新提交是否一致。"
        actions={
          apps?.length ? (
            <>
              <Button variant="outline" onClick={() => status.refetch()} disabled={status.isFetching}>
                <RefreshCw className={cn(status.isFetching && "animate-spin")} />
                刷新状态
              </Button>
              <Button disabled={!updatable.length} onClick={() => setDeploying(updatable.map((s) => `${s.project}/${s.app}`))}>
                <Rocket />
                部署有更新的{updatable.length ? `（${updatable.length}）` : ""}
              </Button>
            </>
          ) : undefined
        }
      />
      {isLoading ? (
        <Skeleton className="h-40 w-full" />
      ) : !apps?.length ? (
        <div className="grid gap-4 md:grid-cols-3">
          {steps.map(({ icon: Icon, title, text, to, done }, i) => (
            <Card key={title} className="gap-3">
              <CardHeader className="gap-2">
                <div className="flex items-center gap-2 text-sm text-muted-foreground">
                  {done ? (
                    <CheckCircle2 className="size-6 text-success" />
                  ) : (
                    <span className="flex size-6 items-center justify-center rounded-full bg-primary/10 text-xs font-medium text-primary">{i + 1}</span>
                  )}
                  <Icon className="size-4" />
                </div>
                <CardTitle className="text-base">{title}</CardTitle>
                <CardDescription>{text}</CardDescription>
              </CardHeader>
              <CardContent>
                <Button asChild variant="ghost" size="sm" className="-ml-2">
                  <Link to={to}>
                    前往
                    <ArrowRight />
                  </Link>
                </Button>
              </CardContent>
            </Card>
          ))}
        </div>
      ) : (
        <div className="space-y-6">
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:max-w-3xl">
            {[
              ["项目 / 应用", `${projects?.length ?? 0} / ${apps.length}`, ""],
              ["有更新", status.data ? String(count("has_update")) : "…", "text-primary"],
              ["未部署", status.data ? String(count("not_deployed")) : "…", ""],
              ["读取失败", status.data ? String(count("unreachable")) : "…", count("unreachable") ? "text-destructive" : ""],
            ].map(([label, n, cls]) => (
              <Card key={label} className="gap-1 px-4 py-3">
                <p className="text-xs text-muted-foreground">{label}</p>
                <p className={cn("text-2xl font-semibold tabular-nums", cls)}>{n}</p>
              </Card>
            ))}
          </div>
          <Card className="gap-0 overflow-hidden py-0">
            <ul className="divide-y">
              {apps.map((a) => {
                const d = driverOf(a.driver);
                const st = statusOf(a.project, a.name);
                return (
                  <li key={a.project + "/" + a.name}>
                    <Link
                      to={`/projects/${a.project}/apps/${a.name}`}
                      className="grid gap-2 px-4 py-3 hover:bg-accent/40 md:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_minmax(0,1.6fr)_7rem] md:items-center"
                    >
                      <span className="flex min-w-0 items-center gap-3">
                        <DriverIcon name={d?.icon ?? ""} className="size-4 shrink-0 text-primary" />
                        <span className="min-w-0">
                          <span className="block truncate text-sm font-medium">{a.title}</span>
                          <span className="block truncate font-mono text-xs text-muted-foreground">
                            {a.project}/{a.name}
                          </span>
                        </span>
                      </span>
                      <span className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
                        <Badge variant="secondary">{d?.title ?? a.driver}</Badge>
                        <DeployTarget server={a.server} account={a.params.account} />
                      </span>
                      <span className="min-w-0 text-xs">
                        {st ? (
                          <>
                            <span className="block truncate font-mono" title={st.current}>
                              线上 {st.current || "—"}
                            </span>
                            <span className="block truncate text-muted-foreground" title={st.subject}>
                              本地 {st.branch} {st.localCommit}
                              {st.dirty > 0 && `（${st.dirty} 个文件未提交）`} {st.subject}
                            </span>
                          </>
                        ) : status.isFetching ? (
                          <Skeleton className="h-8 w-full max-w-64" />
                        ) : null}
                      </span>
                      <span>{st && <AppStatusBadge status={st} />}</span>
                    </Link>
                  </li>
                );
              })}
            </ul>
          </Card>
        </div>
      )}
      {deploying && <DeployDialog apps={deploying} onClose={() => setDeploying(null)} />}
    </>
  );
}
