import { useQueryClient } from "@tanstack/react-query";
import { Boxes, ChevronRight, GitBranch, Pencil, Plus, RefreshCw, Rocket, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { toast } from "sonner";
import { ConfirmButton } from "@/components/common/ConfirmButton";
import { DeployTarget } from "@/components/common/DeployTarget";
import { EmptyState, PageHeader } from "@/components/layout/Page";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { DriverIcon } from "@/features/app-form/DriverPicker";
import { AppStatusBadge } from "@/features/deploy/AppStatusBadge";
import { DeployDialog } from "@/features/deploy/DeployDialog";
import { useCatalog, useProject, useStatus } from "@/hooks/queries";
import { http } from "@/lib/api";
import { timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import { ProjectDialog } from "./ProjectsPage";

export function ProjectPage() {
  const { project: name = "" } = useParams();
  const { data, isLoading, error } = useProject(name);
  const { data: catalog } = useCatalog();
  const status = useStatus(name);
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [editing, setEditing] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [deploying, setDeploying] = useState<string[] | null>(null);

  // 按部署顺序排列，和批量部署的执行顺序一致
  const apps = useMemo(() => {
    const order = (a: { params: Record<string, unknown> }) => (typeof a.params.order === "number" ? a.params.order : 100);
    return [...(data?.apps ?? [])].sort((a, b) => order(a) - order(b) || a.name.localeCompare(b.name));
  }, [data]);
  const groups = useMemo(() => [...new Set(apps.flatMap((a) => (a.params.groups as string[] | undefined) ?? []))].sort(), [apps]);
  const statusOf = (app: string) => status.data?.find((s) => s.app === app);
  const updatable = apps.filter((a) => ["has_update", "not_deployed"].includes(statusOf(a.name)?.state ?? ""));

  if (isLoading) return <Skeleton className="h-40 w-full" />;
  if (error || !data) {
    return (
      <Alert variant="destructive">
        <AlertDescription>{error?.message ?? "读取项目失败"}</AlertDescription>
      </Alert>
    );
  }
  const { project } = data;
  const driverOf = (d: string) => catalog?.drivers.find((x) => x.name === d);
  const toggle = (app: string) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(app)) n.delete(app);
      else n.add(app);
      return n;
    });
  const selectOnly = (names: string[]) => setSelected(new Set(names));
  const newApp = (
    <Button asChild variant={apps.length ? "outline" : "default"}>
      <Link to={`/projects/${name}/apps/new`}>
        <Plus />
        新建应用
      </Link>
    </Button>
  );

  return (
    <>
      <PageHeader
        title={project.title}
        description={
          <>
            <span className="font-mono">{project.name}</span>
            {project.description && <> · {project.description}</>}
          </>
        }
        actions={
          <>
            <Button variant="outline" onClick={() => setEditing(true)}>
              <Pencil />
              编辑
            </Button>
            <ConfirmButton
              trigger={
                <Button variant="outline">
                  <Trash2 />
                  删除
                </Button>
              }
              title={`删除项目 ${project.title}？`}
              description={apps.length ? `项目里还有 ${apps.length} 个应用，需要先删除它们。` : "删除后无法恢复。"}
              confirmText="删除"
              destructive
              onConfirm={async () => {
                await http.del(`/api/projects/${name}`);
                await qc.invalidateQueries({ queryKey: ["projects"] });
                toast.success("已删除项目");
                navigate("/projects");
              }}
            />
            {newApp}
          </>
        }
      />

      {apps.length === 0 ? (
        <EmptyState icon={Boxes} title="还没有应用" description="应用是独立部署、独立回滚的单元，如一个后端服务或一个前端站点。" action={newApp} />
      ) : (
        <>
          {/* 批量选择和部署 */}
          <div className="mb-3 flex flex-wrap items-center gap-2 text-sm">
            <span className="text-muted-foreground">选择：</span>
            <Button variant="ghost" size="xs" onClick={() => selectOnly(apps.map((a) => a.name))}>
              全部
            </Button>
            {groups.map((g) => (
              <Button
                key={g}
                variant="ghost"
                size="xs"
                onClick={() => selectOnly(apps.filter((a) => ((a.params.groups as string[] | undefined) ?? []).includes(g)).map((a) => a.name))}
              >
                分组 {g}
              </Button>
            ))}
            <Button variant="ghost" size="xs" disabled={!updatable.length} onClick={() => selectOnly(updatable.map((a) => a.name))}>
              有更新的（{updatable.length}）
            </Button>
            {selected.size > 0 && (
              <Button variant="ghost" size="xs" onClick={() => setSelected(new Set())}>
                清除
              </Button>
            )}
            <span className="flex-1" />
            <Button variant="ghost" size="sm" onClick={() => status.refetch()} disabled={status.isFetching}>
              <RefreshCw className={cn(status.isFetching && "animate-spin")} />
              刷新状态
            </Button>
            <Button size="sm" disabled={selected.size === 0} onClick={() => setDeploying(apps.filter((a) => selected.has(a.name)).map((a) => `${name}/${a.name}`))}>
              <Rocket />
              部署选中的{selected.size ? `（${selected.size}）` : ""}
            </Button>
          </div>

          <Card className="gap-0 overflow-hidden py-0">
            <ul className="divide-y">
              {apps.map((a) => {
                const d = driverOf(a.driver);
                const repo = String(a.params.repo_path ?? "");
                const appGroups = (a.params.groups as string[] | undefined) ?? [];
                const st = statusOf(a.name);
                return (
                  <li key={a.name} className={cn("flex items-stretch", selected.has(a.name) && "bg-accent/40")}>
                    <label className="flex cursor-pointer items-center pl-4" aria-label={`选择 ${a.title}`}>
                      <input type="checkbox" className="size-4 accent-primary" checked={selected.has(a.name)} onChange={() => toggle(a.name)} />
                    </label>
                    <Link
                      to={`/projects/${name}/apps/${a.name}`}
                      className="grid min-w-0 flex-1 gap-2 px-4 py-3 transition-colors hover:bg-accent/40 md:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)_minmax(0,1.3fr)_minmax(0,1.2fr)_auto] md:items-center"
                    >
                      <div className="flex min-w-0 items-center gap-3">
                        <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary">
                          <DriverIcon name={d?.icon ?? ""} className="size-4" />
                        </span>
                        <div className="min-w-0">
                          <p className="truncate text-sm font-medium">{a.title}</p>
                          <p className="truncate font-mono text-xs text-muted-foreground">{a.name}</p>
                        </div>
                      </div>
                      <div className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
                        <Badge variant="secondary">{d?.title ?? a.driver}</Badge>
                        <DeployTarget server={a.server} account={a.params.account} />
                        {appGroups.map((g) => (
                          <Badge key={g} variant="outline" className="font-normal">
                            {g}
                          </Badge>
                        ))}
                      </div>
                      <div className="min-w-0 text-xs text-muted-foreground">
                        <p className="truncate font-mono" title={repo}>
                          {repo}
                        </p>
                        <p className="flex items-center gap-1">
                          <GitBranch className="size-3" />
                          {String(a.params.branch ?? "")} · 第 {a.revision} 版 · {timeAgo(a.updatedAt)}修改
                        </p>
                      </div>
                      <div className="min-w-0 space-y-0.5 text-xs">
                        {st ? (
                          <>
                            <AppStatusBadge status={st} />
                            <p className="truncate font-mono text-muted-foreground" title={st.error || st.current}>
                              {st.current || st.error || "—"}
                            </p>
                          </>
                        ) : status.isFetching ? (
                          <Skeleton className="h-8 w-32" />
                        ) : null}
                      </div>
                      <ChevronRight className="hidden size-4 text-muted-foreground md:block" />
                    </Link>
                  </li>
                );
              })}
            </ul>
          </Card>
        </>
      )}
      {editing && <ProjectDialog project={project} onClose={() => setEditing(false)} />}
      {deploying && (
        <DeployDialog
          apps={deploying}
          onClose={() => {
            setDeploying(null);
            setSelected(new Set());
          }}
        />
      )}
    </>
  );
}
