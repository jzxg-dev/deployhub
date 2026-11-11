import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { AlertTriangle, ArrowRight, GitCommitHorizontal, Loader2, Rocket, XCircle } from "lucide-react";
import { useNavigate } from "react-router";
import { toast } from "sonner";
import { DeployTarget } from "@/components/common/DeployTarget";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { useCatalog } from "@/hooks/queries";
import { errorMessage, http } from "@/lib/api";
import type { DeployPlan, Job } from "@/lib/types";

/** 部署确认：服务器、预计版本、线上版本、提醒，确认后开始部署并跳到任务页。 */
export function DeployDialog({ apps, onClose }: { apps: string[]; onClose: () => void }) {
  const navigate = useNavigate();
  const { data: catalog } = useCatalog();
  const [continueOnError, setContinueOnError] = useState(false);
  const plan = useQuery({
    queryKey: ["deploy-plan", ...apps],
    queryFn: () => http.post<DeployPlan>("/api/deploys/plan", { apps }),
    gcTime: 0,
  });
  const start = useMutation({
    mutationFn: () => http.post<Job>("/api/deploys", { apps, continueOnError }),
    onSuccess: (job) => {
      onClose();
      navigate(`/jobs/${job.id}`);
    },
    onError: (e) => toast.error(errorMessage(e)),
  });

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>确认部署{apps.length > 1 ? ` ${apps.length} 个应用` : ""}</DialogTitle>
          <DialogDescription>
            部署时先同步代码（以本地为准），实际版本号以执行时为准。健康检查失败会自动回滚到上一个版本。
            {apps.length > 1 && " 多个应用按部署顺序依次执行。"}
          </DialogDescription>
        </DialogHeader>
        {plan.isLoading ? (
          <div className="space-y-2">
            <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" />
              正在读取线上版本…
            </p>
            <Skeleton className="h-24 w-full" />
          </div>
        ) : plan.error ? (
          <p className="text-sm text-destructive">{errorMessage(plan.error)}</p>
        ) : (
          plan.data && (
            <div className="space-y-3">
              {plan.data.items.map((it) => (
                <div key={`${it.project}/${it.app}`} className="space-y-2 rounded-lg border p-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <p className="text-sm font-medium">
                      {it.title} <span className="font-mono text-xs text-muted-foreground">{it.project}/{it.app}</span>
                    </p>
                    <DeployTarget server={it.server} account={catalog?.drivers.find((d) => d.name === it.driver)?.title} className="text-xs text-muted-foreground" />
                  </div>
                  <div className="flex flex-wrap items-center gap-2 font-mono text-xs">
                    <span className="rounded bg-muted px-1.5 py-0.5">{it.online || "（未部署）"}</span>
                    <ArrowRight className="size-3.5 text-muted-foreground" />
                    <span className="rounded bg-primary/10 px-1.5 py-0.5 text-primary">{it.version || "—"}</span>
                  </div>
                  {it.commit && (
                    <p className="flex min-w-0 items-center gap-1 text-xs text-muted-foreground">
                      <GitCommitHorizontal className="size-3.5 shrink-0" />
                      <span className="shrink-0">{it.branch}</span>
                      <span className="truncate">{it.subject}</span>
                    </p>
                  )}
                  {it.blocked && (
                    <p className="flex gap-1.5 text-xs text-destructive">
                      <XCircle className="mt-0.5 size-3.5 shrink-0" />
                      不能部署：{it.blocked}
                    </p>
                  )}
                  {it.warnings.map((w) => (
                    <p key={w} className="flex gap-1.5 text-xs">
                      <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-warning" />
                      {w}
                    </p>
                  ))}
                </div>
              ))}
              {plan.data.warnings.map((w) => (
                <p key={w} className="flex gap-1.5 rounded-md bg-warning/10 px-3 py-2 text-xs">
                  <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-warning" />
                  {w}
                </p>
              ))}
            </div>
          )
        )}
        {apps.length > 1 && (
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" className="size-4 accent-primary" checked={continueOnError} onChange={(e) => setContinueOnError(e.target.checked)} />
            某个应用失败后继续部署剩下的（默认停止，剩下的跳过）
          </label>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button onClick={() => start.mutate()} disabled={!plan.data?.ok || start.isPending}>
            {start.isPending ? <Loader2 className="animate-spin" /> : <Rocket />}
            开始部署
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
