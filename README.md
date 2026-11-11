# deployhub

通用部署平台：在网页上创建项目和应用，选择部署方式、填写参数，之后在页面上点「部署」。Go 编写，单个可执行文件，直接使用运行它的那台机器上的 git、SSH 密钥和构建环境；可以只在本机用，也可以放到一台机器上给多人用账号密码登录，每个账号只能看到自己的项目。设计和里程碑见 [PLAN.md](PLAN.md)。

**当前进度：M3 功能完成，M5 的 Cloudflare Workers 提前完成**——四种部署方式（Go 服务、静态站点、Docker 镜像、Cloudflare Workers）都可以在页面上部署（确认 → 实时日志 → 健康检查失败自动回滚）、取消、手动回滚；Cloudflare 账号在「凭据」里管理；项目页可以勾选或按分组批量部署；总览页显示每个应用「有更新 / 未部署 / 已是最新」；可以导入旧工具的部署历史、导出导入配置；账号密码登录，数据按账号隔离，可以监听非本机地址；代码可以来自 git 地址（平台自己克隆和拉取），SSH 私钥可以由平台加密保存。**M4 功能完成**（MCP 用官方 SDK 的客户端和 curl 验证过，还没在 Claude Code 里实际用过）：访问令牌（只读 / 可部署 / 管理）、AI 发起的部署要在页面上审批、MCP 服务、OpenAPI 文档、幂等键。Workers 只用模拟的 Cloudflare 接口验证过，还没在真实账号上发布过。

## 使用

