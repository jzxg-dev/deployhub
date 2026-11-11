import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Bot, Check, Copy, Download, FileJson, History, KeyRound, Loader2, LogOut, Plus, Trash2, Upload } from "lucide-react";
import { ConfirmButton } from "@/components/common/ConfirmButton";
import { Badge } from "@/components/ui/badge";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { timeAgo } from "@/lib/format";
import type { AccessToken, TokenLevel } from "@/lib/types";
import { useRef, useState } from "react";
import { toast } from "sonner";
import { FormItem } from "@/components/common/FormItem";
import { PageHeader } from "@/components/layout/Page";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { SelectInput } from "@/features/app-form/controls/SelectInput";
import { useEventStream } from "@/hooks/events";
import { useProjects, useSession, useTokens } from "@/hooks/queries";
import { errorMessage, fieldErrors, http } from "@/lib/api";
import { logout } from "@/lib/session";

interface HistoryResult {
  imported: number;
  skipped: number;
  unknown: string[];
}

interface ImportResult {
  created: string[];
  skipped: string[];
  failed: string[];
  warnings: string[];
}

/** 导入旧部署工具的部署历史（history.jsonl）。 */
function ImportHistory() {
  const qc = useQueryClient();
  const { data: projects } = useProjects();
  const [project, setProject] = useState("");
  const [path, setPath] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const run = useMutation({
    mutationFn: () => http.post<HistoryResult>("/api/import/history", { project, path }),
    onSuccess: (r) => {
      setErrors({});
      void qc.invalidateQueries({ queryKey: ["jobs"] });
      toast.success(`导入 ${r.imported} 条，跳过 ${r.skipped} 条`, {
        description: r.unknown.length ? `项目里没有这些应用，记录已导入但不关联应用：${r.unknown.join("、")}` : undefined,
      });
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      toast.error(errorMessage(e));
    },
  });
  return (
    <Card className="gap-4">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <History className="size-4" />
          导入旧工具的部署历史
        </CardTitle>
        <CardDescription>
          把旧部署工具的 history.jsonl 导入「部署记录」，单元名对应项目里的应用名。只导入部署和回滚，重复导入会跳过已经导入过的。
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <FormItem label="导入到项目" required error={errors.project}>
          <SelectInput value={project} onChange={setProject} options={(projects ?? []).map((p) => ({ value: p.name, label: p.title, help: p.name }))} emptyText="还没有项目" />
        </FormItem>
        <FormItem id="hist-path" label="history.jsonl 的路径" required error={errors.path}>
          <Input id="hist-path" value={path} onChange={(e) => setPath(e.target.value)} className="font-mono text-sm" placeholder="~/deploy/logs/history.jsonl" />
        </FormItem>
        <Button onClick={() => run.mutate()} disabled={!project || !path || run.isPending}>
          {run.isPending ? <Loader2 className="animate-spin" /> : <History />}
          导入
        </Button>
      </CardContent>
    </Card>
  );
}

