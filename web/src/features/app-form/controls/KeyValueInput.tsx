import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { KVPair } from "../schema";

interface Props {
  value: KVPair[];
  onChange: (v: KVPair[]) => void;
  keyLabel?: string;
  valueLabel?: string;
  invalid?: boolean;
}

/** 键值对：每行一对，可增删。 */
export function KeyValueInput({ value, onChange, keyLabel = "键", valueLabel = "值", invalid }: Props) {
  const update = (i: number, patch: Partial<KVPair>) => onChange(value.map((p, j) => (j === i ? { ...p, ...patch } : p)));

  return (
    <div className="space-y-2">
      {value.length > 0 && (
        <div className="grid grid-cols-[minmax(0,2fr)_minmax(0,3fr)_auto] gap-2 text-xs text-muted-foreground">
          <span>{keyLabel}</span>
          <span>{valueLabel}</span>
          <span className="w-8" />
        </div>
      )}
      {value.map((p, i) => (
        <div key={i} className="grid grid-cols-[minmax(0,2fr)_minmax(0,3fr)_auto] gap-2">
          <Input
            value={p.key}
            onChange={(e) => update(i, { key: e.target.value })}
            placeholder={keyLabel}
            aria-label={`${keyLabel} ${i + 1}`}
            aria-invalid={invalid || undefined}
            className="font-mono text-xs"
          />
          <Input
            value={p.value}
            onChange={(e) => update(i, { value: e.target.value })}
            placeholder={valueLabel}
            aria-label={`${valueLabel} ${i + 1}`}
            className="font-mono text-xs"
          />
          <Button type="button" variant="ghost" size="icon" aria-label="删除这一行" onClick={() => onChange(value.filter((_, j) => j !== i))}>
            <Trash2 className="text-muted-foreground" />
          </Button>
        </div>
      ))}
      <Button type="button" variant="outline" size="sm" onClick={() => onChange([...value, { key: "", value: "" }])}>
        <Plus />
        添加
      </Button>
    </div>
  );
}
