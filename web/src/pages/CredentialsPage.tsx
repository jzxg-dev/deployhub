import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, CircleAlert, Cloud, Loader2, Pencil, Plus, ShieldCheck, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ConfirmButton } from "@/components/common/ConfirmButton";
import { FormItem } from "@/components/common/FormItem";
import { EmptyState, PageHeader } from "@/components/layout/Page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Textarea } from "@/components/ui/textarea";
import { SelectInput } from "@/features/app-form/controls/SelectInput";
import { useCatalog, useCredentials } from "@/hooks/queries";
import { errorMessage, fieldErrors, http } from "@/lib/api";
import { timeAgo } from "@/lib/format";
import type { Credential, CredentialKind, InfoItem } from "@/lib/types";

function InfoList({ info }: { info: InfoItem[] }) {
  return (
    <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs">
      {info.map((it) => (
        <div key={it.label} className="contents">
          <dt className="text-muted-foreground">{it.label}</dt>
          <dd className="break-all">{it.value}</dd>
        </div>
      ))}
    </dl>
  );
}

function CredentialDialog({ kinds, credential, onClose }: { kinds: CredentialKind[]; credential?: Credential; onClose: () => void }) {
  const qc = useQueryClient();
  const [kindName, setKindName] = useState(credential?.kind ?? kinds[0]?.name ?? "");
  const [name, setName] = useState(credential?.name ?? "");
  const [title, setTitle] = useState(credential?.title ?? "");
  const [values, setValues] = useState<Record<string, string>>(credential?.values ?? {});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [tested, setTested] = useState<{ info?: InfoItem[]; error?: string }>();
  const kind = kinds.find((k) => k.name === kindName);
  const body = () => ({ name, kind: kindName, title, values });

  const save = useMutation({
    mutationFn: () => (credential ? http.put(`/api/credentials/${credential.name}`, body()) : http.post("/api/credentials", body())),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["credentials"] });
      toast.success(credential ? "已保存" : `已添加 ${name}`);
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      toast.error(errorMessage(e));
    },
  });
  const test = useMutation({
    mutationFn: () => http.post<{ info: InfoItem[] }>(`/api/credentials/test${credential ? `?name=${encodeURIComponent(credential.name)}` : ""}`, body()),
    onMutate: () => setTested(undefined),
    onSuccess: (r) => {
      setErrors({});
      setTested({ info: r.info });
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      setTested({ error: errorMessage(e) });
    },
  });

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{credential ? `编辑 ${credential.name}` : "添加凭据"}</DialogTitle>
          <DialogDescription>{kind?.help ?? "成组的登录信息，敏感字段加密保存。"}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          {!credential && kinds.length > 1 && (
            <FormItem label="种类" required error={errors.kind}>
              <SelectInput
                value={kindName}
                onChange={(v) => {
                  setKindName(v);
                  // 换种类后字段不同，清掉已经填的值和验证结果
                  setValues({});
                  setErrors({});
                  setTested(undefined);
                }}
                options={kinds.map((k) => ({ value: k.name, label: k.title }))}
              />
            </FormItem>
          )}
          <FormItem id="cred-name" label="标识" required error={errors.name} help={credential ? "创建后不能修改" : "应用和服务器里用它选择凭据，如 cf-main、deploy-key"}>
            <Input id="cred-name" value={name} disabled={!!credential} onChange={(e) => setName(e.target.value.toLowerCase())} className="font-mono" placeholder={kindName === "sshkey" ? "deploy-key" : "cf-main"} />
          </FormItem>
          <FormItem id="cred-title" label="名称" error={errors.title}>
            <Input id="cred-title" value={title} onChange={(e) => setTitle(e.target.value)} placeholder={kindName === "sshkey" ? "部署用的密钥" : "主账号"} />
          </FormItem>
          {kind?.fields.map((f) => {
            const saved = !!credential?.secrets.includes(f.key);
            return (
              <FormItem
                key={f.key}
                id={`cred-${f.key}`}
                label={f.label}
                required={f.required && !saved}
                error={errors[f.key]}
                help={f.sensitive && saved ? `已保存，留空表示不修改。${f.help ?? ""}` : f.help}
              >
                {f.type === "textarea" ? (
                  // 多行内容（如 SSH 私钥）：原样保存，换行有意义，不去空白
                  <Textarea
                    id={`cred-${f.key}`}
                    autoComplete="off"
                    spellCheck={false}
                    className="max-h-48 min-h-28 font-mono text-xs break-all"
                    value={values[f.key] ?? ""}
                    placeholder={f.sensitive && saved ? "（已保存，留空表示不修改）" : f.placeholder}
                    onChange={(e) => setValues((v) => ({ ...v, [f.key]: e.target.value }))}
                  />
                ) : (
                  <Input
                    id={`cred-${f.key}`}
                    type={f.sensitive ? "password" : "text"}
                    autoComplete={f.sensitive ? "new-password" : "off"}
                    spellCheck={false}
                    className="font-mono text-sm"
                    value={values[f.key] ?? ""}
                    placeholder={f.sensitive && saved ? "••••••••（已保存）" : f.placeholder}
                    onChange={(e) => setValues((v) => ({ ...v, [f.key]: e.target.value.trim() }))}
                  />
                )}
              </FormItem>
            );
          })}
          {tested && (
            <div className={`rounded-md border p-3 ${tested.error ? "border-destructive/50" : "border-success/40"}`}>
              <p className={`mb-2 flex items-center gap-1.5 text-sm font-medium ${tested.error ? "text-destructive" : "text-success"}`}>
                {tested.error ? <CircleAlert className="size-4" /> : <CheckCircle2 className="size-4" />}
                {tested.error ? "验证没有通过" : "验证通过"}
              </p>
              {tested.error ? <p className="text-xs break-all text-muted-foreground">{tested.error}</p> : <InfoList info={tested.info ?? []} />}
            </div>
          )}
          <DialogFooter className="gap-2">
            <Button type="button" variant="outline" onClick={() => test.mutate()} disabled={test.isPending}>
              {test.isPending ? <Loader2 className="animate-spin" /> : <ShieldCheck />}
              验证
            </Button>
            <Button type="submit" disabled={save.isPending}>
              {save.isPending && <Loader2 className="animate-spin" />}
              保存
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function VerifyButton({ name }: { name: string }) {
  const qc = useQueryClient();
  const verify = useMutation({
    mutationFn: () => http.post<Credential>(`/api/credentials/${name}/test`),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["credentials"] });
      toast.success(`${name} 验证通过`);
    },
    onError: (e) => toast.error(`${name} 验证没有通过`, { description: errorMessage(e) }),
  });
  return (
    <Button variant="ghost" size="sm" onClick={() => verify.mutate()} disabled={verify.isPending}>
      {verify.isPending ? <Loader2 className="animate-spin" /> : <ShieldCheck />}
      验证
    </Button>
  );
}

