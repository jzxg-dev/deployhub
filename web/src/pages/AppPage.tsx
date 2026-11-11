import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, CircleSlash, History, Loader2, RefreshCw, Rocket, RotateCcw, Stethoscope, Trash2, TriangleAlert, XCircle } from "lucide-react";
import { useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { toast } from "sonner";
import { ConfirmButton } from "@/components/common/ConfirmButton";
import { PageHeader } from "@/components/layout/Page";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { AppForm } from "@/features/app-form/AppForm";
import { DriverIcon } from "@/features/app-form/DriverPicker";
import { PlanView } from "@/features/app-form/PlanView";
import { RepoCard } from "@/features/app-form/RepoCard";
import { DeployDialog } from "@/features/deploy/DeployDialog";
import { useApp, useAppState, useCatalog, useJobs, useRevisions } from "@/hooks/queries";
import { errorMessage, http } from "@/lib/api";
import { formatTime, timeAgo } from "@/lib/format";
import type { App, CheckItem, CheckResult, DriverSpec, Field, Job, Preview, RepoInfo } from "@/lib/types";
import { JobList } from "./JobsPage";
import { cn } from "@/lib/utils";

const statusIcon: Record<CheckItem["status"], { icon: typeof CheckCircle2; cls: string; text: string }> = {
  ok: { icon: CheckCircle2, cls: "text-success", text: "通过" },
  warn: { icon: TriangleAlert, cls: "text-warning", text: "提醒" },
  fail: { icon: XCircle, cls: "text-destructive", text: "不通过" },
  skip: { icon: CircleSlash, cls: "text-muted-foreground", text: "跳过" },
};

function CheckList({ result }: { result: CheckResult }) {
  const groups = [...new Set(result.items.map((i) => i.group))];
  const count = (s: CheckItem["status"]) => result.items.filter((i) => i.status === s).length;
  return (
    <div className="space-y-4">
      <div className={cn("flex flex-wrap items-center gap-2 rounded-md px-3 py-2 text-sm", result.ok ? "bg-success/10" : "bg-destructive/10")}>
        {result.ok ? <CheckCircle2 className="size-4 text-success" /> : <XCircle className="size-4 text-destructive" />}
        <span className="font-medium">{result.ok ? "可以部署" : "有问题需要处理"}</span>
        <span className="text-xs text-muted-foreground">
          {count("ok")} 项通过 · {count("warn")} 项提醒 · {count("fail")} 项不通过{count("skip") ? ` · ${count("skip")} 项跳过` : ""} · 用时 {(result.durationMs / 1000).toFixed(1)} 秒
        </span>
      </div>
      {groups.map((g) => (
        <div key={g} className="space-y-1.5">
          <p className="text-xs font-medium text-muted-foreground">{g}</p>
          <ul className="space-y-1">
            {result.items
              .filter((i) => i.group === g)
              .map((i, idx) => {
                const s = statusIcon[i.status];
                return (
                  <li key={idx} className="flex gap-2 text-sm">
                    <s.icon className={cn("mt-0.5 size-4 shrink-0", s.cls)} aria-label={s.text} />
                    <span className="shrink-0 font-medium">{i.name}</span>
                    <span className="min-w-0 break-words text-muted-foreground">{i.message}</span>
                  </li>
                );
              })}
          </ul>
        </div>
      ))}
    </div>
  );
}

/** 线上状态：当前版本、最后一条部署记录、保留的版本（可以回滚）。 */
function OnlineState({ app }: { app: App }) {
  const navigate = useNavigate();
  const state = useAppState(app.project, app.name);
  const st = state.data;
  // 没有服务器的部署方式（如 Workers）读的是云平台上的版本
  const cloud = !app.server;
  return (
    <Card className="gap-4">
      <CardHeader className="flex items-start justify-between gap-2">
        <div className="space-y-1.5">
          <CardTitle className="text-base">线上状态</CardTitle>
          <CardDescription>{cloud ? "读取 Cloudflare 上 100% 流量的版本和最近的版本。" : "读取服务器上的 current 和保留的版本。"}</CardDescription>
        </div>
        <Button variant="ghost" size="icon-sm" aria-label="刷新" onClick={() => state.refetch()} disabled={state.isFetching}>
          <RefreshCw className={cn(state.isFetching && "animate-spin")} />
        </Button>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        {state.isLoading ? (
          <Skeleton className="h-20 w-full" />
        ) : state.error ? (
          <p className="text-destructive">{errorMessage(state.error)}</p>
        ) : !st?.reachable ? (
          <p className="text-destructive">读取失败：{st?.error}</p>
        ) : (
          <>
            {!!st.busy && (
              <Link to={`/jobs/${st.busy}`} className="flex items-center gap-1.5 rounded-md bg-primary/10 px-3 py-2 text-primary hover:underline">
                <Loader2 className="size-4 animate-spin" />
                正在部署（任务 #{st.busy}），点击查看
              </Link>
            )}
            <div>
              <p className="text-xs text-muted-foreground">当前版本</p>
              <p className="font-mono text-sm break-all">{st.current || "（还没有部署过）"}</p>
              {st.last && (
                <p className="mt-1 text-xs text-muted-foreground">
                  最后一次：{st.last.time} {st.last.action === "rollback" ? "回滚" : "部署"} {st.last.version}，
                  {{ success: "成功", failed: "失败", rolled_back: "已回滚" }[st.last.result] ?? st.last.result}
                  {st.last.operator && `（${st.last.operator}）`}
                </p>
              )}
            </div>
            {st.releases.length > 0 && (
              <div className="space-y-1.5">
                <p className="text-xs text-muted-foreground">{cloud ? "Cloudflare 上最近的版本（新的在前）" : "服务器上保留的版本（新的在前）"}</p>
                <ul className="divide-y rounded-md border">
                  {st.releases.map((v) => (
                    <li key={v} className="flex items-center justify-between gap-2 px-3 py-1.5">
                      <span className="min-w-0 truncate font-mono text-xs">{v}</span>
                      {v === st.current ? (
                        <Badge>当前</Badge>
                      ) : (
                        <ConfirmButton
                          trigger={
                            <Button variant="ghost" size="xs" disabled={!!st.busy}>
                              <RotateCcw />
                              回滚到这个版本
                            </Button>
                          }
                          title={`回滚到 ${v}？`}
                          description={
                            cloud
                              ? `通过 Cloudflare 接口把 ${v} 设为 100% 流量，然后做健康检查；检查失败会切回 ${st.current || "原来的版本"}。不需要重新构建。`
                              : `切换到 ${v} 并重启服务，然后做健康检查；检查失败会切回 ${st.current || "原来的版本"}。不需要重新构建。`
                          }
                          confirmText="回滚"
                          onConfirm={async () => {
                            const job = await http.post<Job>("/api/rollbacks", { app: `${app.project}/${app.name}`, version: v });
                            navigate(`/jobs/${job.id}`);
                          }}
                        />
                      )}
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}

function Overview({ app, driver }: { app: App; driver?: DriverSpec }) {
  const qc = useQueryClient();
  // 代码来源是 git 地址时显示地址，仓库信息是平台上次拉取下来的内容
  const managed = app.params.repo_source === "git";
  const repoPath = String((managed ? app.params.repo_url : app.params.repo_path) ?? "");
  const repo = useQuery({
    queryKey: ["projects", app.project, "apps", app.name, "repo", app.revision],
    queryFn: () => http.get<RepoInfo>(`/api/projects/${app.project}/apps/${app.name}/repo`),
  });
  const preview = useQuery({
    queryKey: ["preview", app.project, app.name, "saved", app.revision],
    queryFn: () => http.post<Preview>(`/api/projects/${app.project}/apps/${app.name}/preview`, {}),
  });
  const check = useMutation({
    mutationFn: () => http.post<CheckResult>(`/api/projects/${app.project}/apps/${app.name}/check`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["servers"] }),
    onError: (e) => toast.error(errorMessage(e)),
  });
  const groups = (app.params.groups as string[] | undefined) ?? [];
  const info: [string, React.ReactNode][] = [
    ["部署方式", driver?.title ?? app.driver],
    app.server
      ? [
          "服务器",
          <Link key="s" to="/servers" className="text-primary hover:underline">
            {app.server}
          </Link>,
        ]
      : [
          "账号",
          <Link key="s" to="/credentials" className="text-primary hover:underline">
            {String(app.params.account ?? "—")}
          </Link>,
        ],
    ["部署分支", String(app.params.branch ?? "")],
    ["分组", groups.length ? groups.join("、") : "—"],
    ["配置版本", `第 ${app.revision} 版 · ${timeAgo(app.updatedAt)}修改`],
    ["创建时间", formatTime(app.createdAt)],
  ];

  return (
    <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
      <div className="min-w-0 space-y-4">
        <OnlineState app={app} />
        <Card className="gap-4">
          <CardHeader>
            <CardTitle className="text-base">基本信息</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-sm">
              {info.map(([k, v]) => (
                <div key={k} className="contents">
                  <dt className="text-muted-foreground">{k}</dt>
                  <dd className="min-w-0 break-words">{v}</dd>
                </div>
              ))}
            </dl>
            <div className="space-y-1.5">
              <p className="text-sm text-muted-foreground">仓库</p>
              <p className="font-mono text-xs break-all">{repoPath}</p>
              <RepoCard repo={repo.data} loading={repo.isFetching} />
            </div>
          </CardContent>
        </Card>
        <Card className="gap-4">
          <CardHeader className="flex flex-wrap items-start justify-between gap-2">
            <div className="space-y-1.5">
              <CardTitle className="text-base">部署前检查</CardTitle>
              <CardDescription>
                {app.server ? "检查本机仓库和工具、服务器连接、目录权限、必需文件等。只读，不改动服务器。" : "检查本机仓库和工具、云平台账号和线上状态。只读，不做改动。"}
              </CardDescription>
            </div>
            <Button variant="outline" onClick={() => check.mutate()} disabled={check.isPending}>
              {check.isPending ? <Loader2 className="animate-spin" /> : <Stethoscope />}
              {check.data ? "重新检查" : "检查"}
            </Button>
          </CardHeader>
          {(check.data || check.isPending) && (
            <CardContent>{check.data ? <CheckList result={check.data} /> : <Skeleton className="h-32 w-full" />}</CardContent>
          )}
        </Card>
      </div>
      <Card className="min-w-0 gap-4">
        <CardHeader>
          <CardTitle className="text-base">部署计划</CardTitle>
          <CardDescription>按当前保存的配置，部署时会执行这些命令。</CardDescription>
        </CardHeader>
        <CardContent>
          {preview.data ? <PlanView preview={preview.data} /> : preview.error ? <p className="text-sm text-destructive">{errorMessage(preview.error)}</p> : <Skeleton className="h-64 w-full" />}
        </CardContent>
      </Card>
    </div>
  );
}

/** 两个版本之间改了哪些参数。 */
function diffParams(fields: Field[], before: Record<string, unknown>, after: Record<string, unknown>) {
  const keys = fields.map((f) => f.key).filter((k) => JSON.stringify(before[k]) !== JSON.stringify(after[k]));
  return keys.map((k) => ({ field: fields.find((f) => f.key === k)!, before: before[k], after: after[k] }));
}

function show(v: unknown, field: Field): string {
  if (v === undefined) return "（未填，用自动值）";
  if (typeof v === "string") {
    if (field.type === "code" || v.includes("\n")) return `（${v.split("\n").length} 行内容）`;
    return v === "" ? "（空）" : v;
  }
  return JSON.stringify(v);
}

function Revisions({ app, fields }: { app: App; fields: Field[] }) {
  const qc = useQueryClient();
  const { data: revs, isLoading } = useRevisions(app.project, app.name);
  if (isLoading) return <Skeleton className="h-40 w-full" />;
  return (
    <Card className="gap-0 py-0">
      <ol className="divide-y">
        {revs?.map((r, i) => {
          const prev = revs[i + 1];
          const changes = prev ? diffParams(fields, prev.params, r.params) : [];
          return (
            <li key={r.revision} className="space-y-2 px-4 py-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <p className="flex items-center gap-2 text-sm">
                  <History className="size-4 text-muted-foreground" />
                  <span className="font-medium">第 {r.revision} 版</span>
                  {r.revision === app.revision && <Badge>当前</Badge>}
                  <span className="text-xs text-muted-foreground">{formatTime(r.createdAt)}</span>
                </p>
                {r.revision !== app.revision && (
                  <ConfirmButton
                    trigger={
                      <Button variant="outline" size="sm">
                        <RotateCcw />
                        恢复到这个版本
                      </Button>
                    }
                    title={`恢复到第 ${r.revision} 版？`}
                    description={`用第 ${r.revision} 版的参数保存为第 ${app.revision + 1} 版，当前版本仍保留在修改记录里。`}
                    confirmText="恢复"
                    onConfirm={async () => {
                      await http.post(`/api/projects/${app.project}/apps/${app.name}/revisions/${r.revision}/restore`);
                      await qc.invalidateQueries({ queryKey: ["projects", app.project] });
                      toast.success(`已恢复到第 ${r.revision} 版的配置`);
                    }}
                  />
                )}
              </div>
              {!prev ? (
                <p className="text-xs text-muted-foreground">创建应用</p>
              ) : changes.length === 0 ? (
                <p className="text-xs text-muted-foreground">参数没有变化</p>
              ) : (
                <ul className="space-y-1 text-xs">
                  {changes.map((c) => (
                    <li key={c.field.key} className="grid gap-1 sm:grid-cols-[8rem_minmax(0,1fr)]">
                      <span className="text-muted-foreground">{c.field.label}</span>
                      <span className="min-w-0 font-mono break-all">
                        <span className="text-destructive line-through decoration-destructive/50">{show(c.before, c.field)}</span>
                        <span className="mx-1.5 text-muted-foreground">→</span>
                        <span className="text-success">{show(c.after, c.field)}</span>
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </li>
          );
        })}
      </ol>
    </Card>
  );
}

export function AppPage() {
  const { project = "", app: name = "" } = useParams();
  const [params, setParams] = useSearchParams();
  const tab = params.get("tab") ?? "overview";
  const { data: app, isLoading, error } = useApp(project, name);
  const { data: catalog } = useCatalog();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [deploying, setDeploying] = useState(false);
  const jobs = useJobs({ project, app: name });

  if (isLoading || !catalog) return <Skeleton className="h-64 w-full" />;
  if (error || !app) {
    return (
      <Alert variant="destructive">
        <AlertDescription>{error?.message ?? "读取应用失败"}</AlertDescription>
      </Alert>
    );
  }
  const driver = catalog.drivers.find((d) => d.name === app.driver);
  const fields = [...catalog.common.filter((f) => !driver?.omit?.includes(f.key)), ...(driver?.fields ?? [])];

  return (
    <>
      <PageHeader
        title={app.title}
        description={
          <span className="inline-flex flex-wrap items-center gap-1.5">
            <Link to={`/projects/${project}`} className="hover:text-foreground hover:underline">
              {project}
            </Link>
            <span>/</span>
            <span className="font-mono">{app.name}</span>
            <span>·</span>
            <DriverIcon name={driver?.icon ?? ""} className="size-4 text-primary" />
            {driver?.title}
          </span>
        }
        actions={
          <>
            <ConfirmButton
              trigger={
                <Button variant="outline">
                  <Trash2 />
                  删除
                </Button>
              }
              title={`删除应用 ${app.title}？`}
              description="删除平台里的应用配置和修改记录，不影响已经部署的程序。"
              confirmText="删除"
              destructive
              onConfirm={async () => {
                await http.del(`/api/projects/${project}/apps/${name}`);
                await qc.invalidateQueries({ queryKey: ["projects"] });
                toast.success("已删除应用");
                navigate(`/projects/${project}`);
              }}
            />
            <Button onClick={() => setDeploying(true)}>
              <Rocket />
              部署
            </Button>
          </>
        }
      />
      <Tabs value={tab} onValueChange={(v) => setParams(v === "overview" ? {} : { tab: v }, { replace: true })}>
        <TabsList className="mb-4">
          <TabsTrigger value="overview">概览</TabsTrigger>
          <TabsTrigger value="jobs">部署记录</TabsTrigger>
          <TabsTrigger value="config">配置</TabsTrigger>
          <TabsTrigger value="history">修改记录</TabsTrigger>
        </TabsList>
        <TabsContent value="overview">
          <Overview app={app} driver={driver} />
        </TabsContent>
        <TabsContent value="jobs">
          {jobs.isLoading ? (
            <Skeleton className="h-32 w-full" />
          ) : jobs.data?.length ? (
            <JobList jobs={jobs.data} />
          ) : (
            <p className="text-sm text-muted-foreground">还没有部署过。</p>
          )}
        </TabsContent>
        <TabsContent value="config">
          {driver ? (
            // key：保存或恢复后版本号变了，用新数据重建表单
            <AppForm
              key={app.revision}
              catalog={catalog}
              driver={driver}
              project={project}
              app={app}
              onSaved={() => qc.invalidateQueries({ queryKey: ["projects", project, "apps", name] })}
            />
          ) : (
            <Alert variant="destructive">
              <AlertDescription>不认识的部署方式：{app.driver}</AlertDescription>
            </Alert>
          )}
        </TabsContent>
        <TabsContent value="history">
          <Revisions app={app} fields={fields} />
        </TabsContent>
      </Tabs>
      {deploying && <DeployDialog apps={[`${project}/${name}`]} onClose={() => setDeploying(false)} />}
    </>
  );
}
