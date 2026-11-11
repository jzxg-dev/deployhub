import { Box, ChevronRight, Cloud, Container, Globe, ServerCog, type LucideIcon } from "lucide-react";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import type { DriverSpec } from "@/lib/types";

const icons: Record<string, LucideIcon> = {
  "server-cog": ServerCog,
  globe: Globe,
  container: Container,
  cloud: Cloud,
};

export function DriverIcon({ name, className }: { name: string; className?: string }) {
  const Icon = icons[name] ?? Box;
  return <Icon className={className} />;
}

/** 新建应用第一步：选择部署方式。 */
export function DriverPicker({ drivers, onPick }: { drivers: DriverSpec[]; onPick: (name: string) => void }) {
  return (
    <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
      {drivers.map((d) => (
        <button
          key={d.name}
          type="button"
          onClick={() => onPick(d.name)}
          className="group rounded-xl text-left outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
        >
          <Card className="h-full gap-3 transition-colors group-hover:border-primary/60 group-hover:bg-accent/40">
            <CardHeader className="gap-3">
              <div className="flex items-center justify-between">
                <span className="flex size-10 items-center justify-center rounded-lg bg-primary/10 text-primary">
                  <DriverIcon name={d.icon} className="size-5" />
                </span>
                <ChevronRight className="size-4 text-muted-foreground transition-transform group-hover:translate-x-0.5" />
              </div>
              <CardTitle className="text-base">{d.title}</CardTitle>
              <CardDescription className="text-foreground/80">{d.summary}</CardDescription>
              <CardDescription className="text-xs leading-relaxed">{d.description}</CardDescription>
            </CardHeader>
          </Card>
        </button>
      ))}
      <Card className="h-full gap-2 border-dashed bg-transparent shadow-none md:col-span-2 xl:col-span-4">
        <CardHeader>
          <CardTitle className="text-sm text-muted-foreground">以后加入</CardTitle>
          <CardDescription>自定义命令（平台还没覆盖的特殊部署）</CardDescription>
        </CardHeader>
      </Card>
    </div>
  );
}
