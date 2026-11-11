import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Loader2, Pencil, PlugZap, Plus, Server as ServerIcon, Trash2, XCircle } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ConfirmButton } from "@/components/common/ConfirmButton";
import { FormItem } from "@/components/common/FormItem";
import { EmptyState, PageHeader } from "@/components/layout/Page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { SelectInput } from "@/features/app-form/controls/SelectInput";
import { TagInput } from "@/features/app-form/controls/TagInput";
import { useCredentials, useServers } from "@/hooks/queries";
import { errorMessage, fieldErrors, http } from "@/lib/api";
import { timeAgo } from "@/lib/format";
import type { Server, ServerFacts, ServerTestResult } from "@/lib/types";
import { cn } from "@/lib/utils";

/** 服务器环境的检测结果，用小标签展示。 */
export function FactsBadges({ facts }: { facts: ServerFacts }) {
  const tools: [string, boolean, string][] = [
    ["Docker", facts.docker, facts.dockerVersion],
    ["compose", facts.compose, ""],
    ["rsync", facts.rsync, ""],
    ["systemd", facts.systemd, ""],
    ["免密 sudo", facts.sudoNoPassword, ""],
  ];
  return (
    <div className="flex flex-wrap gap-1.5">
      <Badge variant="secondary" className="font-mono">
        {facts.machine}
        {facts.arch && facts.arch !== facts.machine ? ` · ${facts.arch}` : ""}
      </Badge>
      {facts.os && <Badge variant="secondary">{facts.os}</Badge>}
      {facts.user && <Badge variant="secondary">用户 {facts.user}</Badge>}
      {tools.map(([name, ok, extra]) => (
        <Badge key={name} variant="outline" className={cn(ok ? "text-success" : "text-muted-foreground line-through")}>
          {name}
          {extra && ` ${extra}`}
        </Badge>
      ))}
    </div>
  );
}

function TestResult({ result }: { result: ServerTestResult }) {
  return result.ok && result.facts ? (
    <div className="space-y-2 rounded-md border border-success/40 bg-success/5 p-3">
      <p className="flex items-center gap-1.5 text-sm text-success">
        <CheckCircle2 className="size-4" />
        连接成功（{result.durationMs} ms）
      </p>
      <FactsBadges facts={result.facts} />
    </div>
  ) : (
    <div className="space-y-1 rounded-md border border-destructive/40 bg-destructive/5 p-3">
      <p className="flex items-center gap-1.5 text-sm text-destructive">
        <XCircle className="size-4" />
        {result.error}
      </p>
      {result.detail && <pre className="overflow-x-auto font-mono text-xs whitespace-pre-wrap text-muted-foreground">{result.detail}</pre>}
    </div>
  );
}

interface FormState {
  name: string;
  title: string;
  host: string;
  port: string;
  user: string;
  keyPath: string;
  keyCredential: string;
  sshOptions: string[];
}

const empty: FormState = { name: "", title: "", host: "", port: "22", user: "", keyPath: "~/.ssh/id_ed25519", keyCredential: "", sshOptions: [] };

function toState(s: Server): FormState {
  return { name: s.name, title: s.title, host: s.host, port: String(s.port), user: s.user, keyPath: s.keyPath, keyCredential: s.keyCredential, sshOptions: s.sshOptions };
}

