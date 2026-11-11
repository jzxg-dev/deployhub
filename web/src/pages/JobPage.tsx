import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, Check, CircleStop, Hourglass, Loader2, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useParams } from "react-router";
import { toast } from "sonner";
import { ConfirmButton } from "@/components/common/ConfirmButton";
import { DeployTarget } from "@/components/common/DeployTarget";
import { PageHeader } from "@/components/layout/Page";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { JobLog } from "@/features/deploy/JobLog";
import { StatusBadge, StatusIcon, duration, initiatorLabel, isActive, kindLabel } from "@/features/deploy/status";
import { UnitSteps, stepsOf } from "@/features/deploy/UnitSteps";
import { useEventStream, useServerEvent } from "@/hooks/events";
import { useCatalog, useJob } from "@/hooks/queries";
import { errorMessage, http } from "@/lib/api";
import { formatTime } from "@/lib/format";
import type { JobEvent } from "@/lib/types";

/** 合并事件：按编号去重排序（实时推送和补齐的请求可能重叠）。 */
function merge(a: JobEvent[], b: JobEvent[]): JobEvent[] {
  if (b.length === 0) return a;
  const last = a.length ? a[a.length - 1].id : 0;
  if (b.every((e) => e.id > last)) return [...a, ...b];
  const map = new Map<number, JobEvent>();
  for (const e of [...a, ...b]) map.set(e.id, e);
  return [...map.values()].sort((x, y) => x.id - y.id);
}

