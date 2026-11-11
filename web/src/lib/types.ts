// 和 Go 端 internal/driver 的声明一一对应。

export type FieldType = "text" | "textarea" | "number" | "bool" | "select" | "list" | "kv" | "code" | "records";

export interface Option {
  value: string;
  label: string;
  help?: string;
}

export interface Condition {
  key: string;
  in?: string[];
  notEmpty?: boolean;
}

export interface Field {
  key: string;
  label: string;
  section?: string;
  type: FieldType;
  required?: boolean;
  default?: unknown;
  defaultHint?: string;
  placeholder?: string;
  help?: string;
  options?: Option[];
  optionsFrom?: string;
  allowCustom?: boolean;
  pattern?: string;
  patternHint?: string;
  min?: number;
  max?: number;
  language?: string;
  keyLabel?: string;
  valueLabel?: string;
  fields?: Field[];
  showIf?: Condition;
  immutable?: boolean;
  sensitive?: boolean;
}

export interface Section {
  key: string;
  label: string;
  help?: string;
}

export interface StepLabel {
  key: string;
  label: string;
}

export interface DriverSpec {
  name: string;
  title: string;
  summary: string;
  description: string;
  icon: string;
  defaultCheck: string;
  fields: Field[];
  /** 用不到的通用参数，如 Workers 去掉 server */
  omit?: string[];
  /** 部署步骤（预检之后） */
  steps: StepLabel[];
}

export interface CredentialKind {
  name: string;
  title: string;
  help: string;
  fields: Field[];
}

export interface Catalog {
  sections: Section[];
  common: Field[];
  drivers: DriverSpec[];
  credentialKinds: CredentialKind[];
}

/** 登录账号。项目、服务器、密钥、凭据、部署记录都按账号隔离。 */
export interface User {
  username: string;
  /** 管理员可以管理账号 */
  admin: boolean;
  disabled: boolean;
  createdAt: string;
  lastLoginAt?: string;
  /** 账号下的项目数，只在账号列表里有 */
  projectCount: number;
}

export interface Session {
  authenticated: boolean;
  version: string;
  user: User;
}

export type TokenLevel = "read" | "deploy" | "admin";

/** 访问令牌：给 AI 和脚本调用 HTTP API、MCP 用。令牌本身只在创建时返回一次。 */
export interface AccessToken {
  name: string;
  level: TokenLevel;
  /** 令牌开头的几个字符，用来辨认 */
  hint: string;
  createdAt: string;
  lastUsedAt?: string;
}

// ---------- 服务器、密钥、项目、应用（对应 Go 端 internal/model）----------

export interface ServerFacts {
  machine: string;
  arch: string;
  os: string;
  user: string;
  docker: boolean;
  dockerVersion: string;
  compose: boolean;
  rsync: boolean;
  systemd: boolean;
  sudoNoPassword: boolean;
}

export interface Server {
  name: string;
  title: string;
  host: string;
  port: number;
  user: string;
  keyPath: string;
  /** 平台保存的 SSH 私钥（凭据）的名称；不为空时用它登录，不用 keyPath */
  keyCredential: string;
  sshOptions: string[];
  facts?: ServerFacts;
  checkedAt?: string;
  createdAt: string;
  updatedAt: string;
  appCount: number;
}

export interface ServerTestResult {
  ok: boolean;
  facts?: ServerFacts;
  error?: string;
  detail?: string;
  durationMs: number;
}

export interface Secret {
  name: string;
  description: string;
  createdAt: string;
  updatedAt: string;
  usedBy: string[];
}

export interface InfoItem {
  label: string;
  value: string;
}

export interface Credential {
  name: string;
  kind: string;
  title: string;
  /** 不敏感的字段，如账号 ID */
  values: Record<string, string>;
  /** 已经保存了值的敏感字段 */
  secrets: string[];
  info: InfoItem[];
  checkedAt?: string;
  createdAt: string;
  updatedAt: string;
  usedBy: string[];
}

export interface Project {
  name: string;
  title: string;
  description: string;
  createdAt: string;
  updatedAt: string;
  appCount: number;
}

export interface App {
  project: string;
  name: string;
  title: string;
  driver: string;
  server: string;
  params: Record<string, unknown>;
  revision: number;
  createdAt: string;
  updatedAt: string;
}

export interface AppRevision {
  revision: number;
  params: Record<string, unknown>;
  createdAt: string;
}

export interface RepoInfo {
  path: string;
  error?: string;
  branch?: string;
  commit?: string;
  subject?: string;
  committedAt?: string;
  dirty: number;
  remote?: string;
  suggestDriver?: string;
  options: Record<string, Option[]>;
  hints: Record<string, string>;
  notes: string[];
}

export interface PlanCommand {
  where: "local" | "server";
  command: string;
  note?: string;
}

export interface Plan {
  resolved: { key: string; label: string; value: string; source: string }[];
  steps: { name: string; commands: PlanCommand[] }[];
  warnings: string[];
}

export interface Preview {
  version: string;
  plan: Plan;
}

export interface CheckItem {
  group: string;
  name: string;
  status: "ok" | "warn" | "fail" | "skip";
  message: string;
}

export interface CheckResult {
  ok: boolean;
  items: CheckItem[];
  durationMs: number;
}

// ---------- 部署任务（对应 Go 端 internal/model、internal/engine）----------

/** pending_approval：访问令牌（AI、脚本）发起、等页面上批准；rejected：已拒绝 */
export type JobStatus = "pending_approval" | "rejected" | "queued" | "running" | "success" | "failed" | "rolled_back" | "canceled";
export type UnitStatus = "pending" | "running" | "success" | "failed" | "rolled_back" | "canceled" | "skipped";

export interface JobUnit {
  jobId: number;
  seq: number;
  project: string;
  app: string;
  title: string;
  driver: string;
  server: string;
  revision: number;
  target?: string;
  version: string;
  previous: string;
  commit: string;
  status: UnitStatus;
  step: string;
  error: string;
  startedAt?: string;
  finishedAt?: string;
}

export interface Job {
  id: number;
  kind: "deploy" | "rollback";
  status: JobStatus;
  initiator: string;
  error: string;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
  units: JobUnit[];
}

export interface JobEvent {
  id: number;
  jobId: number;
  unit?: number;
  time: string;
  type: "step_start" | "step_done" | "step_fail" | "command" | "output" | "info" | "warn" | "error";
  step?: string;
  message: string;
}

export interface AppState {
  reachable: boolean;
  error?: string;
  current: string;
  releases: string[];
  last?: { time: string; version: string; action: string; result: string; operator: string };
  busy?: number;
}

export interface DeployPlanItem {
  project: string;
  app: string;
  title: string;
  driver: string;
  server: string;
  branch: string;
  commit: string;
  subject: string;
  dirty: number;
  version: string;
  online: string;
  blocked?: string;
  warnings: string[];
}

export interface DeployPlan {
  items: DeployPlanItem[];
  warnings: string[];
  ok: boolean;
}

export type AppStatusState = "up_to_date" | "has_update" | "not_deployed" | "unreachable" | "busy";

export interface AppStatus {
  project: string;
  app: string;
  state: AppStatusState;
  current: string;
  branch: string;
  localCommit: string;
  subject: string;
  dirty: number;
  error?: string;
  busy?: number;
}
