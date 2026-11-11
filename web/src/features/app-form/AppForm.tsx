import { zodResolver } from "@hookform/resolvers/zod";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2, Save } from "lucide-react";
import { useEffect, useMemo } from "react";
import { Controller, useForm, useWatch, type FieldErrors, type Path, type Resolver } from "react-hook-form";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useCredentials, useServers } from "@/hooks/queries";
import { useDebounced } from "@/hooks/useDebounced";
import { ApiError, errorMessage, http } from "@/lib/api";
import type { App, Catalog, DriverSpec, Field, Preview, RepoInfo } from "@/lib/types";
import { FieldControl, type OptionSources } from "./FieldControl";
import { PlanView } from "./PlanView";
import { RepoCard } from "./RepoCard";
import type { RecordErrors } from "./controls/RecordsInput";
import { buildSchema, initialValues, isVisible, toPayload, valuesFromParams, type FormValues } from "./schema";

interface Props {
  catalog: Catalog;
  driver: DriverSpec;
  project: string;
  /** 编辑已有应用时传入；不传表示新建 */
  app?: App;
  onSaved: (app: App) => void;
}

/** 应用参数表单：通用参数 + 部署方式参数，按分组显示；右侧是部署计划预览和要保存的数据。 */
export function AppForm({ catalog, driver, project, app, onSaved }: Props) {
  const qc = useQueryClient();
  // 通用参数去掉这种部署方式用不到的（如 Workers 没有服务器）
  const fields = useMemo(() => [...catalog.common.filter((f) => !driver.omit?.includes(f.key)), ...driver.fields], [catalog, driver]);
  const resolver = useMemo(() => zodResolver(buildSchema(fields)) as unknown as Resolver<FormValues>, [fields]);
  const form = useForm<FormValues>({
    defaultValues: app ? valuesFromParams(fields, app.params) : initialValues(fields),
    resolver,
    mode: "onSubmit",
    reValidateMode: "onChange",
  });
  const values = (useWatch({ control: form.control }) ?? {}) as FormValues;
  const errors = form.formState.errors;
  const payload = useMemo(() => toPayload(fields, values), [fields, values]);

  // 服务器下拉和服务器检测结果
  const { data: servers } = useServers();
  const server = servers?.find((s) => s.name === values.server);
  const { data: credentials } = useCredentials();

  // 仓库识别：仓库目录停止输入 0.6 秒后识别。代码来源是 git 地址时没有本机目录可以识别，
  // 自动值（构建命令、入口等）要等第一次检查或部署把代码拉下来之后才有。
  const localRepo = values.repo_source !== "git";
  const repoPath = useDebounced(localRepo ? String(values.repo_path ?? "").trim() : "", 600);
  const repoQuery = useQuery({
    queryKey: ["detect", repoPath],
    queryFn: () => http.post<RepoInfo>("/api/detect/repo", { path: repoPath }),
    enabled: repoPath !== "",
    staleTime: 10_000,
  });
  const repo = repoPath ? repoQuery.data : undefined;
  // 部署分支还没填时，用仓库当前分支
  useEffect(() => {
    if (repo?.hints.branch && !form.getValues("branch")) form.setValue("branch", repo.hints.branch, { shouldValidate: form.formState.isSubmitted });
  }, [repo, form]);

  const sources: OptionSources = useMemo(
    () => ({
      servers: (servers ?? []).map((s) => ({ value: s.name, label: s.name, help: `${s.user ? s.user + "@" : ""}${s.host}${s.title ? " · " + s.title : ""}` })),
      credentials: (credentials ?? []).reduce<Record<string, { value: string; label: string; help?: string }[]>>((acc, c) => {
        (acc[c.kind] ??= []).push({ value: c.name, label: c.name, help: [c.title, ...Object.values(c.values)].filter(Boolean).join(" · ") });
        return acc;
      }, {}),
      detect: repo?.options ?? {},
    }),
    [servers, credentials, repo],
  );

  // 留空参数的自动值：仓库识别结果、服务器检测的架构
  const autoHint = (f: Field): string | undefined => {
    const arch = server?.facts?.arch;
    if (f.key === "goarch" && arch) return `${arch}（服务器检测）`;
    if (f.key === "platform" && arch) return `linux/${arch}（服务器检测）`;
    // 只对某种部署方式有效的识别结果写成 <部署方式>.<参数名>，优先用
    const h = repo?.hints[`${driver.name}.${f.key}`] ?? repo?.hints[f.key];
    if (h && f.key !== "branch") return `${h}（识别）`;
    return undefined;
  };

  // 部署计划预览：参数停止变化 0.8 秒后刷新
  const previewBody = useDebounced(JSON.stringify(payload), 800);
  const previewQuery = useQuery({
    queryKey: ["preview", project, app?.name ?? "", driver.name, previewBody],
    queryFn: () =>
      http.post<Preview>(app ? `/api/projects/${project}/apps/${app.name}/preview` : `/api/projects/${project}/apps/preview`, {
        driver: driver.name,
        params: JSON.parse(previewBody),
      }),
    placeholderData: (prev) => prev,
  });

  const save = useMutation({
    mutationFn: () =>
      app
        ? http.put<App>(`/api/projects/${project}/apps/${app.name}`, { params: payload, revision: app.revision })
        : http.post<App>(`/api/projects/${project}/apps`, { driver: driver.name, params: payload }),
    onSuccess: async (saved) => {
      await qc.invalidateQueries({ queryKey: ["projects"] });
      await qc.invalidateQueries({ queryKey: ["secrets"] });
      await qc.invalidateQueries({ queryKey: ["credentials"] });
      toast.success(app ? `已保存，第 ${saved.revision} 版` : `已创建应用 ${saved.title}`);
      onSaved(saved);
    },
    onError: (e) => {
      if (e instanceof ApiError && e.fields.length) {
        for (const f of e.fields) {
          if (f.field) form.setError(f.field as Path<FormValues>, { type: "server", message: f.message });
        }
        scrollToFirstError(e.fields.map((f) => f.field.split(".")[0]));
      }
      toast.error(errorMessage(e));
    },
  });

  // 只显示有字段的分组（不管字段当前是否因显示条件被隐藏）
  const sections = useMemo(
    () => catalog.sections.map((s) => ({ ...s, fields: fields.filter((f) => f.section === s.key) })).filter((s) => s.fields.length > 0),
    [catalog.sections, fields],
  );

  const scrollToFirstError = (keys: string[]) => {
    const first = fields.map((f) => f.key).find((k) => keys.includes(k));
    if (first) document.querySelector(`[data-field="${first}"]`)?.scrollIntoView({ behavior: "smooth", block: "center" });
  };
  const onInvalid = (errs: FieldErrors<FormValues>) => {
    const keys = fields.map((f) => f.key).filter((k) => errs[k]);
    toast.error(`有 ${keys.length} 个参数需要修改`);
    scrollToFirstError(keys);
  };

  const extraHelp = (f: Field) => (f.key === "health_mode" && values.health_mode === "default" ? `默认检查：${driver.defaultCheck}` : undefined);

  return (
    <form onSubmit={form.handleSubmit(() => save.mutate(), onInvalid)} noValidate className="grid gap-6 xl:grid-cols-[150px_minmax(0,1fr)_380px]">
      {/* 分组导航 */}
      <nav aria-label="参数分组" className="hidden xl:block">
        <ul className="sticky top-6 space-y-1 text-sm">
          {sections.map((s) => {
            const hasError = s.fields.some((f) => errors[f.key]);
            return (
              <li key={s.key}>
                <a href={`#section-${s.key}`} className="flex items-center justify-between rounded-md px-2 py-1.5 text-muted-foreground hover:bg-accent hover:text-foreground">
                  {s.label}
                  {hasError && <span className="size-1.5 rounded-full bg-destructive" aria-label="有错误" />}
                </a>
              </li>
            );
          })}
        </ul>
      </nav>

      {/* 表单 */}
      <div className="min-w-0 space-y-4">
        {sections.map((s) => {
          const visible = s.fields.filter((f) => isVisible(f, values));
          return (
            <Card key={s.key} id={`section-${s.key}`} className="scroll-mt-6 gap-4">
              <CardHeader>
                <CardTitle className="text-base">{s.label}</CardTitle>
                {s.help && <CardDescription>{s.help}</CardDescription>}
              </CardHeader>
              <CardContent className="space-y-5">
                {visible.map((f) => (
                  <Controller
                    key={f.key}
                    name={f.key}
                    control={form.control}
                    render={({ field: rhf, fieldState }) => (
                      <div>
                        <FieldControl
                          field={f}
                          value={rhf.value}
                          onChange={rhf.onChange}
                          error={f.type === "records" ? undefined : fieldState.error?.message}
                          recordErrors={f.type === "records" ? (fieldState.error as unknown as RecordErrors) : undefined}
                          sources={sources}
                          autoHint={autoHint(f)}
                          disabled={!!app && f.immutable}
                          extra={f.key === "repo_path" ? <RepoCard repo={repo} loading={repoQuery.isFetching} /> : undefined}
                        />
                        {extraHelp(f) && <p className="mt-2 text-xs text-muted-foreground">{extraHelp(f)}</p>}
                      </div>
                    )}
                  />
                ))}
              </CardContent>
            </Card>
          );
        })}
      </div>

      {/* 右侧：保存、部署计划预览、要保存的数据 */}
      <aside className="min-w-0">
        <div className="space-y-4 xl:sticky xl:top-6">
          <Card className="gap-4">
            <CardHeader>
              <CardTitle className="text-base">{driver.title}</CardTitle>
              <CardDescription className="leading-relaxed">{driver.summary}</CardDescription>
            </CardHeader>
            <CardContent>
              <Button type="submit" className="w-full" disabled={save.isPending}>
                {save.isPending ? <Loader2 className="animate-spin" /> : <Save />}
                {app ? "保存" : "创建应用"}
              </Button>
              {app && <p className="mt-2 text-center text-xs text-muted-foreground">当前第 {app.revision} 版，保存后生成新版本</p>}
            </CardContent>
          </Card>
          <Card className="gap-3">
            <Tabs defaultValue="plan">
              <CardHeader className="flex items-center justify-between">
                <TabsList>
                  <TabsTrigger value="plan">部署计划</TabsTrigger>
                  <TabsTrigger value="data">保存的数据</TabsTrigger>
                </TabsList>
                {previewQuery.isFetching && <Loader2 className="size-4 animate-spin text-muted-foreground" />}
              </CardHeader>
              <CardContent className="mt-3">
                <TabsContent value="plan" className="max-h-[70vh] overflow-y-auto">
                  {previewQuery.data ? (
                    <PlanView preview={previewQuery.data} />
                  ) : previewQuery.error ? (
                    <p className="text-xs text-destructive">{errorMessage(previewQuery.error)}</p>
                  ) : (
                    <Skeleton className="h-40 w-full" />
                  )}
                </TabsContent>
                <TabsContent value="data">
                  <p className="mb-2 text-xs text-muted-foreground">没填的参数不保存，部署时用自动值（见部署计划）</p>
                  <pre className="max-h-[60vh] overflow-auto rounded-md bg-muted p-3 font-mono text-xs leading-relaxed">
                    {JSON.stringify({ driver: driver.name, ...payload }, null, 2)}
                  </pre>
                </TabsContent>
              </CardContent>
            </Tabs>
          </Card>
        </div>
      </aside>
    </form>
  );
}
