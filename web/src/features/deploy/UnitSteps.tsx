import { CheckCircle2, Circle, Loader2, XCircle } from "lucide-react";
import type { JobEvent, JobUnit } from "@/lib/types";
import { cn } from "@/lib/utils";

const expected = {
  deploy: ["预检", "准备服务器", "构建", "上传", "切换", "健康检查", "清理"],
  rollback: ["预检", "切换", "健康检查"],
};

interface StepInfo {
  name: string;
  status: "pending" | "running" | "done" | "fail";
  ms?: number;
}

/** 从事件里算出一个应用的各步骤状态；还在执行时，后面没到的步骤显示为等待。
 * deploySteps 是部署方式的步骤名称（预检之后），不传时用部署到服务器的默认步骤。 */
export function stepsOf(events: JobEvent[], unit: JobUnit, kind: "deploy" | "rollback", deploySteps?: string[]): StepInfo[] {
  const list: StepInfo[] = [];
  for (const e of events) {
    if (e.unit !== unit.seq || !e.type.startsWith("step_")) continue;
    if (e.type === "step_start") list.push({ name: e.message, status: "running" });
    const cur = [...list].reverse().find((s) => s.name === e.step);
    if (!cur) continue;
    if (e.type === "step_done") {
      cur.status = "done";
      cur.ms = Number(e.message);
    } else if (e.type === "step_fail") cur.status = "fail";
  }
  if (unit.status === "pending" || unit.status === "running") {
    const names = kind === "deploy" && deploySteps?.length ? ["预检", ...deploySteps] : expected[kind];
    for (const name of names) if (!list.some((s) => s.name === name)) list.push({ name, status: "pending" });
  }
  // 已结束的任务里还标着执行中的步骤（被取消或中断），不再转圈
  if (unit.status !== "running") for (const s of list) if (s.status === "running") s.status = "fail";
  return list;
}

export function UnitSteps({ steps }: { steps: StepInfo[] }) {
  return (
    <ol className="flex flex-wrap gap-x-1 gap-y-2">
      {steps.map((s, i) => (
        <li key={i} className="flex items-center gap-1">
          {i > 0 && <span className="mx-1 h-px w-3 bg-border" />}
          <span
            className={cn(
              "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs",
              s.status === "done" && "border-success/40 text-success",
              s.status === "fail" && "border-destructive/50 text-destructive",
              s.status === "running" && "border-primary/50 text-primary",
              s.status === "pending" && "text-muted-foreground",
            )}
          >
            {s.status === "done" ? (
              <CheckCircle2 className="size-3" />
            ) : s.status === "fail" ? (
              <XCircle className="size-3" />
            ) : s.status === "running" ? (
              <Loader2 className="size-3 animate-spin" />
            ) : (
              <Circle className="size-3" />
            )}
            {s.name}
            {s.ms !== undefined && Number.isFinite(s.ms) && <span className="text-muted-foreground">{(s.ms / 1000).toFixed(1)}s</span>}
          </span>
        </li>
      ))}
    </ol>
  );
}
