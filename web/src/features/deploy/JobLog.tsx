import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import type { JobEvent } from "@/lib/types";
import { cn } from "@/lib/utils";

const MAX_LINES = 3000;

function time(iso: string) {
  return new Date(iso).toLocaleTimeString("zh-CN", { hour12: false });
}

function seconds(ms: string) {
  const n = Number(ms);
  return Number.isFinite(n) ? `${(n / 1000).toFixed(1)} 秒` : "";
}

function Line({ e, prefix }: { e: JobEvent; prefix?: string }) {
  const tag = prefix ? <span className="mr-1.5 text-muted-foreground">[{prefix}]</span> : null;
  const t = <span className="mr-2 text-muted-foreground/70 select-none">{time(e.time)}</span>;
  switch (e.type) {
    case "step_start":
      return (
        <div className="mt-2 font-semibold text-foreground">
          {t}
          {tag}▶ {e.message}
        </div>
      );
    case "step_done":
      return (
        <div className="text-success">
          {t}
          {tag}✓ {e.step} 完成（{seconds(e.message)}）
        </div>
      );
    case "step_fail":
      return (
        <div className="font-medium text-destructive">
          {t}
          {tag}✗ {e.step} 失败：{e.message}
        </div>
      );
    case "command": {
      const lines = e.message.split("\n");
      return lines.length > 3 ? (
        <details className="text-sky-600 dark:text-sky-400">
          <summary className="cursor-pointer">
            {t}
            {tag}
            {lines[0].startsWith("$") ? lines[0] : "$ " + lines[0]} <span className="text-muted-foreground">（脚本共 {lines.length} 行，点击展开）</span>
          </summary>
          <pre className="ml-6 border-l pl-3 whitespace-pre-wrap">{e.message}</pre>
        </details>
      ) : (
        <div className="whitespace-pre-wrap text-sky-600 dark:text-sky-400">
          {t}
          {tag}
          {e.message.startsWith("$") ? e.message : "$ " + e.message}
        </div>
      );
    }
    case "info":
      return (
        <div className="text-primary">
          {t}
          {tag}
          {e.message}
        </div>
      );
    case "warn":
      return (
        <div className="text-warning">
          {t}
          {tag}! {e.message}
        </div>
      );
    case "error":
      return (
        <div className="font-medium text-destructive">
          {t}
          {tag}
          {e.message}
        </div>
      );
    default:
      return (
        <div className="whitespace-pre-wrap">
          {t}
          {tag}
          {e.message}
        </div>
      );
  }
}

/** 任务日志：步骤、命令、输出。停在底部时有新内容自动滚动。 */
export function JobLog({ events, unitNames }: { events: JobEvent[]; unitNames?: Record<number, string> }) {
  const box = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const [showAll, setShowAll] = useState(false);
  const hidden = showAll ? 0 : Math.max(0, events.length - MAX_LINES);
  const shown = hidden ? events.slice(hidden) : events;

  useLayoutEffect(() => {
    const el = box.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [events.length]);
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const onScroll = () => {
      stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    };
    el.addEventListener("scroll", onScroll);
    return () => el.removeEventListener("scroll", onScroll);
  }, []);

  return (
    <div ref={box} className="h-[60vh] min-h-80 overflow-auto rounded-lg border bg-muted/30 p-3 font-mono text-xs leading-relaxed">
      {hidden > 0 && (
        <div className="mb-2 text-center">
          <Button variant="ghost" size="xs" onClick={() => setShowAll(true)}>
            前面还有 {hidden} 行，显示全部
          </Button>
        </div>
      )}
      {shown.length === 0 ? (
        <p className="text-muted-foreground">等待输出…</p>
      ) : (
        shown.map((e) => <Line key={e.id} e={e} prefix={unitNames && e.unit !== undefined ? unitNames[e.unit] : undefined} />)
      )}
      <div className={cn("h-1")} />
    </div>
  );
}
