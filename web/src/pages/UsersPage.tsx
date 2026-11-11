import { useMutation, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Loader2, Plus, ShieldCheck, Trash2, UserCheck, UserX } from "lucide-react";
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
import { useSession, useUsers } from "@/hooks/queries";
import { errorMessage, fieldErrors, http } from "@/lib/api";
import { timeAgo } from "@/lib/format";
import type { User } from "@/lib/types";

/** 新建账号（user 为空），或给已有账号重置密码。 */
function UserDialog({ user, onClose }: { user?: User; onClose: () => void }) {
  const qc = useQueryClient();
  const [username, setUsername] = useState(user?.username ?? "");
  const [password, setPassword] = useState("");
  const [admin, setAdmin] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => (user ? http.put(`/api/users/${user.username}`, { password }) : http.post("/api/users", { username, password, admin })),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["users"] });
      toast.success(user ? `已重置 ${user.username} 的密码` : `已创建账号 ${username}`, {
        description: user ? "这个账号已登录的页面需要重新登录。" : "把用户名和密码告诉对方，让对方登录后在「设置」里改密码。",
      });
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      toast.error(errorMessage(e));
    },
  });
  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{user ? `重置 ${user.username} 的密码` : "新建账号"}</DialogTitle>
          <DialogDescription>
            {user ? "设置一个新密码并告诉对方。对方已登录的页面会立即失效。" : "新账号从空白开始，有自己的项目、服务器、密钥和凭据，看不到其他账号的数据。"}
          </DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          {!user && (
            <FormItem id="user-name" label="用户名" required error={errors.username} help="小写字母开头，可以有数字、- 和 _；创建后不能修改">
              <Input id="user-name" value={username} autoComplete="off" autoCapitalize="none" onChange={(e) => setUsername(e.target.value.toLowerCase())} className="font-mono" />
            </FormItem>
          )}
          <FormItem id="user-password" label={user ? "新密码" : "初始密码"} required error={errors.password} help="至少 8 个字符">
            <Input id="user-password" type="text" value={password} autoComplete="off" spellCheck={false} onChange={(e) => setPassword(e.target.value)} className="font-mono" />
          </FormItem>
          {!user && (
            <label className="flex items-start gap-2 text-sm">
              <input type="checkbox" className="mt-0.5 size-4 accent-primary" checked={admin} onChange={(e) => setAdmin(e.target.checked)} />
              <span>
                设为管理员
                <span className="block text-xs text-muted-foreground">管理员可以新建、停用、删除账号和重置密码，但同样看不到别人的项目。</span>
              </span>
            </label>
          )}
          <DialogFooter>
            <Button type="submit" disabled={(!user && !username) || !password || save.isPending}>
              {save.isPending && <Loader2 className="animate-spin" />}
              {user ? "重置密码" : "创建"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function UsersPage() {
  const qc = useQueryClient();
  const { data: session } = useSession();
  const { data: users, isLoading, error } = useUsers(!!session?.user.admin);
  const [dialog, setDialog] = useState<{ user?: User } | null>(null);

  const update = useMutation({
    mutationFn: ({ user, body }: { user: User; body: { admin?: boolean; disabled?: boolean } }) => http.put(`/api/users/${user.username}`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["users"] }),
    onError: (e) => toast.error(errorMessage(e)),
  });

  if (session && !session.user.admin) {
    return <EmptyState icon={ShieldCheck} title="只有管理员可以管理账号" description="需要新建账号或重置密码时，请联系管理员。" />;
  }

  return (
    <>
      <PageHeader
        title="账号"
        description="每个账号有自己的项目、服务器、密钥、凭据和部署记录，互相看不到。"
        actions={
          <Button onClick={() => setDialog({})}>
            <Plus />
            新建账号
          </Button>
        }
      />
      {isLoading ? (
        <Skeleton className="h-40 w-full" />
      ) : error ? (
        <p className="text-sm text-destructive">{errorMessage(error)}</p>
      ) : (
        <Card className="gap-0 overflow-hidden py-0">
          <ul className="divide-y">
            {(users ?? []).map((u) => {
              const self = u.username === session?.user.username;
              return (
                <li key={u.username} className="grid gap-2 px-4 py-3 md:grid-cols-[minmax(0,1fr)_auto] md:items-center">
                  <div className="min-w-0 space-y-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="truncate font-mono text-sm font-medium">{u.username}</span>
                      {self && <Badge variant="outline">当前账号</Badge>}
                      {u.admin && <Badge variant="secondary">管理员</Badge>}
                      {u.disabled && <Badge variant="destructive">已停用</Badge>}
                    </div>
                    <p className="text-xs text-muted-foreground">
                      {u.projectCount} 个项目 · {u.lastLoginAt ? `${timeAgo(u.lastLoginAt)}登录` : "还没登录过"}
                    </p>
                  </div>
                  {/* 自己的账号在「设置」里改密码；不能停用、降级或删除自己 */}
                  {!self && (
                    <div className="flex flex-wrap items-center gap-1 md:justify-self-end">
                      <Button variant="ghost" size="sm" onClick={() => setDialog({ user: u })}>
                        <KeyRound />
                        重置密码
                      </Button>
                      <Button variant="ghost" size="sm" disabled={update.isPending} onClick={() => update.mutate({ user: u, body: { admin: !u.admin } })}>
                        <ShieldCheck />
                        {u.admin ? "取消管理员" : "设为管理员"}
                      </Button>
                      <Button variant="ghost" size="sm" disabled={update.isPending} onClick={() => update.mutate({ user: u, body: { disabled: !u.disabled } })}>
                        {u.disabled ? <UserCheck /> : <UserX />}
                        {u.disabled ? "启用" : "停用"}
                      </Button>
                      <ConfirmButton
                        trigger={
                          <Button variant="ghost" size="icon-sm" aria-label={`删除 ${u.username}`}>
                            <Trash2 />
                          </Button>
                        }
                        title={`删除账号 ${u.username}？`}
                        description={`这个账号的全部数据会一起删除：${u.projectCount} 个项目和里面的应用、服务器、密钥、凭据、部署记录。删除后无法恢复。只是不想让对方继续登录的话，用「停用」。`}
                        confirmText="删除账号和数据"
                        destructive
                        onConfirm={async () => {
                          await http.del(`/api/users/${u.username}`);
                          await qc.invalidateQueries({ queryKey: ["users"] });
                          toast.success(`已删除账号 ${u.username}`);
                        }}
                      />
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        </Card>
      )}
      {dialog && <UserDialog user={dialog.user} onClose={() => setDialog(null)} />}
    </>
  );
}