export function CredentialsPage() {
  const qc = useQueryClient();
  const { data: catalog } = useCatalog();
  const { data: list, isLoading } = useCredentials();
  const [dialog, setDialog] = useState<{ credential?: Credential } | null>(null);
  const kinds = catalog?.credentialKinds ?? [];
  const kindOf = (n: string) => kinds.find((k) => k.name === n);
  const addButton = (
    <Button onClick={() => setDialog({})} disabled={!kinds.length}>
      <Plus />
      添加
    </Button>
  );

  return (
    <>
      <PageHeader title="凭据" description="成组的登录信息：Cloudflare 账号，登录服务器和拉取 git 仓库用的 SSH 私钥。敏感字段加密保存，页面上不显示。" actions={addButton} />
      {isLoading ? (
        <Skeleton className="h-40 w-full" />
      ) : !list?.length ? (
        <EmptyState icon={Cloud} title="还没有凭据" description="部署 Cloudflare Workers 前先添加 Cloudflare 账号；想让平台保存 SSH 私钥（不依赖机器上的密钥文件）就添加 SSH 私钥。" action={addButton} />
      ) : (
        <Card className="gap-0 overflow-hidden py-0">
          <ul className="divide-y">
            {list.map((c) => {
              const kind = kindOf(c.kind);
              const missing = (kind?.fields ?? []).filter((f) => f.sensitive && f.required && !c.secrets.includes(f.key));
              return (
                <li key={c.name} className="grid gap-3 px-4 py-3 md:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)_auto] md:items-center">
                  <div className="min-w-0 space-y-1">
                    <p className="flex flex-wrap items-center gap-2">
                      <span className="truncate font-mono text-sm font-medium">{c.name}</span>
                      <Badge variant="secondary">{kind?.title ?? c.kind}</Badge>
                    </p>
                    {c.title && <p className="truncate text-xs text-muted-foreground">{c.title}</p>}
                    <p className="truncate font-mono text-xs text-muted-foreground">
                      {Object.entries(c.values)
                        .map(([k, v]) => `${kind?.fields.find((f) => f.key === k)?.label ?? k} ${v}`)
                        .join(" · ")}
                    </p>
                    {missing.length > 0 && (
                      <p className="flex items-center gap-1 text-xs text-warning">
                        <CircleAlert className="size-3" />
                        没有填写 {missing.map((f) => f.label).join("、")}，部署前需要补上
                      </p>
                    )}
                  </div>
                  <div className="min-w-0 space-y-2">
                    {c.info.length > 0 ? (
                      <>
                        <InfoList info={c.info} />
                        {c.checkedAt && <p className="text-xs text-muted-foreground">{timeAgo(c.checkedAt)}验证</p>}
                      </>
                    ) : (
                      <p className="text-xs text-muted-foreground">还没有验证过</p>
                    )}
                    <div className="flex flex-wrap gap-1">
                      {c.usedBy.length === 0 ? (
                        <span className="text-xs text-muted-foreground">还没有被使用</span>
                      ) : (
                        c.usedBy.map((u) => (
                          <Badge key={u} variant="outline" className="font-mono font-normal">
                            {u}
                          </Badge>
                        ))
                      )}
                    </div>
                  </div>
                  <div className="flex items-center gap-1 justify-self-end">
                    <VerifyButton name={c.name} />
                    <Button variant="ghost" size="icon-sm" aria-label="编辑" onClick={() => setDialog({ credential: c })}>
                      <Pencil />
                    </Button>
                    <ConfirmButton
                      trigger={
                        <Button variant="ghost" size="icon-sm" aria-label="删除">
                          <Trash2 />
                        </Button>
                      }
                      title={`删除 ${c.name}？`}
                      description={c.usedBy.length ? `还有应用在用它：${c.usedBy.join("、")}，需要先修改这些应用。` : "删除后无法恢复。"}
                      confirmText="删除"
                      destructive
                      onConfirm={async () => {
                        await http.del(`/api/credentials/${c.name}`);
                        await qc.invalidateQueries({ queryKey: ["credentials"] });
                        toast.success(`已删除 ${c.name}`);
                      }}
                    />
                  </div>
                </li>
              );
            })}
          </ul>
        </Card>
      )}
      {dialog && <CredentialDialog kinds={kinds} credential={dialog.credential} onClose={() => setDialog(null)} />}
    </>
  );
}
