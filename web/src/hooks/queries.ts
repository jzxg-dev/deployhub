import { useQuery } from "@tanstack/react-query";
import { api, http } from "@/lib/api";
import type { AccessToken, App, AppRevision, AppState, AppStatus, Catalog, Credential, Job, Project, Secret, Server, Session, User } from "@/lib/types";

export function useSession() {
  return useQuery({
    queryKey: ["session"],
    queryFn: () => api<Session>("/api/session"),
    retry: false,
    staleTime: 60_000,
  });
}

/** 所有账号，只有管理员能读。 */
export function useUsers(enabled = true) {
  return useQuery({ queryKey: ["users"], queryFn: () => http.get<User[]>("/api/users"), enabled });
}

export function useTokens() {
  return useQuery({ queryKey: ["tokens"], queryFn: () => http.get<AccessToken[]>("/api/tokens") });
}

export function useCatalog() {
  return useQuery({
    queryKey: ["drivers"],
    queryFn: () => api<Catalog>("/api/drivers"),
    staleTime: Infinity,
  });
}

export function useServers() {
  return useQuery({ queryKey: ["servers"], queryFn: () => http.get<Server[]>("/api/servers") });
}

export function useSecrets() {
  return useQuery({ queryKey: ["secrets"], queryFn: () => http.get<Secret[]>("/api/secrets") });
}

export function useCredentials() {
  return useQuery({ queryKey: ["credentials"], queryFn: () => http.get<Credential[]>("/api/credentials") });
}

export function useProjects() {
  return useQuery({ queryKey: ["projects"], queryFn: () => http.get<Project[]>("/api/projects") });
}

export function useProject(name: string) {
  return useQuery({
    queryKey: ["projects", name],
    queryFn: () => http.get<{ project: Project; apps: App[] }>(`/api/projects/${name}`),
  });
}

export function useAllApps() {
  return useQuery({ queryKey: ["apps"], queryFn: () => http.get<App[]>("/api/apps") });
}

export function useApp(project: string, app: string) {
  return useQuery({
    queryKey: ["projects", project, "apps", app],
    queryFn: () => http.get<App>(`/api/projects/${project}/apps/${app}`),
  });
}

export function useRevisions(project: string, app: string) {
  return useQuery({
    queryKey: ["projects", project, "apps", app, "revisions"],
    queryFn: () => http.get<AppRevision[]>(`/api/projects/${project}/apps/${app}/revisions`),
  });
}

export function useJob(id: number) {
  return useQuery({ queryKey: ["jobs", id], queryFn: () => http.get<Job>(`/api/jobs/${id}`) });
}

export function useJobs(filter: { project?: string; app?: string; status?: string } = {}) {
  const qs = new URLSearchParams(Object.entries(filter).filter(([, v]) => v) as [string, string][]).toString();
  return useQuery({ queryKey: ["jobs", "list", filter], queryFn: () => http.get<Job[]>(`/api/jobs${qs ? "?" + qs : ""}`) });
}

export function useAppState(project: string, app: string) {
  return useQuery({
    queryKey: ["state", project, app],
    queryFn: () => http.get<AppState>(`/api/projects/${project}/apps/${app}/state`),
    staleTime: 30_000,
  });
}

/** 应用的部署状态（要连每台服务器，较慢）。project 为空时是所有应用。 */
export function useStatus(project?: string) {
  return useQuery({
    queryKey: ["status", project ?? ""],
    queryFn: () => http.get<AppStatus[]>(`/api/status${project ? "?project=" + project : ""}`),
    staleTime: 30_000,
  });
}
