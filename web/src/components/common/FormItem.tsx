import type { ReactNode } from "react";
import { Label } from "@/components/ui/label";

/** 简单表单的一项：名称、控件、错误、说明。参数表单用 FieldControl，这里给服务器、密钥、项目这类固定表单用。 */
export function FormItem({ id, label, required, error, help, children }: { id?: string; label: string; required?: boolean; error?: string; help?: ReactNode; children: ReactNode }) {
  return (
    <div className="space-y-2">
      <Label htmlFor={id} className="gap-1">
        {label}
        {required && <span className="text-destructive">*</span>}
      </Label>
      {children}
      {error && <p className="text-xs text-destructive">{error}</p>}
      {help && <p className="text-xs leading-relaxed text-muted-foreground">{help}</p>}
    </div>
  );
}
