// 调用后端接口。错误统一抛出 ApiError，未登录时状态码为 401。

export interface FieldError {
  field: string;
  message: string;
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly fields: FieldError[];

  constructor(status: number, code: string, message: string, fields: FieldError[] = []) {
    super(message);
    this.status = status;
    this.code = code;
    this.fields = fields;
  }

  get unauthorized() {
    return this.status === 401;
  }
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, {
      ...init,
      credentials: "same-origin",
      headers: { Accept: "application/json", ...(init?.body ? { "Content-Type": "application/json" } : {}), ...init?.headers },
    });
  } catch {
    throw new ApiError(0, "network", "连接不上 deployhub 服务，请确认它还在运行");
  }
  const text = await res.text();
  const body = text ? safeJSON(text) : undefined;
  if (!res.ok) {
    const e = (body as { error?: { code?: string; message?: string; fields?: FieldError[] } } | undefined)?.error;
    throw new ApiError(res.status, e?.code ?? "http_" + res.status, e?.message ?? `请求失败（${res.status}）`, e?.fields);
  }
  return body as T;
}

function safeJSON(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

/** 发送 JSON 请求的简写。 */
export const http = {
  get: <T>(path: string) => api<T>(path),
  post: <T>(path: string, body?: unknown) => api<T>(path, { method: "POST", body: body === undefined ? undefined : JSON.stringify(body) }),
  put: <T>(path: string, body: unknown) => api<T>(path, { method: "PUT", body: JSON.stringify(body) }),
  del: <T>(path: string) => api<T>(path, { method: "DELETE" }),
};

/** 把接口返回的字段错误换成 { 字段: 说明 }。 */
export function fieldErrors(err: unknown): Record<string, string> {
  const out: Record<string, string> = {};
  if (err instanceof ApiError) for (const f of err.fields) if (!(f.field in out)) out[f.field] = f.message;
  return out;
}

export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
