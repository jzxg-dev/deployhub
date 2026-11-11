import { AlertTriangle, GitBranch, GitCommitHorizontal, Loader2, XCircle } from "lucide-react";
import type { RepoInfo } from "@/lib/types";
import { timeAgo } from "@/lib/format";

/** 仓库识别结果：分支、最新提交、未提交改动、提示。 */
export function RepoCard({ repo, loading }: { repo?: RepoInfo; loading?: boolean }) {
  if (loading && !repo) {
    return (
      <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <Loader2 className="size-3.5 animate-spin" />
        正在识别仓库…
      </p>
    );
  }
  if (!repo) return null;
  if (repo.error) {
    return (
      <p className="flex items-center gap-1.5 text-xs text-destructive">
        <XCircle className="size-3.5" />
        {repo.error}
      </p>
    );
  }
  return (
    <div className="space-y-1.5 rounded-md border bg-muted/30 px-3 py-2 text-xs">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className="flex items-center gap-1 font-medium">
          <GitBranch className="size-3.5" />
          {repo.branch}
        </span>
        {repo.commit && (
          <span className="flex min-w-0 items-center gap-1 text-muted-foreground">
            <GitCommitHorizontal className="size-3.5 shrink-0" />
            <code className="font-mono">{repo.commit}</code>
            <span className="truncate">{repo.subject}</span>
            <span className="shrink-0">· {timeAgo(repo.committedAt)}</span>
          </span>
        )}
        {repo.dirty > 0 && <span className="text-warning">{repo.dirty} 个文件未提交</span>}
        {loading && <Loader2 className="size-3.5 animate-spin text-muted-foreground" />}
      </div>
      {repo.notes.map((n) => (
        <p key={n} className="flex gap-1 text-muted-foreground">
          <AlertTriangle className="mt-0.5 size-3 shrink-0 text-warning" />
          {n}
        </p>
      ))}
    </div>
  );
}
