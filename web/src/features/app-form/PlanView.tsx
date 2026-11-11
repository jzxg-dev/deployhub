import { AlertTriangle, Laptop, Server } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import type { Preview } from "@/lib/types";

/** 部署计划：留空参数的实际值、提醒、每一步要执行的命令。 */
export function PlanView({ preview }: { preview: Preview }) {
  const { plan } = preview;
  return (
    <div className="space-y-4 text-sm">
      <p className="text-xs text-muted-foreground">
        示例版本号 <code className="font-mono text-foreground">{preview.version}</code>
      </p>
      {plan.warnings.length > 0 && (
        <ul className="space-y-1.5">
          {plan.warnings.map((w) => (
            <li key={w} className="flex gap-1.5 rounded-md bg-warning/10 px-2.5 py-2 text-xs text-foreground">
              <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-warning" />
              {w}
            </li>
          ))}
        </ul>
      )}
      {plan.resolved.length > 0 && (
        <div className="space-y-1.5">
          <p className="text-xs font-medium text-muted-foreground">留空参数实际使用的值</p>
          <dl className="space-y-1 text-xs">
            {plan.resolved.map((r) => (
              <div key={r.key} className="flex flex-wrap items-baseline gap-x-2">
                <dt className="text-muted-foreground">{r.label}</dt>
                <dd className="min-w-0 font-mono break-all">{r.value}</dd>
                <Badge variant="outline" className="h-4 px-1 text-[10px] font-normal text-muted-foreground">
                  {r.source}
                </Badge>
              </div>
            ))}
          </dl>
        </div>
      )}
      <ol className="space-y-3">
        {plan.steps.map((step, i) => (
          <li key={step.name} className="space-y-1.5">
            <p className="text-xs font-medium">
              <span className="mr-1.5 text-muted-foreground">{i + 1}.</span>
              {step.name}
            </p>
            {step.commands.map((c, j) => (
              <div key={j} className="space-y-1">
                <div className="flex gap-1.5">
                  <span className="mt-1.5 shrink-0 text-muted-foreground" title={c.where === "local" ? "在本机执行" : "在服务器上执行"}>
                    {c.where === "local" ? <Laptop className="size-3.5" /> : <Server className="size-3.5" />}
                  </span>
                  <pre className="min-w-0 flex-1 overflow-x-auto rounded-md bg-muted px-2.5 py-1.5 font-mono text-[11px] leading-relaxed whitespace-pre-wrap break-all">
                    {c.command}
                  </pre>
                </div>
                {c.note && <p className="pl-5 text-[11px] text-muted-foreground">{c.note}</p>}
              </div>
            ))}
          </li>
        ))}
      </ol>
    </div>
  );
}
