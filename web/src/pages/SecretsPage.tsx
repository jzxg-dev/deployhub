import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Eye, EyeOff, KeyRound, Loader2, Pencil, Plus, Trash2 } from "lucide-react";
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
import { useSecrets } from "@/hooks/queries";
import { errorMessage, fieldErrors, http } from "@/lib/api";
import { timeAgo } from "@/lib/format";
import type { Secret } from "@/lib/types";

function SecretDialog({ secret, onClose }: { secret?: Secret; onClose: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState(secret?.name ?? "");
  const [value, setValue] = useState("");
  const [description, setDescription] = useState(secret?.description ?? "");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () =>
      secret
        ? // 修改时值留空表示不改
          http.put(`/api/secrets/${secret.name}`, { value: value === "" ? null : value, description })
        : http.post("/api/secrets", { name, value, description }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["secrets"] });
      toast.success(secret ? "已保存" : `已添加 ${name}`);
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      toast.error(errorMessage(e));
    },
  });
  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{secret ? `编辑 ${secret.name}` : "添加密钥或变量"}</DialogTitle>
          <DialogDescription>值加密保存。在应用参数（文本、列表、键值对）里用 {"${名称}"} 引用，部署时替换。</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <FormItem id="sec-name" label="名称" required error={errors.name} help={secret ? "创建后不能修改" : "大写字母、数字和下划线，如 PAYMENT_API_URL"}>
            <Input id="sec-name" value={name} disabled={!!secret} onChange={(e) => setName(e.target.value.toUpperCase())} className="font-mono" placeholder="PAYMENT_API_URL" />
          </FormItem>
          <FormItem id="sec-value" label="值" required={!secret} error={errors.value} help={secret ? "留空表示不修改" : undefined}>
            <Textarea id="sec-value" value={value} onChange={(e) => setValue(e.target.value)} className="min-h-20 font-mono text-sm" autoComplete="off" spellCheck={false} />
          </FormItem>
          <FormItem id="sec-desc" label="说明" error={errors.description}>
            <Input id="sec-desc" value={description} onChange={(e) => setDescription(e.target.value)} placeholder="收银台请求的接口地址" />
          </FormItem>
          <DialogFooter>
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

/** 点击才向后端要明文，再点一次隐藏。 */
function RevealValue({ name }: { name: string }) {
  const [value, setValue] = useState<string>();
  const reveal = useMutation({
    mutationFn: () => http.post<{ value: string }>(`/api/secrets/${name}/reveal`),
    onSuccess: (r) => setValue(r.value),
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <div className="flex min-w-0 items-center gap-1">
      <code className="min-w-0 truncate rounded bg-muted px-1.5 py-0.5 font-mono text-xs">{value ?? "••••••••"}</code>
      <Button
        variant="ghost"
        size="icon-xs"
        aria-label={value === undefined ? "显示值" : "隐藏值"}
        onClick={() => (value === undefined ? reveal.mutate() : setValue(undefined))}
      >
        {reveal.isPending ? <Loader2 className="animate-spin" /> : value === undefined ? <Eye /> : <EyeOff />}
      </Button>
    </div>
  );
}

export function SecretsPage() {
  const qc = useQueryClient();
  const { data: secrets, isLoading } = useSecrets();
  const [dialog, setDialog] = useState<{ secret?: Secret } | null>(null);

  return (
    <>
      <PageHeader
        title="密钥与变量"
        description="构建和运行需要的敏感值，加密保存，在参数里用 ${名称} 引用。"
        actions={
          <Button onClick={() => setDialog({})}>
            <Plus />
            添加
          </Button>
        }
      />
      {isLoading ? (
        <Skeleton className="h-40 w-full" />
      ) : !secrets?.length ? (
        <EmptyState
          icon={KeyRound}
          title="还没有密钥"
          description={"比如收银台构建时要用的接口地址，添加后在参数里写 ${PAYMENT_API_URL}。"}
          action={
            <Button onClick={() => setDialog({})}>
              <Plus />
              添加
            </Button>
          }
        />
      ) : (
        <Card className="gap-0 overflow-hidden py-0">
          <ul className="divide-y">
            {secrets.map((s) => (
              <li key={s.name} className="grid gap-2 px-4 py-3 md:grid-cols-[minmax(0,14rem)_minmax(0,1fr)_minmax(0,14rem)_auto] md:items-center">
                <div className="min-w-0">
                  <p className="truncate font-mono text-sm font-medium">{s.name}</p>
                  {s.description && <p className="truncate text-xs text-muted-foreground">{s.description}</p>}
                </div>
                <RevealValue name={s.name} />
                <div className="flex flex-wrap gap-1">
                  {s.usedBy.length === 0 ? (
                    <span className="text-xs text-muted-foreground">没有应用引用</span>
                  ) : (
                    s.usedBy.map((u) => (
                      <Badge key={u} variant="outline" className="font-mono font-normal">
                        {u}
                      </Badge>
                    ))
                  )}
                </div>
                <div className="flex items-center gap-1 justify-self-end">
                  <span className="mr-2 text-xs text-muted-foreground">{timeAgo(s.updatedAt)}更新</span>
                  <Button variant="ghost" size="icon-sm" aria-label="编辑" onClick={() => setDialog({ secret: s })}>
                    <Pencil />
                  </Button>
                  <ConfirmButton
                    trigger={
                      <Button variant="ghost" size="icon-sm" aria-label="删除">
                        <Trash2 />
                      </Button>
                    }
                    title={`删除 ${s.name}？`}
                    description={s.usedBy.length ? `还有应用引用它：${s.usedBy.join("、")}，需要先修改这些应用。` : "删除后无法恢复。"}
                    confirmText="删除"
                    destructive
                    onConfirm={async () => {
                      await http.del(`/api/secrets/${s.name}`);
                      await qc.invalidateQueries({ queryKey: ["secrets"] });
                      toast.success(`已删除 ${s.name}`);
                    }}
                  />
                </div>
              </li>
            ))}
          </ul>
        </Card>
      )}
      {dialog && <SecretDialog secret={dialog.secret} onClose={() => setDialog(null)} />}
    </>
  );
}
