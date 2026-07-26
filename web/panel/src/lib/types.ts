// Dashboard API response types — GET /v1/stats?days=N

export interface NodeStat {
  id: string;
  name: string;
  upload_bytes: number;
  download_bytes: number;
}

export interface NodeCombinedStat {
  id: string;
  name: string;
  total_upload_bytes: number;
  total_download_bytes: number;
  period_upload_bytes: number;
  period_download_bytes: number;
  period_total_bytes: number;
}

export interface NodePeriodStat {
  id: string;
  name: string;
  date: string;
  upload_bytes: number;
  download_bytes: number;
  total_bytes: number;
}

export interface DailyTrafficPoint {
  Date: string;
  Label: string;
  UploadBytes: number;
  DownloadBytes: number;
  TotalBytes: number;
  HeightPct: number;
}

export interface TodayUserStat {
  username: string;
  upload_bytes: number;
  download_bytes: number;
  total_bytes: number;
}

export interface TodayNodeStat {
  node_id: string;
  node_name: string;
  upload_bytes: number;
  download_bytes: number;
  total_bytes: number;
}

export interface TopUserStat {
  Username: string;
  UsedBytes: number;
  RawUsedBytes: number;
  TrafficLimit: number;
}

export interface ExpirationDayPoint {
  Date: string;
  Label: string;
  Count: number;
}

export interface UserGrowthPoint {
  Date: string;
  Label: string;
  Count: number;
}

export interface Summary {
  nodes_count: number;
  node_stats: NodeStat[];
  users_count: number;
  online_users_count: number;
  active_users_count: number;
  disabled_users_count: number;
  expired_users_count: number;
  limited_users_count: number;
  expiring_users_count: number;
  total_connections: number;
  total_devices: number;
  total_upload_bytes: number;
  total_download_bytes: number;
  total_used_bytes: number;
  total_billed_upload_bytes: number;
  total_billed_download_bytes: number;
  total_billed_used_bytes: number;
  daily_traffic: DailyTrafficPoint[];
  node_period_stats: NodePeriodStat[];
  node_combined_stats: NodeCombinedStat[];
  today_node_stats: NodePeriodStat[];
  today_user_stats: TodayUserStat[];
  days: number;
  days_options: number[];
  expiration_days: ExpirationDayPoint[];
  user_growth: UserGrowthPoint[];
  top_users: TopUserStat[];
  open_tickets_count: number;
}

// ── User types ───────────────────────────────────────────────────

export type UserStatus = "active" | "disabled" | "limited" | "expired" | "on_hold";
export type ResetStrategy = "no_reset" | "day" | "week" | "month" | "year";

export interface User {
  id: string;
  username: string;
  status: UserStatus;
  effective_status?: UserStatus;
  note: string;
  expire_at?: string;
  data_limit_reset_strategy: ResetStrategy;
  traffic_limit_bytes: number;
  plan_traffic_limit_bytes: number;
  upload_bytes: number;
  download_bytes: number;
  used_bytes: number;
  raw_upload_bytes: number;
  raw_download_bytes: number;
  on_hold_expire_at?: string;
  last_traffic_reset_at?: string;
  online_at?: string;
  connections: number;
  devices: number;
  created_at: string;
  sub_token?: string;
  email?: string;
  uuid?: string;
  secret?: string;
}

export interface UsersResponse {
  users: User[];
  total: number;
}

export interface CreateUserRequest {
  username: string;
  traffic_limit_bytes?: number;
  expire_at?: string;
  data_limit_reset_strategy?: ResetStrategy;
}

export interface UpdateUserRequest {
  status?: UserStatus;
  expire_at?: string;
  data_limit_reset_strategy?: ResetStrategy;
  traffic_limit_bytes?: number;
  note?: string;
}

// ── Node types ───────────────────────────────────────────────────

export interface Node {
  id: string;
  name: string;
  base_url: string;
  upload_bytes: number;
  download_bytes: number;
  acme_email: string;
  panel_domain: string;
  extra_proxies: string;
  https_port: number;
  expire_at: string | null;
  panel_url: string;
  remark: string;
  ip_override: string;
  disabled: boolean;
  is_landing: boolean;
  online?: boolean;
}

export interface NodesResponse {
  nodes: Node[];
}

export interface CreateNodeRequest {
  name: string;
  base_url?: string;
  expire_at?: string | null;
  panel_url?: string;
  remark?: string;
  ip_override?: string;
  disabled?: boolean;
  is_landing?: boolean;
}

// ── Inbound types ────────────────────────────────────────────────

export type InboundProtocol = "vless" | "trojan" | "shadowsocks" | "anytls" | "hy2";

export interface Inbound {
  id: string;
  node_id: string;
  protocol: InboundProtocol;
  tag: string;
  port: number;
  outbound_id: string;
  method: string;
  password: string;
  security: string;
  reality_private_key: string;
  reality_public_key: string;
  reality_handshake_addr: string;
  reality_short_id: string;
  traffic_rate: number;
  domain?: string;
  // Extra 协议私有杂项配置（JSON 字符串）；hy2 约定：{"sni":"...","masquerade_url":"...","udp_idle_timeout_sec":60}
  extra?: string;
}

export interface InboundsResponse {
  inbounds: Inbound[];
  user_counts?: Record<string, number>;
}

export interface CreateInboundRequest {
  node_id: string;
  protocol: InboundProtocol;
  tag?: string;
  port: number;
  outbound_id?: string;
  method?: string;
  password?: string;
  security?: string;
  reality_private_key?: string;
  reality_public_key?: string;
  reality_handshake_addr?: string;
  reality_short_id?: string;
  traffic_rate?: number;
  domain?: string;
  extra?: string;
}

