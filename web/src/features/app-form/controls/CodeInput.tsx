import { StreamLanguage } from "@codemirror/language";
import { yaml } from "@codemirror/lang-yaml";
import { properties } from "@codemirror/legacy-modes/mode/properties";
import { shell } from "@codemirror/legacy-modes/mode/shell";
import { toml } from "@codemirror/legacy-modes/mode/toml";
import CodeMirror from "@uiw/react-codemirror";
import { useEffect, useMemo, useState } from "react";
import { cn } from "@/lib/utils";

interface Props {
  value: string;
  onChange: (v: string) => void;
  language?: string;
  invalid?: boolean;
}

function languageExtension(language?: string) {
  switch (language) {
    case "yaml":
      return [yaml()];
    case "toml":
      return [StreamLanguage.define(toml)];
    case "ini":
      return [StreamLanguage.define(properties)];
    case "shell":
      return [StreamLanguage.define(shell)];
    default:
      return [];
  }
}

function useDarkMode() {
  const [dark, setDark] = useState(() => document.documentElement.classList.contains("dark"));
  useEffect(() => {
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = () => setDark(mq.matches);
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);
  return dark;
}

/** 代码编辑器，用于配置文件、服务模板、compose 文件。按需加载（见 FieldControl 的 lazy）。 */
export default function CodeInput({ value, onChange, language, invalid }: Props) {
  const extensions = useMemo(() => languageExtension(language), [language]);
  const dark = useDarkMode();
  return (
    <div
      className={cn(
        "overflow-hidden rounded-md border border-input text-xs focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/50",
        invalid && "border-destructive",
      )}
    >
      <CodeMirror
        value={value}
        onChange={onChange}
        extensions={extensions}
        theme={dark ? "dark" : "light"}
        minHeight="120px"
        maxHeight="420px"
        basicSetup={{ foldGutter: false, highlightActiveLine: false }}
      />
    </div>
  );
}
