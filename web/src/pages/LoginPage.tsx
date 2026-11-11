import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Loader2, LockKeyhole } from "lucide-react";
import { useState } from "react";
import { FormItem } from "@/components/common/FormItem";
import { AuthCard } from "@/components/layout/AuthCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { errorMessage, http } from "@/lib/api";

/** 没有登录时显示。setupRequired：还没有任何账号，要先用终端里的链接创建管理员。 */
export function LoginPage({ setupRequired }: { setupRequired: boolean }) {
  const qc = useQueryClient();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const login = useMutation({
    mutationFn: () => http.post("/api/login", { username, password }),
    // 登录状态变了，所有缓存作废；AuthGate 重新读取会话后显示主界面
    onSuccess: () => qc.resetQueries(),
  });

  if (setupRequired) {
    return (
      <AuthCard icon={LockKeyhole} title="还没有账号" description="先创建管理员账号，之后用用户名和密码登录。">
        <div className="space-y-2 text-sm text-muted-foreground">
          <p>
            在运行 deployhub 的机器上执行 <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-foreground">deployhub</code>
            ，终端会打印一个创建管理员的链接（已经在运行时也一样）。
          </p>
          <p>链接 10 分钟内有效，只能用一次。</p>
        </div>
      </AuthCard>
    );
  }

  return (
    <AuthCard icon={LockKeyhole} title="登录 deployhub" description="用管理员给你的用户名和密码登录。">
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          login.mutate();
        }}
      >
        {login.isError && (
          <Alert variant="destructive">
            <AlertDescription>{errorMessage(login.error)}</AlertDescription>
          </Alert>
        )}
        <FormItem id="login-username" label="用户名">
          <Input id="login-username" name="username" autoComplete="username" autoCapitalize="none" autoFocus value={username} onChange={(e) => setUsername(e.target.value)} />
        </FormItem>
        <FormItem id="login-password" label="密码">
          <Input id="login-password" name="password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        </FormItem>
        <Button type="submit" className="w-full" disabled={!username || !password || login.isPending}>
          {login.isPending && <Loader2 className="animate-spin" />}
          登录
        </Button>
        <p className="text-xs leading-relaxed text-muted-foreground">忘记密码请联系管理员重置。</p>
      </form>
    </AuthCard>
  );
}