// ── Outbound types ───────────────────────────────────────────────

export type OutboundProtocol = "ss" | "vless";

export interface Outbound {
  id: string;
  name: string;
  protocol: OutboundProtocol;
  server: string;
  username: string;
  password: string;
  method: string;
  uuid: string;
  sni: string;
  public_key: string;
  short_id: string;
  fingerprint: string;
}

export interface OutboundsResponse {
  outbounds: Outbound[];
}

export interface SSOutboundOption {
  id: string;
  label: string;
}

export interface SSOutboundOptionsResponse {
  options: SSOutboundOption[];
}

export interface CreateOutboundRequest {
  name: string;
  protocol?: OutboundProtocol;
  server: string;
  username?: string;
  password?: string;
  method?: string;
  uuid?: string;
  sni?: string;
  public_key?: string;
  short_id?: string;
  fingerprint?: string;
  flow?: string;
}

// ── Host types ───────────────────────────────────────────────────

export interface Host {
  id: string;
  inbound_id: string;
  remark: string;
  address: string;
  port: number;
  sni: string;
  host: string;
  path: string;
  security: string;
  alpn: string;
  fingerprint: string;
  allow_insecure: boolean;
  mux_enable: boolean;
  reality_public_key: string;
  reality_short_id: string;
  reality_spider_x: string;
  country?: string;      // 国旗 emoji，如 🇭🇰
  region?: string;       // 地区，如 香港
  network?: string;      // 线路，如 IEPL
  entry?: string;        // 入口城市，如 深圳
  tags?: string;         // 业务标签，如 NF·GPT
  relay_node_id?: string; // 前置节点 ID，设置后该节点 NodeGate 自动配置端口转发
  https_port?: number; // 落地节点 NodeGate HTTPS 端口（0 = 跟随节点配置，最终 fallback 443）
}

export interface HostsResponse {
  hosts: Host[];
}

// ── Route Rule types ─────────────────────────────────────────────

export type RouteRuleType = "domain_suffix" | "domain_keyword" | "domain" | "ip_cidr";

export interface RouteRule {
  id: string;
  name: string;
  rule_type: RouteRuleType;
  patterns: string;
  outbound_id: string;
  priority: number;
  node_ids?: string;
  inbound_ids?: string;
}

export interface RouteRulesResponse {
  rules: RouteRule[];
}

// ── Plan types ───────────────────────────────────────────────

export type PlanType = "subscription" | "one_time";

export interface Plan {
  id: string;
  name: string;
  description: string;
  type: PlanType;
  price_cents: number;
  currency: string;
  stripe_price_id: string;
  traffic_limit: number;
  duration_days: number;
  data_limit_reset_strategy: ResetStrategy;
  user_group_ids: string;
  sort_order: number;
  enabled: boolean;
  mode: "live" | "test";
  stock_limit: number; // -1 = 无限制
  stock_sold: number;
  created_at: string;
}

export interface PlansResponse {
  plans: Plan[];
}

// ── Sub Access Log types ─────────────────────────────────────────

export interface SubAccessLog {
  id: number;
  user_id: string;
  ip: string;
  user_agent: string;
  accessed_at: string;
}

export interface SubAccessLogsResponse {
  logs: SubAccessLog[];
}

// ── CF Domain types ─────────────────────────────────────────────

export interface CFDomain {
  id: string;
  cf_token: string;      // 脱敏显示，如 "sk-***abc"
  zone_id: string;
  zone_name: string;     // example.com
  remark: string;
}

export interface CFDomainsResponse {
  domains: CFDomain[];
}

export interface CFZone {
  id: string;
  name: string;          // example.com
  status: string;        // active, pending
}

export interface CFZonesResponse {
  zones: CFZone[];
}

export interface CFDNSRecord {
  id: string;
  type: string;          // A, AAAA, CNAME
  name: string;          // sub.example.com
  content: string;       // IP 或目标
  ttl: number;           // 1 = auto
  proxied: boolean;      // CF 代理
  comment?: string;      // CF 控制台备注
}

export interface CFDNSRecordsResponse {
  records: CFDNSRecord[];
}

export interface CreateCFDomainRequest {
  cf_token: string;
  zone_id: string;
  zone_name: string;
  remark: string;
}

export interface CreateCFDNSRecordRequest {
  type: string;
  name: string;
  content: string;
  ttl: number;
  proxied: boolean;
  comment?: string;
}

export interface UpdateCFDNSRecordRequest {
  type: string;
  name: string;
  content: string;
  ttl: number;
  proxied: boolean;
}


// ── Node Domain types ────────────────────────────────────────────

export interface NodeDomain {
  id: string;
  node_id: string;       // 为空表示未分配节点
  cf_domain_id: string;
  fqdn: string;          // 完整域名，如 jp.example.com
  record_type: string;   // A、AAAA、CNAME
  content: string;       // IP 或 CNAME 目标
  proxied: boolean;
  synced_at: string;
}

export interface NodeDomainsResponse {
  node_domains: NodeDomain[];
}

export interface SyncNodeDomainsRequest {
  cf_domain_id: string;
  node_id?: string;      // 可选，留空则按 IP 自动匹配
}

export interface SyncNodeDomainsResponse {
  synced: number;
  node_domains: NodeDomain[];
}

// ── UserGroup types ──────────────────────────────────────────────

export interface UserGroup {
  id: string;
  name: string;
  remark: string;
  inbound_ids: string; // 逗号分隔
  member_count?: number;
}

export interface UserGroupsResponse { user_groups: UserGroup[] }
export interface UserGroupMember { user_id: string; username: string }
export interface UserGroupMembersResponse { members: UserGroupMember[] }