/** 新增或编辑服务器。可以先测试连接再保存。 */
function ServerDialog({ open, onOpenChange, server }: { open: boolean; onOpenChange: (v: boolean) => void; server?: Server }) {
  const qc = useQueryClient();
  const [form, setForm] = useState<FormState>(server ? toState(server) : empty);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [test, setTest] = useState<ServerTestResult>();
  const set = (patch: Partial<FormState>) => setForm((f) => ({ ...f, ...patch }));
  // 选了平台保存的密钥就不用密钥文件
  const body = () => ({ ...form, port: Number(form.port) || 0, keyPath: form.keyCredential ? "" : form.keyPath });
  const { data: credentials } = useCredentials();
  const keys = (credentials ?? []).filter((c) => c.kind === "sshkey");

  const testMut = useMutation({
    mutationFn: () => http.post<ServerTestResult>("/api/servers/test", body()),
    onMutate: () => setTest(undefined),
    onSuccess: (r) => {
      setErrors({});
      setTest(r);
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      toast.error(errorMessage(e));
    },
  });
  const saveMut = useMutation({
    mutationFn: () => (server ? http.put<Server>(`/api/servers/${server.name}`, body()) : http.post<Server>("/api/servers", body())),
    onSuccess: async (saved) => {
      // 保存前测试成功过，就顺手把检测结果存下来
      if (test?.ok) await http.post(`/api/servers/${saved.name}/test`).catch(() => undefined);
      await qc.invalidateQueries({ queryKey: ["servers"] });
      toast.success(server ? "已保存" : `已添加服务器 ${saved.name}`);
      onOpenChange(false);
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      toast.error(errorMessage(e));
    },
  });

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{server ? `编辑服务器 ${server.name}` : "添加服务器"}</DialogTitle>
          <DialogDescription>只支持 SSH 密钥登录。密钥可以用平台保存的 SSH 私钥（在「凭据」里添加），也可以用运行 deployhub 的机器上的密钥文件。</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            saveMut.mutate();
          }}
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <FormItem id="srv-name" label="名称" required error={errors.name} help={server ? "创建后不能修改" : "应用里用它引用服务器，如 shop-main；创建后不能修改"}>
              <Input id="srv-name" value={form.name} disabled={!!server} onChange={(e) => set({ name: e.target.value })} placeholder="shop-main" />
            </FormItem>
            <FormItem id="srv-title" label="说明" error={errors.title}>
              <Input id="srv-title" value={form.title} onChange={(e) => set({ title: e.target.value })} placeholder="支付主服务器" />
            </FormItem>
          </div>
          <div className="grid gap-4 sm:grid-cols-[1fr_120px]">
            <FormItem id="srv-host" label="地址" required error={errors.host}>
              <Input id="srv-host" value={form.host} onChange={(e) => set({ host: e.target.value })} placeholder="1.2.3.4 或 example.com" />
            </FormItem>
            <FormItem id="srv-port" label="端口" required error={errors.port}>
              <Input id="srv-port" inputMode="numeric" value={form.port} onChange={(e) => set({ port: e.target.value })} />
            </FormItem>
          </div>
          <FormItem id="srv-user" label="用户" error={errors.user} help="留空时用 ~/.ssh/config 或运行 deployhub 的系统用户名">
            <Input id="srv-user" value={form.user} onChange={(e) => set({ user: e.target.value })} placeholder="deploy" />
          </FormItem>
          <div className="grid gap-4 sm:grid-cols-2">
            <FormItem
              id="srv-key-cred"
              label="平台保存的 SSH 私钥"
              error={errors.keyCredential}
              help={keys.length ? "不选时用右边的密钥文件" : "还没有，可以在「凭据」里添加 SSH 私钥；不用的话填右边的密钥文件"}
            >
              <SelectInput
                id="srv-key-cred"
                value={form.keyCredential}
                onChange={(v) => set({ keyCredential: v })}
                options={keys.map((c) => ({ value: c.name, label: c.name, help: c.title }))}
                placeholder="不使用"
                clearable
                emptyText="还没有 SSH 私钥凭据"
              />
            </FormItem>
            <FormItem
              id="srv-key"
              label="密钥文件"
              error={errors.keyPath}
              help={form.keyCredential ? "已选平台保存的私钥，不用密钥文件" : "运行 deployhub 的机器上的路径；留空时用 ssh 默认的密钥和 ssh-agent"}
            >
              <Input
                id="srv-key"
                value={form.keyCredential ? "" : form.keyPath}
                disabled={!!form.keyCredential}
                onChange={(e) => set({ keyPath: e.target.value })}
                className="font-mono text-sm"
              />
            </FormItem>
          </div>
          <FormItem label="额外的 SSH 选项" error={errors.sshOptions} help="写成 名称=值，回车添加；新服务器没确认过指纹时可以加 StrictHostKeyChecking=accept-new">
            <TagInput value={form.sshOptions} onChange={(v) => set({ sshOptions: v })} placeholder="StrictHostKeyChecking=accept-new" />
          </FormItem>
          {test && <TestResult result={test} />}
          <DialogFooter className="gap-2">
            <Button type="button" variant="outline" onClick={() => testMut.mutate()} disabled={testMut.isPending}>
              {testMut.isPending ? <Loader2 className="animate-spin" /> : <PlugZap />}
              测试连接
            </Button>
            <Button type="submit" disabled={saveMut.isPending}>
              {saveMut.isPending && <Loader2 className="animate-spin" />}
              保存
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function ServerCard({ server, onEdit }: { server: Server; onEdit: () => void }) {
  const qc = useQueryClient();
  const testMut = useMutation({
    mutationFn: () => http.post<ServerTestResult>(`/api/servers/${server.name}/test`),
    onSuccess: async (r) => {
      await qc.invalidateQueries({ queryKey: ["servers"] });
      if (r.ok) toast.success(`${server.name} 连接成功`);
      else toast.error(r.error, { description: r.detail });
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const target = `${server.user ? server.user + "@" : ""}${server.host}${server.port !== 22 ? ":" + server.port : ""}`;
  return (
    <Card className="gap-4">
      <CardHeader className="gap-1">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <CardTitle className="flex items-center gap-2 text-base">
              <ServerIcon className="size-4 text-muted-foreground" />
              {server.name}
            </CardTitle>
            <p className="mt-1 truncate font-mono text-xs text-muted-foreground">{target}</p>
            {server.title && <p className="mt-1 text-sm text-muted-foreground">{server.title}</p>}
          </div>
          <div className="flex shrink-0 gap-1">
            <Button variant="ghost" size="icon-sm" aria-label="编辑" onClick={onEdit}>
              <Pencil />
            </Button>
            <ConfirmButton
              trigger={
                <Button variant="ghost" size="icon-sm" aria-label="删除">
                  <Trash2 />
                </Button>
              }
              title={`删除服务器 ${server.name}？`}
              description={server.appCount > 0 ? `还有 ${server.appCount} 个应用部署在这台服务器上，需要先把它们改到其他服务器或删除。` : "只删除平台里的记录，不影响服务器本身。"}
              confirmText="删除"
              destructive
              onConfirm={async () => {
                await http.del(`/api/servers/${server.name}`);
                await qc.invalidateQueries({ queryKey: ["servers"] });
                toast.success(`已删除 ${server.name}`);
              }}
            />
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        {server.facts ? (
          <FactsBadges facts={server.facts} />
        ) : (
          <p className="text-sm text-muted-foreground">还没检测过环境，点「测试连接」检测。</p>
        )}
        <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
          <span>
            {server.appCount} 个应用 · {server.checkedAt ? `${timeAgo(server.checkedAt)}检测` : "未检测"}
          </span>
          <Button variant="outline" size="sm" onClick={() => testMut.mutate()} disabled={testMut.isPending}>
            {testMut.isPending ? <Loader2 className="animate-spin" /> : <PlugZap />}
            测试连接
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

export function ServersPage() {
  const { data: servers, isLoading } = useServers();
  const [dialog, setDialog] = useState<{ server?: Server; key: number } | null>(null);
  const openNew = () => setDialog({ key: Date.now() });

  return (
    <>
      <PageHeader
        title="服务器"
        description="部署目标服务器。测试连接时检测架构、Docker、rsync、systemd 和 sudo 是否免密。"
        actions={
          <Button onClick={openNew}>
            <Plus />
            添加服务器
          </Button>
        }
      />
      {isLoading ? (
        <Skeleton className="h-40 w-full" />
      ) : !servers?.length ? (
        <EmptyState
          icon={ServerIcon}
          title="还没有服务器"
          description="添加服务器后，应用就可以选择部署到哪台。"
          action={
            <Button onClick={openNew}>
              <Plus />
              添加服务器
            </Button>
          }
        />
      ) : (
        <div className="grid gap-4 lg:grid-cols-2">
          {servers.map((s) => (
            <ServerCard key={s.name} server={s} onEdit={() => setDialog({ server: s, key: Date.now() })} />
          ))}
        </div>
      )}
      {dialog && <ServerDialog key={dialog.key} open onOpenChange={(v) => !v && setDialog(null)} server={dialog.server} />}
    </>
  );
}
