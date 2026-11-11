import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { Option } from "@/lib/types";

interface Props {
  id?: string;
  value: string;
  onChange: (v: string) => void;
  options: Option[];
  placeholder?: string;
  allowCustom?: boolean;
  clearable?: boolean;
  emptyText?: string;
  invalid?: boolean;
}

/**
 * 下拉选择。allowCustom 时是带建议的输入框（选项只是识别出来的建议，也可以手填）。
 * clearable 时可以清空，回到「留空用默认值」。
 */
export function SelectInput({ id, value, onChange, options, placeholder, allowCustom, clearable, emptyText, invalid }: Props) {
  if (allowCustom) {
    const listId = id ? `${id}-options` : undefined;
    return (
      <>
        <Input
          id={id}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder}
          list={listId}
          aria-invalid={invalid || undefined}
          className="font-mono text-sm"
        />
        {options.length > 0 && (
          <datalist id={listId}>
            {options.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </datalist>
        )}
      </>
    );
  }

  const current = options.find((o) => o.value === value);
  return (
    <div className="flex items-center gap-1">
      <Select value={value} onValueChange={onChange} disabled={options.length === 0}>
        <SelectTrigger id={id} className="w-full" aria-invalid={invalid || undefined}>
          {/* 选中后只显示名称；选项说明显示在下拉列表和输入框下方 */}
          <SelectValue placeholder={options.length === 0 ? (emptyText ?? "没有可选项") : (placeholder ?? "请选择")}>
            {current?.label}
          </SelectValue>
        </SelectTrigger>
        <SelectContent>
          {options.map((o) => (
            <SelectItem key={o.value} value={o.value} className="items-start">
              <span className="flex flex-col gap-0.5">
                <span>{o.label}</span>
                {o.help && <span className="max-w-md text-xs whitespace-normal text-muted-foreground">{o.help}</span>}
              </span>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {clearable && value !== "" && (
        <Button type="button" variant="ghost" size="icon" aria-label="清空，使用默认值" onClick={() => onChange("")}>
          <X className="text-muted-foreground" />
        </Button>
      )}
    </div>
  );
}
