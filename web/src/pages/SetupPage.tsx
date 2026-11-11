import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Loader2, UserPlus } from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router";
import { FormItem } from "@/components/common/FormItem";
import { AuthCard } from "@/components/layout/AuthCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { errorMessage, fieldErrors, http } from "@/lib/api";

/** 创建第一个管理员：只能通过终端里打印的一次性链接（/setup?token=…）打开。 */
export function SetupPage() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [token] = useState(() => new URLSearchParams(window.location.search).get("token") ?? "");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const create = useMutation({
    mutationFn: () => http.post("/api/setup", { token, username, password }),
    onSuccess: async () => {
      await qc.resetQueries();
      // replace：带令牌的地址不留在浏览器历史里
      await navigate("/", { replace: true });
    },
    onError: (e) => setErrors(fieldErrors(e)),
  });

  return (
    <AuthCard
      icon={UserPlus}
      title="创建管理员"
      description="这是 deployhub 的第一个账号。管理员可以给其他人开账号；每个账号只能看到自己的项目、服务器和密钥。"
    >
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          if (password !== confirm) {
            setErrors({ confirm: "两次输入的密码不一样" });
            return;
          }
          setErrors({});
          create.mutate();
        }}
      >
        {!token && (
          <Alert variant="destructive">
            <AlertDescription>地址里没有令牌，请用终端里打印的完整链接打开。</AlertDescription>
          </Alert>
        )}
        {create.isError && Object.keys(errors).length === 0 && (
          <Alert variant="destructive">
            <AlertDescription>{errorMessage(create.error)}</AlertDescription>
          </Alert>
        )}
        <FormItem id="setup-username" label="用户名" required error={errors.username} help="小写字母开头，可以有数字、- 和 _">
          <Input id="setup-username" name="username" autoComplete="username" autoCapitalize="none" autoFocus value={username} onChange={(e) => setUsername(e.target.value.toLowerCase())} />
        </FormItem>
        <FormItem id="setup-password" label="密码" required error={errors.password} help="至少 8 个字符">
          <Input id="setup-password" name="password" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        </FormItem>
        <FormItem id="setup-confirm" label="再输一次密码" required error={errors.confirm}>
          <Input id="setup-confirm" type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        </FormItem>
        <Button type="submit" className="w-full" disabled={!token || !username || !password || !confirm || create.isPending}>
          {create.isPending && <Loader2 className="animate-spin" />}
          创建并登录
        </Button>
      </form>
    </AuthCard>
  );
}
