import { RefreshCw } from "lucide-react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { PageHeader } from "@/components/layout/Page";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { AppForm } from "@/features/app-form/AppForm";
import { DriverIcon, DriverPicker } from "@/features/app-form/DriverPicker";
import { useCatalog, useProject } from "@/hooks/queries";

/** 新建应用：第一步选部署方式，第二步填参数。所选部署方式记在地址栏，刷新不丢。 */
export function NewAppPage() {
  const { project: projectName = "" } = useParams();
  const { data: catalog, isLoading, error } = useCatalog();
  const { data: projectData, error: projectError } = useProject(projectName);
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const driver = catalog?.drivers.find((d) => d.name === params.get("driver"));

  if (isLoading) return <Skeleton className="h-64 w-full" />;
  if (error || projectError || !catalog) {
    return (
      <Alert variant="destructive">
        <AlertDescription>{(error ?? projectError)?.message ?? "读取失败"}</AlertDescription>
      </Alert>
    );
  }
  const crumb = (
    <Link to={`/projects/${projectName}`} className="hover:text-foreground hover:underline">
      {projectData?.project.title ?? projectName}
    </Link>
  );

  if (!driver) {
    return (
      <>
        <PageHeader title="新建应用" description={<>{crumb} · 第 1 步，共 2 步：选择部署方式。创建后不能更换，换部署方式请新建应用。</>} />
        <DriverPicker drivers={catalog.drivers} onPick={(name) => setParams({ driver: name })} />
      </>
    );
  }

  return (
    <>
      <PageHeader
        title="新建应用"
        description={
          <span className="inline-flex flex-wrap items-center gap-1.5">
            {crumb} · 第 2 步，共 2 步：填写参数。部署方式：
            <DriverIcon name={driver.icon} className="size-4 text-primary" />
            <span className="font-medium text-foreground">{driver.title}</span>
          </span>
        }
        actions={
          <Button variant="outline" size="sm" onClick={() => setParams({})}>
            <RefreshCw />
            更换部署方式
          </Button>
        }
      />
      {/* key：换部署方式时重建表单 */}
      <AppForm
        key={driver.name}
        catalog={catalog}
        driver={driver}
        project={projectName}
        onSaved={(app) => navigate(`/projects/${projectName}/apps/${app.name}`)}
      />
    </>
  );
}
