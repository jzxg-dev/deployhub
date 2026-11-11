import { History } from "lucide-react";
import { Link } from "react-router";
import { EmptyState, PageHeader } from "@/components/layout/Page";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { StatusBadge, duration, kindLabel, initiatorLabel } from "@/features/deploy/status";
import { useJobs } from "@/hooks/queries";
import { formatTime } from "@/lib/format";
import type { Job } from "@/lib/types";

/** 任务列表，部署记录页和应用详情页共用。 */
export function JobList({ jobs }: { jobs: Job[] }) {
  return (
    <Card className="gap-0 overflow-hidden py-0">
      <ul className="divide-y">
        {jobs.map((j) => (
          <li key={j.id}>
            <Link to={`/jobs/${j.id}`} className="grid gap-1 px-4 py-3 hover:bg-accent/40 md:grid-cols-[5rem_6rem_minmax(0,1fr)_auto] md:items-center md:gap-3">
              <span className="font-mono text-xs text-muted-foreground">#{j.id}</span>
              <StatusBadge status={j.status} />
              <span className="min-w-0 text-sm">
                <span className="mr-2 text-muted-foreground">{kindLabel[j.kind]}</span>
                {j.units.map((u, i) => (
                  <span key={u.seq}>
                    {i > 0 && "、"}
                    <span className="font-medium">{u.title}</span>
                    {u.version && <span className="ml-1 font-mono text-xs text-muted-foreground">{u.version}</span>}
                  </span>
                ))}
              </span>
              <span className="text-xs text-muted-foreground">
                {formatTime(j.createdAt)}
                {/* 导入的历史记录没有耗时（开始和结束时间相同），不显示 */}
                {j.startedAt && j.startedAt !== j.finishedAt && ` · ${duration(j.startedAt, j.finishedAt)}`}
                {j.initiator !== "page" && ` · ${initiatorLabel(j.initiator)}`}
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </Card>
  );
}

export function JobsPage() {
  const { data: jobs, isLoading } = useJobs();
  return (
    <>
      <PageHeader title="部署记录" description="所有部署和回滚任务，新的在前。" />
      {isLoading ? (
        <Skeleton className="h-40 w-full" />
      ) : !jobs?.length ? (
        <EmptyState icon={History} title="还没有部署记录" description="在应用页面点「部署」后，任务会出现在这里。" />
      ) : (
        <JobList jobs={jobs} />
      )}
    </>
  );
}
