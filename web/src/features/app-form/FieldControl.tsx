import { lazy, Suspense, type ReactNode } from "react";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import type { Field, Option } from "@/lib/types";
import { cn } from "@/lib/utils";
import { KeyValueInput } from "./controls/KeyValueInput";
import { RecordsInput, type RecordErrors } from "./controls/RecordsInput";
import { SelectInput } from "./controls/SelectInput";
import { TagInput } from "./controls/TagInput";
import type { FormRecord, KVPair } from "./schema";

const CodeInput = lazy(() => import("./controls/CodeInput"));

/** 运行时的选项来源：已添加的服务器、凭据（按种类）、仓库识别结果。 */
export interface OptionSources {
  servers: Option[];
  credentials: Record<string, Option[]>;
  detect: Record<string, Option[]>;
}

function resolveOptions(field: Field, sources: OptionSources): { options: Option[]; emptyText?: string } {
  if (field.options?.length) return { options: field.options };
  if (field.optionsFrom === "servers") {
    return { options: sources.servers, emptyText: "还没有服务器，先在「服务器」页面添加" };
  }
  if (field.optionsFrom?.startsWith("credentials:")) {
    return { options: sources.credentials[field.optionsFrom.slice(12)] ?? [], emptyText: "还没有这种凭据，先在「凭据」页面添加" };
  }
  if (field.optionsFrom?.startsWith("detect:")) {
    return { options: sources.detect[field.optionsFrom.slice(7)] ?? [] };
  }
  return { options: [] };
}

interface Props {
  field: Field;
  value: unknown;
  onChange: (v: unknown) => void;
  error?: string;
  recordErrors?: RecordErrors;
  idPrefix?: string;
  sources: OptionSources;
  /** 记录里的子字段排得更紧凑 */
  compact?: boolean;
  /** 留空时实际会用的值（仓库识别或服务器检测得出），显示在输入框里 */
  autoHint?: string;
  disabled?: boolean;
  /** 显示在控件下面的附加内容，如仓库识别结果 */
  extra?: ReactNode;
}

export function FieldControl({ field, value, onChange, error, recordErrors, idPrefix = "f", sources, compact, autoHint, disabled, extra }: Props) {
  const id = `${idPrefix}-${field.key}`;
  const invalid = Boolean(error);
  const placeholder = autoHint ? `留空：${autoHint}` : (field.placeholder ?? (field.defaultHint ? `留空：${field.defaultHint}` : undefined));

  let control;
  switch (field.type) {
    case "text":
      control = (
        <Input
          id={id}
          value={(value as string) ?? ""}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder}
          disabled={disabled}
          aria-invalid={invalid || undefined}
        />
      );
      break;
    case "textarea":
      control = (
        <Textarea
          id={id}
          value={(value as string) ?? ""}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder}
          aria-invalid={invalid || undefined}
          className="min-h-20 font-mono text-xs"
        />
      );
      break;
    case "number":
      control = (
        <Input
          id={id}
          type="number"
          inputMode="numeric"
          min={field.min}
          max={field.max}
          value={value === undefined || value === null ? "" : String(value)}
          onChange={(e) => onChange(e.target.value === "" ? undefined : Number(e.target.value))}
          placeholder={placeholder}
          aria-invalid={invalid || undefined}
          className="max-w-40"
        />
      );
      break;
    case "bool":
      control = <Switch id={id} checked={Boolean(value)} onCheckedChange={onChange} />;
      break;
    case "select": {
      const { options, emptyText } = resolveOptions(field, sources);
      control = (
        <SelectInput
          id={id}
          value={(value as string) ?? ""}
          onChange={onChange}
          options={options}
          placeholder={placeholder}
          allowCustom={field.allowCustom}
          clearable={!field.required && field.default === undefined}
          emptyText={emptyText}
          invalid={invalid}
        />
      );
      break;
    }
    case "list":
      control = <TagInput id={id} value={(value as string[]) ?? []} onChange={onChange} placeholder={placeholder} invalid={invalid} />;
      break;
    case "kv":
      control = (
        <KeyValueInput value={(value as KVPair[]) ?? []} onChange={onChange} keyLabel={field.keyLabel} valueLabel={field.valueLabel} invalid={invalid} />
      );
      break;
    case "code":
      control = (
        <Suspense fallback={<Skeleton className="h-32 w-full" />}>
          <CodeInput value={(value as string) ?? ""} onChange={onChange} language={field.language} invalid={invalid} />
        </Suspense>
      );
      break;
    case "records":
      control = (
        <RecordsInput field={field} value={(value as FormRecord[]) ?? []} onChange={onChange} errors={recordErrors} idPrefix={id} sources={sources} />
      );
      break;
  }

  const inline = field.type === "bool";
  const selected = field.type === "select" ? field.options?.find((o) => o.value === value) : undefined;

  return (
    <div className={cn("space-y-2", compact && "space-y-1.5")} data-field={field.key}>
      <div className={cn("flex flex-wrap items-center gap-x-2 gap-y-1", inline && "justify-between")}>
        <Label htmlFor={id} className="gap-1">
          {field.label}
          {field.required && <span className="text-destructive">*</span>}
        </Label>
        {field.immutable && (
          <Badge variant="outline" className="font-normal text-muted-foreground">
            创建后不能修改
          </Badge>
        )}
        {inline && control}
      </div>
      {!inline && control}
      {error && <p className="text-xs text-destructive">{error}</p>}
      {extra}
      {selected?.help && <p className="text-xs text-muted-foreground">{selected.help}</p>}
      {field.help && <p className="text-xs leading-relaxed text-muted-foreground">{field.help}</p>}
      {field.defaultHint && field.placeholder && !autoHint && <p className="text-xs text-muted-foreground">留空时：{field.defaultHint}</p>}
    </div>
  );
}
