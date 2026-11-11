import { z } from "zod";
import type { Field } from "@/lib/types";

// 表单内部的值：kv 用数组保存（输入键名的过程中键可能为空或重复），保存时再转成对象。
export interface KVPair {
  key: string;
  value: string;
}
export type FormRecord = Record<string, unknown>;
export type FormValues = Record<string, unknown>;

/** 字段是否显示：显示条件引用同一层级的另一个字段。 */
export function isVisible(field: Field, values: FormValues): boolean {
  const c = field.showIf;
  if (!c) return true;
  const v = values[c.key];
  if (c.in && c.in.length > 0) return c.in.includes(String(v ?? ""));
  if (c.notEmpty) return !isEmpty(v);
  return true;
}

export function isEmpty(v: unknown): boolean {
  if (v === undefined || v === null) return true;
  if (typeof v === "string") return v.trim() === "";
  if (Array.isArray(v)) return v.length === 0;
  return false;
}

/** 新建时的初始值：有默认值用默认值，否则按类型给空值。 */
export function initialValue(field: Field): unknown {
  const d = field.default;
  switch (field.type) {
    case "kv":
      return d && typeof d === "object" ? Object.entries(d as Record<string, string>).map(([key, value]) => ({ key, value })) : [];
    case "list":
      return Array.isArray(d) ? [...d] : [];
    case "records":
      return [];
    case "bool":
      return typeof d === "boolean" ? d : false;
    case "number":
      return typeof d === "number" ? d : undefined;
    default:
      return typeof d === "string" ? d : "";
  }
}

export function initialValues(fields: Field[]): FormValues {
  return Object.fromEntries(fields.map((f) => [f.key, initialValue(f)]));
}

/**
 * 编辑时从保存的参数换成表单值：保存了的用保存的值，没保存的（表示用自动值）留空；
 * 有默认值但没保存的（声明后来新增的参数）用默认值，和后端「没传就用默认值」一致。
 */
export function valuesFromParams(fields: Field[], params: Record<string, unknown>): FormValues {
  const out: FormValues = {};
  for (const f of fields) {
    if (!(f.key in params)) {
      out[f.key] = f.default !== undefined ? initialValue(f) : emptyValue(f);
      continue;
    }
    const v = params[f.key];
    switch (f.type) {
      case "kv":
        out[f.key] = v && typeof v === "object" ? Object.entries(v as Record<string, string>).map(([key, value]) => ({ key, value })) : [];
        break;
      case "records":
        out[f.key] = Array.isArray(v) ? v.map((r) => valuesFromParams(f.fields ?? [], r as Record<string, unknown>)) : [];
        break;
      default:
        out[f.key] = v;
    }
  }
  return out;
}

function emptyValue(field: Field): unknown {
  return initialValue({ ...field, default: undefined });
}

/** 新增一条记录时的初始值。 */
export function newRecord(field: Field): FormRecord {
  return initialValues(field.fields ?? []);
}

/**
 * 转成要保存的数据：去掉不显示的字段，kv 转成对象。
 * 空值：没有默认值的去掉（部署时用自动值）；有默认值的保留空值，表示有意清空（如不排除任何文件），否则后端会补回默认值。
 */
export function toPayload(fields: Field[], values: FormValues): FormValues {
  const out: FormValues = {};
  for (const f of fields) {
    if (!isVisible(f, values)) continue;
    const v = values[f.key];
    if (f.default !== undefined && f.type !== "bool" && f.type !== "number") {
      const before = Object.keys(out).length;
      writePayload(f, v, out);
      if (Object.keys(out).length === before) out[f.key] = f.type === "list" ? [] : f.type === "kv" ? {} : "";
      continue;
    }
    writePayload(f, v, out);
  }
  return out;
}

function writePayload(f: Field, v: unknown, out: FormValues) {
  {
    switch (f.type) {
      case "kv": {
        const pairs = (v as KVPair[] | undefined) ?? [];
        const obj = Object.fromEntries(pairs.filter((p) => p.key.trim() !== "").map((p) => [p.key.trim(), p.value]));
        if (Object.keys(obj).length > 0) out[f.key] = obj;
        break;
      }
      case "records": {
        const rows = ((v as FormRecord[] | undefined) ?? []).map((r) => toPayload(f.fields ?? [], r));
        if (rows.length > 0) out[f.key] = rows;
        break;
      }
      case "list": {
        const list = ((v as string[] | undefined) ?? []).map((s) => s.trim()).filter(Boolean);
        if (list.length > 0) out[f.key] = list;
        break;
      }
      case "bool":
        out[f.key] = Boolean(v);
        break;
      case "number":
        if (typeof v === "number" && !Number.isNaN(v)) out[f.key] = v;
        break;
      case "code":
      case "textarea":
        if (typeof v === "string" && v.trim() !== "") out[f.key] = v;
        break;
      default:
        if (typeof v === "string" && v.trim() !== "") out[f.key] = v.trim();
    }
  }
}

type Issue = { path: (string | number)[]; message: string };

/** 按声明校验一层字段，返回带路径的错误。后端在 M1 做同样的校验，以后端为准。 */
export function validate(fields: Field[], values: FormValues, prefix: (string | number)[] = []): Issue[] {
  const issues: Issue[] = [];
  for (const f of fields) {
    if (!isVisible(f, values)) continue;
    const v = values[f.key];
    const path = [...prefix, f.key];
    const add = (message: string, sub: (string | number)[] = []) => issues.push({ path: [...path, ...sub], message });

    if (f.required) {
      const empty =
        f.type === "kv" ? !((v as KVPair[] | undefined) ?? []).some((p) => p.key.trim() !== "") : f.type === "bool" ? false : isEmpty(v);
      if (empty) {
        add(`请填写${/^[A-Za-z0-9]/.test(f.label) ? " " : ""}${f.label}`);
        continue;
      }
    }

    if (typeof v === "string" && v.trim() !== "" && f.pattern && (f.type === "text" || f.type === "select")) {
      if (!new RegExp(f.pattern).test(v.trim())) add(f.patternHint ?? "格式不正确");
    }

    if (f.type === "number" && typeof v === "number") {
      if (Number.isNaN(v)) add("请输入数字");
      else if (f.min !== undefined && v < f.min) add(`不能小于 ${f.min}`);
      else if (f.max !== undefined && v > f.max) add(`不能大于 ${f.max}`);
    }

    if (f.type === "kv") {
      const seen = new Set<string>();
      for (const p of (v as KVPair[] | undefined) ?? []) {
        const k = p.key.trim();
        if (k === "" && p.value.trim() !== "") {
          add(`${f.keyLabel ?? "键"}不能为空`);
          break;
        }
        if (k !== "" && seen.has(k)) {
          add(`${f.keyLabel ?? "键"} ${k} 重复`);
          break;
        }
        seen.add(k);
      }
    }

    if (f.type === "list") {
      const list = (v as string[] | undefined) ?? [];
      if (new Set(list).size !== list.length) add("有重复的项");
    }

    if (f.type === "records") {
      ((v as FormRecord[] | undefined) ?? []).forEach((row, i) => {
        issues.push(...validate(f.fields ?? [], row, [...path, i]));
      });
    }
  }
  return issues;
}

/** 生成给 react-hook-form 用的 zod 校验：结构宽松，规则全部在 validate 里。 */
export function buildSchema(fields: Field[]) {
  return z.record(z.string(), z.unknown()).superRefine((values, ctx) => {
    for (const issue of validate(fields, values)) {
      ctx.addIssue({ code: "custom", path: issue.path, message: issue.message });
    }
  });
}
