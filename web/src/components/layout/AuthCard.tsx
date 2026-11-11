import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

/** 登录、创建管理员这类不在主界面里的页面：居中的一张卡片。 */
export function AuthCard({ icon: Icon, title, description, children }: { icon: LucideIcon; title: string; description: ReactNode; children: ReactNode }) {
  return (
    <div className="flex min-h-svh items-center justify-center p-4">
      <Card className="w-full max-w-sm gap-4">
        <CardHeader className="gap-3">
          <span className="flex size-10 items-center justify-center rounded-lg bg-primary/10 text-primary">
            <Icon className="size-5" />
          </span>
          <CardTitle>{title}</CardTitle>
          <CardDescription className="leading-relaxed">{description}</CardDescription>
        </CardHeader>
        <CardContent>{children}</CardContent>
      </Card>
    </div>
  );
}
