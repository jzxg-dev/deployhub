import { useMutation, useQueryClient } from "@tanstack/react-query";
import { FolderKanban, Loader2, Plus } from "lucide-react";
import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { toast } from "sonner";
import { FormItem } from "@/components/common/FormItem";
import { EmptyState, PageHeader } from "@/components/layout/Page";
import { Button } from "@/components/ui/button";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Textarea } from "@/components/ui/textarea";
import { useProjects } from "@/hooks/queries";
import { errorMessage, fieldErrors, http } from "@/lib/api";
import { timeAgo } from "@/lib/format";
import type { Project } from "@/lib/types";

/** 新建或编辑项目。 */
export function ProjectDialog({ project, onClose }: { project?: Project; onClose: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [form, setForm] = useState({ name: project?.name ?? "", title: project?.title ?? "", description: project?.description ?? "" });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => (project ? http.put<Project>(`/api/projects/${project.name}`, form) : http.post<Project>("/api/projects", form)),
    onSuccess: async (p) => {
      await qc.invalidateQueries({ queryKey: ["projects"] });
      toast.success(project ? "已保存" : `已创建项目 ${p.title}`);
      onClose();
      if (!project) navigate(`/projects/${p.name}`);
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
          <DialogTitle>{project ? "编辑项目" : "新建项目"}</DialogTitle>
          <DialogDescription>项目把相关的应用放在一起，如 shop 下的后端、管理后台和收银台。</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <FormItem id="p-name" label="标识" required error={errors.name} help={project ? "创建后不能修改" : "用在地址里，小写字母、数字和 -，创建后不能修改"}>
            <Input id="p-name" value={form.name} disabled={!!project} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="shop" className="font-mono" />
          </FormItem>
          <FormItem id="p-title" label="名称" required error={errors.title}>
            <Input id="p-title" value={form.title} onChange={(e) => setForm({ ...form, title: e.target.value })} placeholder="示例商城" />
          </FormItem>
          <FormItem id="p-desc" label="说明" error={errors.description}>
            <Textarea id="p-desc" value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
          </FormItem>
          <DialogFooter>
            <Button type="submit" disabled={save.isPending}>
              {save.isPending && <Loader2 className="animate-spin" />}
              {project ? "保存" : "创建"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function ProjectsPage() {
  const { data: projects, isLoading } = useProjects();
  const [creating, setCreating] = useState(false);
  const newButton = (
    <Button onClick={() => setCreating(true)}>
      <Plus />
      新建项目
    </Button>
  );
  return (
    <>
      <PageHeader title="项目" description="项目把相关的应用放在一起，如 shop。" actions={newButton} />
      {isLoading ? (
        <Skeleton className="h-32 w-full" />
      ) : !projects?.length ? (
        <EmptyState icon={FolderKanban} title="还没有项目" description="先新建一个项目，再在项目里添加应用。" action={newButton} />
      ) : (
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {projects.map((p) => (
            <Link key={p.name} to={`/projects/${p.name}`} className="group">
              <Card className="h-full gap-2 transition-colors group-hover:border-primary/60">
                <CardHeader className="gap-1.5">
                  <CardTitle className="flex items-center gap-2 text-base">
                    <FolderKanban className="size-4 text-primary" />
                    {p.title}
                  </CardTitle>
                  <CardDescription className="font-mono text-xs">{p.name}</CardDescription>
                  {p.description && <CardDescription className="line-clamp-2">{p.description}</CardDescription>}
                  <CardDescription className="pt-2 text-xs">
                    {p.appCount} 个应用 · {timeAgo(p.updatedAt)}更新
                  </CardDescription>
                </CardHeader>
              </Card>
            </Link>
          ))}
        </div>
      )}
      {creating && <ProjectDialog onClose={() => setCreating(false)} />}
    </>
  );
}