export function JobPage() {
  const { data: catalog } = useCatalog();
  const driverOf = (name: string) => catalog?.drivers.find((d) => d.name === name);
  const id = Number(useParams().id);
  const job = useJob(id);
  const { status: stream } = useEventStream();
  const [events, setEvents] = useState<JobEvent[]>([]);
  const [unit, setUnit] = useState("all");
  const [, tick] = useState(0);

  const lastId = events.length ? events[events.length - 1].id : 0;
  const lastIdRef = useRef(0);
  lastIdRef.current = lastId;
  const loadMore = useCallback(
    (after: number) =>
      http
        .get<JobEvent[]>(`/api/jobs/${id}/events?after=${after}`)
        .then((evs) => setEvents((cur) => merge(cur, evs)))
        .catch(() => undefined),
    [id],
  );

  useEffect(() => {
    setEvents([]);
    void loadMore(0);
  }, [loadMore]);

  // 实时推送
  useServerEvent<JobEvent>("job_event", (e) => {
    if (e.jobId === id) setEvents((cur) => merge(cur, [e]));
  });

  const active = job.data ? isActive(job.data.status) : false;
  // 实时连接恢复（如标签页回到前台）时补齐断开期间的事件
  useEffect(() => {
    if (stream === "open") void loadMore(lastIdRef.current);
  }, [stream, loadMore]);
  // 实时连接断开时，执行中的任务每 3 秒补齐一次
  useEffect(() => {
    if (!active || stream === "open") return;
    const t = setInterval(() => {
      void job.refetch();
      void loadMore(lastId);
    }, 3000);
    return () => clearInterval(t);
  }, [active, stream, lastId, job, loadMore]);
  // 执行中时每秒刷新一次耗时
  useEffect(() => {
    if (!active) return;
    const t = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, [active]);

  const cancel = useMutation({
    mutationFn: () => http.post(`/api/jobs/${id}/cancel`),
    onSuccess: () => toast.success("已请求取消；已经开始切换的应用会把切换和健康检查做完"),
    onError: (e) => toast.error(errorMessage(e)),
  });

  // 待审批的任务：批准后开始执行，拒绝后结束
  const qc = useQueryClient();
  const decide = useMutation({
    mutationFn: (action: "approve" | "reject") => http.post(`/api/jobs/${id}/${action}`),
    onSuccess: async (_, action) => {
      await qc.invalidateQueries({ queryKey: ["jobs"] });
      toast.success(action === "approve" ? "已批准，开始执行" : "已拒绝，任务没有执行");
    },
    onError: (e) => toast.error(errorMessage(e)),
  });

  const unitNames = useMemo(() => Object.fromEntries((job.data?.units ?? []).map((u) => [u.seq, u.app])), [job.data]);
  const shownEvents = useMemo(() => (unit === "all" ? events : events.filter((e) => e.unit === Number(unit) || e.unit === undefined)), [events, unit]);

  if (job.isLoading) return <Skeleton className="h-64 w-full" />;
  if (job.error || !job.data) {
    return (
      <Alert variant="destructive">
        <AlertDescription>{job.error ? errorMessage(job.error) : "读取任务失败"}</AlertDescription>
      </Alert>
    );
  }
  const j = job.data;
  const multi = j.units.length > 1;

  return (
    <>
      <PageHeader
        title={`${kindLabel[j.kind]}任务 #${j.id}`}
        description={
          <span className="inline-flex flex-wrap items-center gap-x-3 gap-y-1">
            <StatusBadge status={j.status} />
            <span>{formatTime(j.createdAt)} 发起</span>
            <span>发起方：{initiatorLabel(j.initiator)}</span>
            {j.startedAt && <span>用时 {duration(j.startedAt, j.finishedAt)}</span>}
          </span>
        }
        actions={
          active && (
            <ConfirmButton
              trigger={
                <Button variant="outline" disabled={cancel.isPending}>
                  {cancel.isPending ? <Loader2 className="animate-spin" /> : <CircleStop />}
                  取消任务
                </Button>
              }
              title="取消这个任务？"
              description="构建和上传阶段会直接中止；已经开始切换版本的应用会把切换和健康检查做完再停，不会停在一半。"
              confirmText="取消任务"
              destructive
              onConfirm={() => cancel.mutateAsync()}
            />
          )
        }
      />

      {j.status === "pending_approval" && (
        <Card className="mb-4 gap-0 border-warning/50 py-4">
          <CardContent className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex min-w-0 items-start gap-2">
              <Hourglass className="mt-0.5 size-4 shrink-0 text-warning" />
              <div className="min-w-0 space-y-1">
                <p className="text-sm font-medium">
                  {initiatorLabel(j.initiator)}请求{kindLabel[j.kind]}，等你批准
                </p>
                <p className="text-xs leading-relaxed text-muted-foreground">
                  批准后立刻开始执行，执行前会重新预检；拒绝则不执行。想让某个应用不用审批，可以在它的配置里关掉「AI 部署需要审批」。
                </p>
              </div>
            </div>
            <div className="flex shrink-0 gap-2">
              <Button variant="outline" disabled={decide.isPending} onClick={() => decide.mutate("reject")}>
                <X />
                拒绝
              </Button>
              <Button disabled={decide.isPending} onClick={() => decide.mutate("approve")}>
                {decide.isPending ? <Loader2 className="animate-spin" /> : <Check />}
                批准并执行
              </Button>
            </div>
          </CardContent>
        </Card>
      )}

      <div className="mb-4 space-y-3">
        {j.units.map((u) => (
          <Card key={u.seq} className="gap-0 py-4">
            <CardContent className="space-y-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex min-w-0 items-center gap-2">
                  <StatusIcon status={u.status} />
                  <Link to={`/projects/${u.project}/apps/${u.app}`} className="truncate font-medium hover:underline">
                    {u.title}
                  </Link>
                  <span className="font-mono text-xs text-muted-foreground">
                    {u.project}/{u.app}
                  </span>
                </div>
                <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
                  <DeployTarget server={u.server} account={driverOf(u.driver)?.title} />
                  {(u.previous || u.version) && (
                    <span className="flex items-center gap-1 font-mono">
                      {u.previous || "（无）"}
                      <ArrowRight className="size-3" />
                      <span className="text-foreground">{u.version || "…"}</span>
                    </span>
                  )}
                  {u.startedAt && <span>{duration(u.startedAt, u.finishedAt)}</span>}
                  <StatusBadge status={u.status} className="text-xs" />
                </div>
              </div>
              <UnitSteps steps={stepsOf(events, u, j.kind, driverOf(u.driver)?.steps.map((s) => s.label))} />
              {u.error && <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{u.error}</p>}
            </CardContent>
          </Card>
        ))}
      </div>

      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-sm font-medium">日志</h2>
        {multi && (
          <Tabs value={unit} onValueChange={setUnit}>
            <TabsList>
              <TabsTrigger value="all">全部</TabsTrigger>
              {j.units.map((u) => (
                <TabsTrigger key={u.seq} value={String(u.seq)}>
                  {u.app}
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
        )}
      </div>
      <JobLog events={shownEvents} unitNames={multi && unit === "all" ? unitNames : undefined} />
    </>
  );
}
