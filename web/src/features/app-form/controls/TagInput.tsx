import { X } from "lucide-react";
import { useState, type KeyboardEvent } from "react";
import { cn } from "@/lib/utils";

interface Props {
  id?: string;
  value: string[];
  onChange: (v: string[]) => void;
  placeholder?: string;
  invalid?: boolean;
}

/** 字符串列表：回车、逗号或失去焦点时添加，点 × 删除，空输入时按退格删除最后一项。 */
export function TagInput({ id, value, onChange, placeholder, invalid }: Props) {
  const [draft, setDraft] = useState("");

  const commit = () => {
    const items = draft
      .split(/[,，\n]/)
      .map((s) => s.trim())
      .filter((s) => s && !value.includes(s));
    if (items.length) onChange([...value, ...items]);
    setDraft("");
  };

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter" || e.key === ",") {
      e.preventDefault();
      commit();
    } else if (e.key === "Backspace" && draft === "" && value.length > 0) {
      onChange(value.slice(0, -1));
    }
  };

  return (
    <div
      className={cn(
        "flex min-h-9 w-full flex-wrap items-center gap-1.5 rounded-md border border-input bg-transparent px-2 py-1.5 text-sm shadow-xs transition-[color,box-shadow] focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/50 dark:bg-input/30",
        invalid && "border-destructive ring-destructive/20",
      )}
    >
      {value.map((item) => (
        <span key={item} className="inline-flex items-center gap-1 rounded bg-secondary px-1.5 py-0.5 font-mono text-xs">
          {item}
          <button
            type="button"
            aria-label={`删除 ${item}`}
            className="text-muted-foreground hover:text-foreground"
            onClick={() => onChange(value.filter((v) => v !== item))}
          >
            <X className="size-3" />
          </button>
        </span>
      ))}
      <input
        id={id}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={onKeyDown}
        onBlur={commit}
        placeholder={value.length === 0 ? placeholder : undefined}
        className="min-w-24 flex-1 bg-transparent py-0.5 outline-none placeholder:text-muted-foreground"
      />
    </div>
  );
}