/** 导出和导入配置：服务器、密钥、项目、应用。 */
function ExportImport() {
  const qc = useQueryClient();
  const [sensitive, setSensitive] = useState(false);
  const [result, setResult] = useState<ImportResult>();
  const file = useRef<HTMLInputElement>(null);

  const exp = useMutation({
    mutationFn: () => http.post<unknown>("/api/export", { sensitive }),
    onSuccess: (data) => {
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = `deployhub-${new Date().toISOString().slice(0, 10)}${sensitive ? "-含敏感内容" : ""}.json`;
      a.click();
      URL.revokeObjectURL(a.href);
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const imp = useMutation({
    mutationFn: async (f: File) => http.post<ImportResult>("/api/import", JSON.parse(await f.text())),
    onSuccess: (r) => {
      setResult(r);
      void qc.invalidateQueries();
      toast.success(`新建 ${r.created.length} 项，跳过 ${r.skipped.length} 项${r.failed.length ? `，失败 ${r.failed.length} 项` : ""}`);
    },
    onError: (e) => toast.error(e instanceof SyntaxError ? "不是有效的 JSON 文件" : errorMessage(e)),
  });

  return (
    <Card className="gap-4">
      <CardHeader>
        <CardTitle className="text-base">导出和导入配置</CardTitle>
        <CardDescription>当前账号的服务器、密钥、凭据、项目和应用导出成 JSON，用来备份或迁移到另一台机器。导入时只新建，同名的已有数据不会被覆盖。</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="space-y-2">
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" className="size-4 accent-primary" checked={sensitive} onChange={(e) => setSensitive(e.target.checked)} />
            包含密钥的值、凭据的 Token 和配置文件内容（明文）
          </label>
          <p className="text-xs text-muted-foreground">
            {sensitive ? "导出的文件里有明文密钥，请妥善保管，用完删除。" : "不包含时，导入后需要手动补上密钥的值、凭据的 Token 和配置文件内容。"}
          </p>
          <Button variant="outline" onClick={() => exp.mutate()} disabled={exp.isPending}>
            {exp.isPending ? <Loader2 className="animate-spin" /> : <Download />}
            导出
          </Button>
        </div>
        <div className="space-y-2">
          <input
            ref={file}
            type="file"
            accept="application/json,.json"
            className="hidden"
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) imp.mutate(f);
              e.target.value = "";
            }}
          />
          <Button variant="outline" onClick={() => file.current?.click()} disabled={imp.isPending}>
            {imp.isPending ? <Loader2 className="animate-spin" /> : <Upload />}
            导入配置文件
          </Button>
          {result && (
            <div className="space-y-2 rounded-md border p-3 text-xs">
              {(
                [
                  ["新建", result.created, "text-success"],
                  ["跳过", result.skipped, "text-muted-foreground"],
                  ["失败", result.failed, "text-destructive"],
                  ["需要处理", result.warnings, "text-warning"],
                ] as const
              ).map(([label, list, cls]) =>
                list.length ? (
                  <div key={label}>
                    <p className={`font-medium ${cls}`}>
                      {label}（{list.length}）
                    </p>
                    <ul className="ml-4 list-disc text-muted-foreground">
                      {list.map((x) => (
                        <li key={x}>{x}</li>
                      ))}
                    </ul>
                  </div>
                ) : null,
              )}
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  );
}

const levels: { value: TokenLevel; label: string; help: string }[] = [
  { value: "read", label: "只读", help: "查看应用、状态、部署记录和日志、部署计划" },
  { value: "deploy", label: "可部署", help: "再加上发起部署、回滚、取消任务" },
  { value: "admin", label: "管理", help: "再加上新建和修改项目、应用、服务器" },
];
const levelLabel = Object.fromEntries(levels.map((l) => [l.value, l.label])) as Record<TokenLevel, string>;

/** 一行可以复制的文本（令牌、命令）。 */
function CopyLine({ text, label }: { text: string; label: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="flex items-start gap-2">
      <pre className="min-w-0 flex-1 rounded-md bg-muted px-3 py-2 font-mono text-xs leading-relaxed break-all whitespace-pre-wrap">{text}</pre>
      <Button
        type="button"
        variant="outline"
        size="icon-sm"
        aria-label={`复制${label}`}
        onClick={async () => {
          try {
            await navigator.clipboard.writeText(text);
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
          } catch {
            toast.error("复制失败，请手动选中复制");
          }
        }}
      >
        {copied ? <Check /> : <Copy />}
      </Button>
    </div>
  );
}

/** 创建访问令牌；创建后显示令牌和接入命令，关掉就再也看不到令牌了。 */
function TokenDialog({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [level, setLevel] = useState<TokenLevel>("deploy");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [created, setCreated] = useState<{ name: string; token: string }>();
  const create = useMutation({
    mutationFn: () => http.post<AccessToken & { token: string }>("/api/tokens", { name, level }),
    onSuccess: async (t) => {
      await qc.invalidateQueries({ queryKey: ["tokens"] });
      setCreated({ name: t.name, token: t.token });
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      toast.error(errorMessage(e));
    },
  });
  const origin = window.location.origin;
  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-lg">
        {created ? (
          <>
            <DialogHeader>
              <DialogTitle>令牌「{created.name}」已创建</DialogTitle>
              <DialogDescription>令牌只显示这一次，关掉这个窗口就看不到了。丢了就吊销它，再建一个新的。</DialogDescription>
            </DialogHeader>
            <div className="space-y-4">
              <div className="space-y-1.5">
                <p className="text-sm font-medium">令牌</p>
                <CopyLine text={created.token} label="令牌" />
              </div>
              <div className="space-y-1.5">
                <p className="text-sm font-medium">接入 Claude Code（MCP）</p>
                <CopyLine text={`claude mcp add --transport http deployhub ${origin}/mcp --header "Authorization: Bearer ${created.token}"`} label="接入命令" />
                <p className="text-xs text-muted-foreground">在终端执行一次，之后直接对 AI 说「把某某应用部署一下」。</p>
              </div>
              <div className="space-y-1.5">
                <p className="text-sm font-medium">脚本里用 HTTP 接口</p>
                <CopyLine text={`curl -H "Authorization: Bearer ${created.token}" ${origin}/api/apps`} label="示例命令" />
              </div>
            </div>
            <DialogFooter>
              <Button onClick={onClose}>我已经保存好了</Button>
            </DialogFooter>
          </>
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>创建访问令牌</DialogTitle>
              <DialogDescription>给 AI 或脚本用。令牌只能访问当前账号的数据，而且不管什么权限都读不到密钥的值和配置文件内容。</DialogDescription>
            </DialogHeader>
            <form
              className="space-y-4"
              onSubmit={(e) => {
                e.preventDefault();
                create.mutate();
              }}
            >
              <FormItem id="token-name" label="名称" required error={errors.name} help="给谁用的，部署记录里会显示它，如 Claude Code">
                <Input id="token-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Claude Code" autoFocus />
              </FormItem>
              <FormItem label="权限" required error={errors.level}>
                <div className="space-y-2">
                  {levels.map((l) => (
                    <label key={l.value} className="flex cursor-pointer items-start gap-2 text-sm">
                      <input type="radio" name="token-level" className="mt-1 accent-primary" checked={level === l.value} onChange={() => setLevel(l.value)} />
                      <span>
                        {l.label}
                        <span className="block text-xs text-muted-foreground">{l.help}</span>
                      </span>
                    </label>
                  ))}
                </div>
              </FormItem>
              <p className="text-xs leading-relaxed text-muted-foreground">
                用令牌发起的部署和回滚，在打开了「AI 部署需要审批」的应用上（默认打开）要等你在页面上批准才执行。
              </p>
              <DialogFooter>
                <Button type="submit" disabled={!name.trim() || create.isPending}>
                  {create.isPending && <Loader2 className="animate-spin" />}
                  创建
                </Button>
              </DialogFooter>
            </form>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

/** 访问令牌：给 AI（MCP）和脚本（HTTP 接口）用。 */
function AccessTokens() {
  const qc = useQueryClient();
  const { data: tokens } = useTokens();
  const [creating, setCreating] = useState(false);
  return (
    <Card className="gap-4">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <Bot className="size-4" />
          访问令牌
        </CardTitle>
        <CardDescription>
          让 AI（通过 MCP）和脚本（通过 HTTP 接口）查看状态、发起部署。令牌读不到密钥和配置文件内容；密钥、凭据、令牌的管理和任务审批只能在页面上做。
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {tokens && tokens.length > 0 && (
          <ul className="divide-y rounded-md border">
            {tokens.map((t) => (
              <li key={t.name} className="flex flex-wrap items-center justify-between gap-2 px-3 py-2">
                <div className="min-w-0 space-y-0.5">
                  <p className="flex flex-wrap items-center gap-2 text-sm">
                    <span className="truncate font-medium">{t.name}</span>
                    <Badge variant="secondary">{levelLabel[t.level] ?? t.level}</Badge>
                  </p>
                  <p className="text-xs text-muted-foreground">
                    <span className="font-mono">{t.hint}</span> · {timeAgo(t.createdAt)}创建 · {t.lastUsedAt ? `${timeAgo(t.lastUsedAt)}用过` : "还没用过"}
                  </p>
                </div>
                <ConfirmButton
                  trigger={
                    <Button variant="ghost" size="icon-sm" aria-label={`吊销 ${t.name}`}>
                      <Trash2 />
                    </Button>
                  }
                  title={`吊销令牌「${t.name}」？`}
                  description="吊销后用这个令牌的 AI 和脚本立刻不能再调用，无法恢复。"
                  confirmText="吊销"
                  destructive
                  onConfirm={async () => {
                    await http.del(`/api/tokens/${encodeURIComponent(t.name)}`);
                    await qc.invalidateQueries({ queryKey: ["tokens"] });
                    toast.success(`已吊销 ${t.name}`);
                  }}
                />
              </li>
            ))}
          </ul>
        )}
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" onClick={() => setCreating(true)}>
            <Plus />
            创建令牌
          </Button>
          <Button variant="ghost" asChild>
            <a href="/api/openapi.json" target="_blank" rel="noreferrer">
              <FileJson />
              接口文档（OpenAPI）
            </a>
          </Button>
        </div>
      </CardContent>
      {creating && <TokenDialog onClose={() => setCreating(false)} />}
    </Card>
  );
}

/** 当前账号：改密码、退出登录。 */
function Account() {
  const { data: session } = useSession();
  const [current, setCurrent] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const change = useMutation({
    mutationFn: () => http.post("/api/account/password", { current, password }),
    onSuccess: () => {
      setCurrent("");
      setPassword("");
      setConfirm("");
      setErrors({});
      toast.success("密码已修改", { description: "其他设备上已登录的页面需要重新登录。" });
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      toast.error(errorMessage(e));
    },
  });
  return (
    <Card className="gap-4">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <KeyRound className="size-4" />
          账号
        </CardTitle>
        <CardDescription>
          当前登录：<span className="font-mono text-foreground">{session?.user.username}</span>
          {session?.user.admin && "（管理员）"}。项目、服务器、密钥、凭据和部署记录只有这个账号能看到。
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (password !== confirm) {
              setErrors({ confirm: "两次输入的新密码不一样" });
              return;
            }
            change.mutate();
          }}
        >
          <FormItem id="pw-current" label="当前密码" required error={errors.current}>
            <Input id="pw-current" type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
          </FormItem>
          <FormItem id="pw-new" label="新密码" required error={errors.password} help="至少 8 个字符">
            <Input id="pw-new" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
          </FormItem>
          <FormItem id="pw-confirm" label="再输一次新密码" required error={errors.confirm}>
            <Input id="pw-confirm" type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
          </FormItem>
          <div className="flex flex-wrap gap-2">
            <Button type="submit" disabled={!current || !password || !confirm || change.isPending}>
              {change.isPending && <Loader2 className="animate-spin" />}
              修改密码
            </Button>
            <Button type="button" variant="outline" onClick={() => void logout()}>
              <LogOut />
              退出登录
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

export function SettingsPage() {
  const { data: session } = useSession();
  const { status, lastEventAt } = useEventStream();
  const rows: [string, string][] = [
    ["版本", session?.version ?? "—"],
    ["实时事件", status === "open" ? "已连接" : status === "connecting" ? "连接中" : "已断开"],
    ["最近一次心跳", lastEventAt ? lastEventAt.toLocaleTimeString("zh-CN") : "—"],
  ];
  return (
    <>
      <PageHeader title="设置" description="账号、给 AI 和脚本用的访问令牌、导出和导入。" />
      <div className="grid gap-4 lg:grid-cols-2">
        <div className="space-y-4">
          <Account />
          <AccessTokens />
          <ExportImport />
          <Card className="gap-4">
            <CardHeader>
              <CardTitle className="text-base">运行状态</CardTitle>
            </CardHeader>
            <CardContent>
              <dl className="divide-y text-sm">
                {rows.map(([k, v]) => (
                  <div key={k} className="flex justify-between py-2.5">
                    <dt className="text-muted-foreground">{k}</dt>
                    <dd className="font-mono">{v}</dd>
                  </div>
                ))}
              </dl>
            </CardContent>
          </Card>
        </div>
        <ImportHistory />
      </div>
    </>
  );
}
