# deployhub 开发规则

通用部署平台，设计和里程碑以 [PLAN.md](PLAN.md) 为准；使用和目录说明见 [README.md](README.md)。

## 通用

- 页面文字、错误信息、代码注释、文档、Git commit 信息都用中文。
- 改完运行 `make test`（go vet、go test、前端类型检查）；涉及页面的改动用 `make build` 后启动 `./bin/deployhub -no-browser -no-keychain -data .deployhub-dev` 在浏览器里验证（开发数据不写钥匙串）。
- 页面用账号密码登录；开发数据目录里还没有账号时，用终端里打印的链接创建管理员。验证登录、账号相关的改动时不要直接用 `.deployhub-dev`（里面可能有真实配置，创建管理员会把它们归到新账号名下），复制一份数据目录或用空目录。
- 应用的仓库一律通过 `core.Service.AppRepo` / `RepoDir` 取（代码来源可能是本机目录，也可能是平台克隆的 git 地址），不要直接读 `repo_path`。连接服务器前用 `ConnectableServer` 取服务器（会把平台保存的私钥准备成文件）。
- 不在生产服务器上做测试。「测试连接」和「检查」只执行只读命令，可以对真实服务器运行；部署和回滚只在测试环境做。
- 测试环境是本机 OrbStack 虚拟机，都带 systemd 和免密 sudo：
  - `dh-test`：Debian 12，没有 rsync、没有 Docker，测 Go 服务和 scp 上传。
  - `dh-docker`：Ubuntu 24.04，有 Docker（含 compose 插件）、Nginx（18081 端口指向 /srv/www/demo-site/current）、rsync，测静态站点和 Docker 镜像。
  - 在 deployhub 里添加服务器：地址 127.0.0.1、端口 32222、用户填虚拟机名、密钥 `~/.orbstack/ssh/id_ed25519`、SSH 选项 `StrictHostKeyChecking=no` 和 `UserKnownHostsFile=/dev/null`。登录后实际用户是本机用户名。
  - 测「git 地址」来源：`dh-docker` 上有 git，在里面建一个裸仓库（`git clone --bare`），地址写 `ssh://dh-docker@127.0.0.1:32222/home/<本机用户名>/<仓库>.git`，私钥用 OrbStack 那把建一个「SSH 私钥」凭据。平台拉取时会往 `~/.ssh/known_hosts` 里记一条 `[127.0.0.1]:32222`，测完删掉。
- Cloudflare Workers 用模拟的 Cloudflare 接口测试（`go run ./internal/cloudflare/cftest/fakecf`，deployhub 用 `CLOUDFLARE_API_BASE_URL` 指向它，见 README「开发」）。真实账号上只部署单独的测试 Worker，不碰线上站点；真实的 API Token 由用户自己在页面上填写。

## 后端（Go）

- 只用标准库 `net/http`，接口前缀 `/api`，JSON；错误统一用 `writeError` 返回 `{"error": {"code", "message", "fields"}}`，校验错误按字段放进 `fields`。
- 新增接口加在 `internal/api/routes.go` 的路由表里，同时定好访问要求：只能在页面上（`page`，默认应该选它）、管理员（`pageAdmin`）、或访问令牌至少要的权限（`read` / `deploy` / `admin`）。会返回密钥明文、配置文件内容这类敏感数据的接口不能对令牌开放；对令牌开放的读接口要用不带敏感内容的版本（如 `GetAppRedacted`）。`TestEveryRouteIsGuarded` 会逐个检查，OpenAPI 文档自动更新。
- 给 AI 的新能力加在 `internal/mcp`：工具只转调业务层和引擎，按权限级别注册，标好只读或有破坏性；发起部署、回滚要带 `ViaToken`（走审批）。
- 数据访问只写在 `internal/store`；SQL 尽量写成 SQLite 和 PostgreSQL 都能用的写法，时间存 Unix 秒。
- 数据按账号隔离：服务器、密钥、项目、凭据、任务表有 `owner_id`，这些表的每条查询都要带 `owner_id = ?`（用 `s.owner`），应用通过项目归属。访问令牌表也是；新增属于账号的表也要带 `owner_id`，并在 `Store.DeleteUser` 里一起删除、在 `internal/api/accounts_test.go` 的隔离测试里覆盖。接口处理函数里用 `s.core(r)`、`s.engine(r)` 取当前账号的业务层和引擎。
- 要重建表的迁移（SQLite 改不了已有约束）在文件第一行写 `-- migrate:no-foreign-keys`，执行时会关闭外键检查，提交前再整体校验。
- 改表结构只新增迁移文件 `internal/store/migrations/<四位序号>_<说明>.sql`，不要修改已有的迁移。
- 不引入 CGO 依赖。

### 新增部署方式

1. 在 `internal/driver/<名称>/` 实现 `driver.Driver`，`Spec()` 里声明参数（类型、默认值、校验、说明、显示条件）。
2. 在 `internal/driver/builtin` 注册。`driver.NewRegistry` 会检查声明（键重复、分组不存在、显示条件无效等），`go test ./internal/driver/...` 能发现声明错误。
3. 前端表单根据声明自动生成，一般不需要改前端。新的参数类型才需要在 `web/src/features/app-form/FieldControl.tsx` 里加控件，并同步 `web/src/lib/types.ts`。

## 前端（web/）

- React + Vite + TypeScript + Tailwind 4 + shadcn/ui；数据请求用 TanStack Query，表单用 react-hook-form + zod。
- Node 和 pnpm 版本由 `mise.toml` 固定，命令用 `mise exec -- pnpm ...`（Makefile 已处理）。
- shadcn 组件放在 `src/components/ui/`，用 `mise exec -- pnpm dlx shadcn@latest add <组件> --yes` 添加。shadcn 命令行解析不了本项目的 `@/lib/utils` 别名，添加后要把组件里的 `import { cn } from "cn"` 改成 `from "@/lib/utils"`，并 `pnpm remove cn`（它会误装一个叫 cn 的包）。
- 颜色用 `src/index.css` 里的主题变量（`bg-background`、`text-muted-foreground` 等），深色模式跟随系统，不写死颜色。
- 页面要在手机宽度下可用，不出现横向滚动。
