import { Link, Outlet, createBrowserRouter } from "react-router";
import { AppShell } from "@/components/layout/AppShell";
import { EmptyState } from "@/components/layout/Page";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import { EventStreamProvider } from "@/hooks/events";
import { useSession } from "@/hooks/queries";
import { ApiError } from "@/lib/api";
import { LoginPage } from "@/pages/LoginPage";
import { SetupPage } from "@/pages/SetupPage";
import { UsersPage } from "@/pages/UsersPage";
import { NewAppPage } from "@/pages/NewAppPage";
import { OverviewPage } from "@/pages/OverviewPage";
import { AppPage } from "@/pages/AppPage";
import { CredentialsPage } from "@/pages/CredentialsPage";
import { JobPage } from "@/pages/JobPage";
import { JobsPage } from "@/pages/JobsPage";
import { ProjectPage } from "@/pages/ProjectPage";
import { ProjectsPage } from "@/pages/ProjectsPage";
import { SecretsPage } from "@/pages/SecretsPage";
import { ServersPage } from "@/pages/ServersPage";
import { SettingsPage } from "@/pages/SettingsPage";
import { SearchX } from "lucide-react";

/** 先确认登录状态：未登录显示登录页，登录后才建立事件连接、显示页面。 */
function AuthGate() {
  const { data, error, isLoading } = useSession();
  if (isLoading) {
    return (
      <div className="p-8">
        <Skeleton className="h-8 w-48" />
      </div>
    );
  }
  if (error instanceof ApiError && error.unauthorized) return <LoginPage setupRequired={error.code === "setup_required"} />;
  if (error || !data) {
    return (
      <div className="p-8">
        <Alert variant="destructive">
          <AlertDescription>{error?.message ?? "读取登录状态失败"}</AlertDescription>
        </Alert>
      </div>
    );
  }
  return (
    <EventStreamProvider>
      <Outlet />
    </EventStreamProvider>
  );
}

function NotFoundPage() {
  return (
    <EmptyState
      icon={SearchX}
      title="页面不存在"
      description={
        <>
          地址可能写错了，<Link to="/" className="text-primary underline-offset-4 hover:underline">回到总览</Link>。
        </>
      }
    />
  );
}

export const router = createBrowserRouter([
  // 创建第一个管理员：用终端里打印的一次性链接打开，不需要登录
  { path: "setup", element: <SetupPage /> },
  {
    element: <AuthGate />,
    children: [
      {
        element: <AppShell />,
        children: [
          { index: true, element: <OverviewPage /> },
          { path: "projects", element: <ProjectsPage /> },
          { path: "projects/:project", element: <ProjectPage /> },
          { path: "projects/:project/apps/new", element: <NewAppPage /> },
          { path: "projects/:project/apps/:app", element: <AppPage /> },
          { path: "jobs", element: <JobsPage /> },
          { path: "jobs/:id", element: <JobPage /> },
          { path: "servers", element: <ServersPage /> },
          { path: "secrets", element: <SecretsPage /> },
          { path: "credentials", element: <CredentialsPage /> },
          { path: "settings", element: <SettingsPage /> },
          { path: "users", element: <UsersPage /> },
          { path: "*", element: <NotFoundPage /> },
        ],
      },
    ],
  },
]);