需要 Go（版本见 `go.mod`）和 [mise](https://mise.jdx.dev/)（固定 Node、pnpm 版本，见 `mise.toml`）。

```bash
make build        # 构建前端，再编译 Go 程序到 bin/deployhub（前端嵌入可执行文件）
./bin/deployhub   # 启动并打开浏览器
```

- 页面用账号密码登录。第一次启动时还没有账号，终端会打印一个创建管理员的链接（10 分钟内有效，只能用一次）；已经在运行时再执行 `deployhub` 会打印新的链接。登录后 30 天内不用再登录。
- 默认监听 `127.0.0.1:7788`。给其他机器访问见下面的「给多人使用」。
- 数据目录默认 `~/.deployhub/`（SQLite 数据库 `deployhub.db`），可以用 `-data` 或环境变量 `DEPLOYHUB_HOME` 指定。
- 密钥的值和应用配置文件的内容加密保存，主密钥放在 macOS 钥匙串；用 `-no-keychain` 启动时放在数据目录的 `master.key`（权限 600）。主密钥丢了，加密的数据就无法恢复。
- 忘记密码：普通账号找管理员在「账号」页重置；管理员在运行 deployhub 的机器上执行 `deployhub -reset-password <用户名>`，会打印一个新的随机密码（要带上和启动时一样的 `-data`、`-no-keychain`）。
- `deployhub -h` 查看全部选项。日常操作都在页面上完成，没有子命令。

## 备份和恢复

全部数据就是数据目录里的 `deployhub.db` 和主密钥。主密钥丢了，库里加密的密钥、凭据、配置文件内容都无法恢复，所以两样要一起备份。

- **自动备份**：运行期间每天备份一次（距离上一次超过 24 小时就做，启动时也检查），放在数据目录的 `backups/` 下：`deployhub-<日期>-<时间>.db` 是数据库的一致快照，`master.key` 是主密钥（即使主密钥平时在钥匙串里，这里也会有一份）。保留最近 14 份，用 `-backup-keep` 改，`0` 表示不自动备份。
- **升级前自动备份**：新版本要升级数据库的表结构时，先留一份 `deployhub-before-upgrade-v<旧版本号>-….db`，备份失败就不升级。升级不能回退，这份备份不会被自动清理。
- **手动备份**：`deployhub -backup` 立刻备份一次然后退出，服务正在运行时也可以用（要带上和启动时一样的 `-data`、`-no-keychain`）。
- **换个地方放**：默认的备份目录和数据在同一块磁盘上，防得了误操作和数据库损坏，防不了磁盘损坏。用 `-backup-dir`（环境变量 `DEPLOYHUB_BACKUP_DIR`）指到别的磁盘或同步盘。备份目录里有主密钥，谁拿到这个目录就能解开全部密钥，放到云盘前想清楚。

恢复：

1. 停掉 deployhub。
2. 把要恢复的那份 `deployhub-….db` 复制成数据目录下的 `deployhub.db`，并删掉旁边的 `deployhub.db-wal` 和 `deployhub.db-shm`（如果有）。
3. 把备份目录里的 `master.key` 复制到数据目录。
4. 启动。恢复到另一台机器或另一个目录时，用 `-no-keychain` 启动（钥匙串里的主密钥和原来的目录路径绑定，新位置取不到）。

备份不包括构建缓存和平台克隆的仓库（`accounts/` 下），它们会在下次部署时重新生成。

## 给多人使用

管理员在「账号」页新建账号、重置密码、停用、删除。每个账号有自己的项目、应用、服务器、密钥、凭据和部署记录，互相看不到，名称可以相同；管理员只多了管理账号的权限，同样看不到别人的数据。删除账号会连同它的全部数据一起删除，只是不想让对方继续登录时用「停用」。

从没有账号的旧版本升级时，原有的数据全部归升级后创建的第一个管理员。

放到一台机器上给其他机器访问：

```bash
# 推荐：只监听本机，前面放带证书的反向代理（Caddy、Nginx）
./bin/deployhub -no-browser -no-keychain -public-url https://deploy.example.com

# 内网直接访问（没有 HTTPS，密码明文传输，只适合可信的内网）
./bin/deployhub -no-browser -no-keychain -addr 0.0.0.0:7788 -public-url http://192.168.1.5:7788
```

- `-public-url`（环境变量 `DEPLOYHUB_PUBLIC_URL`）是浏览器里访问平台用的地址。平台只接受 `Host` 和 `Origin` 是这个地址的请求；监听的不是本机地址时必须填。地址是 `https://` 时，会话 Cookie 只通过 HTTPS 发送。
- 反向代理要把原来的 `Host` 传给 deployhub（Caddy 默认会；Nginx 要加 `proxy_set_header Host $host;`），实时日志用的是 SSE，Nginx 要加 `proxy_buffering off;`。
- Linux 上没有钥匙串，用 `-no-keychain`，主密钥放在数据目录的 `master.key`。
- 那台机器上要装好部署用到的工具：`git`、`ssh`、`rsync`，以及各应用构建需要的 `go`、`docker`、`mise`、`wrangler` 等。
- 代码和密钥不用事先放到那台机器上：
  - **SSH 私钥**：在「凭据」里添加「SSH 私钥」，粘贴私钥内容（不能带口令），加密保存。验证后显示对应的公钥，把公钥加到服务器的 `~/.ssh/authorized_keys` 或 git 仓库的部署密钥里。添加服务器时在「平台保存的 SSH 私钥」里选它。
  - **代码**：应用的「代码来源」选「git 地址」，填 SSH 地址（配合上面的私钥）或 HTTPS 地址。私有仓库用 HTTPS 时把令牌放进「密钥与变量」，地址写成 `https://x-access-token:${GIT_TOKEN}@github.com/<组织>/<仓库>.git`。平台每个应用克隆一份，检查、打开部署确认、刷新总览状态和部署时拉取部署分支的最新提交。
  - 「本机目录」和「密钥文件」两种原来的方式仍然可以用，指的是运行 deployhub 的那台机器上的路径。
- **账号隔离的是数据，不是执行环境。** 构建命令由用户填写，在运行 deployhub 的机器上以同一个系统用户执行，所以有账号的人实际上能在那台机器上执行任意命令，也就有办法读到别人的仓库和密钥。只给信得过的人开账号。

## 让 AI 和脚本部署

在「设置 → 访问令牌」里创建令牌，选权限：

| 权限 | 能做什么 |
|---|---|
| 只读 | 查看应用、线上状态、部署记录和日志、部署计划；校验参数；部署前检查 |
| 可部署 | 再加上发起部署、回滚、取消任务 |
| 管理 | 再加上新建和修改项目、应用、服务器 |

令牌创建后还能在「设置 → 访问令牌」里再次查看、复制（库里存的是用主密钥加密后的令牌；升级到这个版本之前创建的令牌只有哈希，查看不了，可以吊销后重建）；只能访问创建它的账号的数据。不管什么权限，令牌都读不到密钥的值和配置文件内容；密钥、凭据、令牌、账号的管理，删除，导入导出，任务审批，都只能登录页面后做。

**Claude Code（MCP）**：创建令牌后页面上会给出接入命令，在终端执行一次：

```bash
claude mcp add --transport http deployhub http://127.0.0.1:7788/mcp --header "Authorization: Bearer <令牌>"
```

之后直接对 AI 说「把 shop-api 部署一下，失败就看日志找原因」。AI 能用的工具随令牌权限变化：`list_apps`、`get_app`、`list_jobs`、`get_job`、`get_job_logs`、`plan_deploy`、`wait_job`、`list_drivers`、`detect_repo`、`validate_app`（只读），`deploy`、`rollback`、`cancel_job`（可部署），`create_app`、`update_app`（管理）。

**脚本（HTTP 接口）**：请求头带 `Authorization: Bearer <令牌>`。全部接口和每个接口需要的权限见 `GET /api/openapi.json`（「设置」页有入口）。

```bash
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:7788/api/apps
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Idempotency-Key: release-42" \
     -d '{"apps":["shop/shop-api"]}' http://127.0.0.1:7788/api/deploys
curl -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:7788/api/jobs/12/logs?tail=50"
```

- **审批**：用令牌发起的部署和回滚，在打开了「AI 部署需要审批」的应用上（默认打开）先进入「待审批」，不执行；每个页面顶部会提示，在任务页点「批准并执行」才开始（执行前重新预检），点「拒绝」则不执行。同一个应用同时只能有一个待审批的任务。信得过的应用可以在配置里关掉这一项。
- **幂等键**：部署、回滚带上 `Idempotency-Key`（MCP 工具是 `idempotency_key` 参数），同一个键重复提交时返回原来的任务，网络重试不会重复部署。
- 部署记录里的「发起方」会显示是哪个令牌发起的。

## 开发

前端和 Go 服务分开跑，两个终端：

```bash
make dev-api      # Go 服务，数据放在 .deployhub-dev/，放行前端开发服务器的地址
```

```bash
make dev-web      # 前端开发服务器 127.0.0.1:5173，接口代理到 7788
```

打开 `make dev-api` 打印的地址（指向 5173），用账号密码登录；还没有账号时打印的是创建管理员的链接。改 Go 代码后重启 `make dev-api`，改前端代码自动热更新。

```bash
make test         # go vet、go test、前端类型检查
```

不连真实的 Cloudflare 验证 Workers 部署：

```bash
go run ./internal/cloudflare/cftest/fakecf     # 模拟的 Cloudflare 接口，127.0.0.1:8787，打印测试用的账号 ID 和 Token
```

```bash
CLOUDFLARE_API_BASE_URL=http://127.0.0.1:8787/client/v4 ./bin/deployhub -no-browser -no-keychain -data .deployhub-dev
```

测试仓库放 `wrangler.jsonc` 和 cftest 里的假 wrangler（`fake-wrangler.sh`），构建命令填 `cftest.InstallCommand`，健康检查地址填 `http://127.0.0.1:8787/__worker/<Worker 名称>`；仓库里有 `BROKEN` 文件时发布的版本返回 500，用来测自动回滚。

## 目录

```
cmd/deployhub/          入口：参数、启动服务、打开浏览器
internal/
  api/                  HTTP 接口（路由表 routes.go，OpenAPI 文档从它生成）、SSE 事件流、托管前端页面
  mcp/                  MCP 服务（Streamable HTTP）：工具转调业务层和部署引擎
  backup/               备份：数据库快照和主密钥，每天自动备份、保留最近若干份
  auth/                 会话 Cookie、访问令牌的解析、密码哈希、登录失败次数限制、创建管理员的一次性链接、Host 和 Origin 校验
  buildinfo/            编译时注入的版本号
  cloudflare/           Cloudflare 接口客户端：验证 Token、Workers 的版本和部署
    cftest/             模拟的 Cloudflare 接口和假 wrangler（测试、本机验证用；fakecf/ 可以单独运行）
  core/                 业务层：服务器、密钥、凭据、项目、应用的增删改查和校验，部署前检查、命令预览；账号管理；克隆和拉取 git 地址的仓库
  credential/           凭据种类（Cloudflare 账号、SSH 私钥）：字段声明和验证
  detect/               识别本机仓库（编译入口、包管理器、Dockerfile、wrangler 配置）和服务器环境
  driver/               部署方式：参数声明、参数校验（Clean）、部署计划（Plan、Checks）、执行（Executor）
    gobinary/ static/ image/ workers/   四种内置部署方式
    builtin/            注册内置部署方式
  engine/               部署引擎：任务、预检和同步代码、逐步执行、自动回滚、取消、线上状态；待审批和批准、幂等键、日志分页
  events/               进程内事件分发（推给 SSE）
  model/                各层共用的数据结构
  remote/               调系统 ssh、scp、rsync：执行命令（实时输出）、上传文件，连接复用
  shell/                执行本地命令，按行实时输出，可取消
  store/                SQLite：打开、迁移（migrations/*.sql）、读写；数据按账号隔离（For）
  tenant/               每个账号一套业务层、部署引擎和事件通道
  vault/                AES-GCM 加密，主密钥在钥匙串或 master.key
web/                    前端：React + Vite + TypeScript + Tailwind + shadcn/ui
  src/features/app-form/  按参数声明自动生成的应用表单
  dist/                 构建产物，由 web/embed.go 嵌入可执行文件
```

## 部署是怎么执行的

点「部署」后先显示确认（服务器、预计版本、线上版本、提醒），确认后创建任务，在服务进程里执行，关掉浏览器不影响：

1. **预检**：服务器连得上；仓库存在、分支正确、未提交改动按应用设置处理；按同步方式更新代码（以本地为准：只有本地单纯落后远程时才快进）；读取线上版本。代码来源是 git 地址时，这一步改为拉取部署分支并把平台克隆的工作区切到它的最新提交（工作区是平台的，不保留本地改动，没被跟踪的构建产物留着当缓存）。
2. **准备服务器** → **构建** → **上传**：这三步可以取消。
3. **切换** → **健康检查**：从切换开始不再响应取消。健康检查失败时自动切回上一个版本并再次检查。
4. 在服务器的 `deploy.log` 追加记录（格式和旧工具一致），清理超出保留数的旧版本。

Cloudflare Workers 没有服务器，步骤是预检 → 检查账号（验证 Token、读取线上版本）→ 构建（仓库副本里）→ 发布（`wrangler deploy`）→ 健康检查（本机）。回滚和自动回滚通过 Cloudflare 接口把旧版本设为 100% 流量，不重新构建。

同一个应用同时只能有一个任务。服务在任务执行中退出时，下次启动会把这些任务标记为中断。

## 安全

- 默认只监听本机地址；所有请求校验 `Host`（防 DNS 重绑定），修改类请求还校验 `Origin`（防跨站请求），只认监听的本机地址和 `-public-url`。
- 密码用 PBKDF2-HMAC-SHA256（60 万次迭代，加盐）保存。同一个用户名 5 分钟内输错 5 次后要等一会儿才能再试；登录失败不区分「用户名不存在」和「密码不对」。
- 会话 Cookie 为 `HttpOnly`、`SameSite=Strict`，值是随机令牌，库里只存它的 SHA-256。改密码、被重置密码、停用、删除账号时，对应的会话立即失效。
- 访问令牌以 `dh_` 开头，认证时按 SHA-256 查找；令牌本身和密钥一样用主密钥加密保存，只有登录页面后才能再次查看，令牌自己读不到。吊销或账号停用后立刻失效。带令牌的请求只按令牌认证、不认 Cookie，所以不校验 `Origin`（别的网站拿不到令牌，也没法跨站加这个请求头）；`Host` 照样校验。每个接口的访问要求集中写在路由表里，默认是「只能在页面上」，有测试逐个检查。
- 每个账号只能读写自己的数据：查询统一带归属条件，实时事件、任务锁、构建目录也各用各的。访问别人的项目、任务、密钥一律返回「不存在」。
- 创建管理员的一次性令牌库里只存 SHA-256，有账号后就不能再用；响应头带 `Referrer-Policy: no-referrer`，避免链接通过 Referer 泄露。
- 密钥明文只能用 `POST /api/secrets/{名称}/reveal` 读取（修改类请求才校验 Origin）；列表和应用列表里不返回敏感内容。
- 凭据的敏感字段（如 Cloudflare API Token）加密保存，接口从不返回；部署时只通过环境变量传给 wrangler，日志里显示为 `***`。
- 服务器的 SSH 选项不允许设置会在本机执行命令的 ProxyCommand、LocalCommand 等。
- 平台保存的 SSH 私钥在库里是加密的，保存前用 `ssh-keygen` 验证格式。系统的 ssh 和 git 只能从文件读私钥，所以用到时会写到 `<数据目录>/accounts/<账号编号>/keys/`（目录 700、文件 600），私钥改了或凭据删了就删掉对应的文件；还有服务器或应用在用的私钥不能删除。
- git 地址只接受 `https://`、`http://`、`ssh://` 和 `用户@主机:路径`；地址通过环境变量传给 git，不写进 `.git/config`，不出现在命令行里；日志和错误信息里的令牌显示为 `***`。第一次连接 git 服务器时自动记下它的主机指纹（`StrictHostKeyChecking=accept-new`），之后指纹变了会拒绝。

## 许可

[MIT](LICENSE)
