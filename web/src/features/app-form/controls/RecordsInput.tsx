import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { Field } from "@/lib/types";
import { FieldControl, type OptionSources } from "../FieldControl";
import { isVisible, newRecord, type FormRecord } from "../schema";

/** react-hook-form 的记录错误：下标 → 子字段键 → { message } */
export type RecordErrors = Array<Record<string, { message?: string } | undefined> | undefined>;

interface Props {
  field: Field;
  value: FormRecord[];
  onChange: (v: FormRecord[]) => void;
  errors?: RecordErrors;
  idPrefix: string;
  sources: OptionSources;
}

/** 多条记录：每条是一组子字段，如上传的配置文件（文件名、来源、内容）。 */
export function RecordsInput({ field, value, onChange, errors, idPrefix, sources }: Props) {
  const subFields = field.fields ?? [];
  const update = (i: number, key: string, v: unknown) => onChange(value.map((row, j) => (j === i ? { ...row, [key]: v } : row)));

  return (
    <div className="space-y-3">
      {value.map((row, i) => (
        <div key={i} className="relative rounded-lg border bg-muted/30 p-3 pr-12">
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className="absolute top-2 right-2"
            aria-label={`删除第 ${i + 1} 条`}
            onClick={() => onChange(value.filter((_, j) => j !== i))}
          >
            <Trash2 className="text-muted-foreground" />
          </Button>
          <div className="grid gap-3 sm:grid-cols-2">
            {subFields
              .filter((f) => isVisible(f, row))
              .map((f) => (
                <div key={f.key} className={f.type === "code" || f.type === "textarea" ? "sm:col-span-2" : undefined}>
                  <FieldControl
                    field={f}
                    value={row[f.key]}
                    onChange={(v) => update(i, f.key, v)}
                    error={errors?.[i]?.[f.key]?.message}
                    idPrefix={`${idPrefix}-${i}`}
                    sources={sources}
                    compact
                  />
                </div>
              ))}
          </div>
        </div>
      ))}
      <Button type="button" variant="outline" size="sm" onClick={() => onChange([...value, newRecord(field)])}>
        <Plus />
        添加{field.label.replace(/^上传的/, "")}
      </Button>
    </div>
  );
}
