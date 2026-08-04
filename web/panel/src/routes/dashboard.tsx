import { useEffect, useState, useCallback, useMemo } from "react";
import {
  AreaChart,
  Area,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip as RechartsTooltip,
  ResponsiveContainer,
  Legend,
} from "recharts";
import {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
  Button,
} from "@/components/ui";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "@/components/ui/dialog";
import { api } from "@/lib/api";
import { useAuthErrorHandler } from "@/hooks/useAuthErrorHandler";
import { formatBytes, formatBytesCompact } from "@/lib/format";
import type { Summary, NodePeriodStat, TodayUserStat, TodayNodeStat } from "@/lib/types";

// ── Color constants ──────────────────────────────────────────────
const BLUE = "#3b82f6";
const CYAN = "#06b6d4";
const PURPLE = "#8b5cf6";
const AMBER = "#f59e0b";
const EMERALD = "#10b981";
const GRID_COLOR = "#333";
const TICK_COLOR = "#a1a1aa";

// ── Custom tooltip ───────────────────────────────────────────────
function TrafficTooltip({ active, payload, label }: any) {
  if (!active || !payload?.length) return null;
  return (
    <div className="rounded-lg border border-[hsl(var(--border))] bg-[hsl(var(--card))] px-3 py-2 text-xs shadow-xl">
      <p className="mb-1 font-medium text-[hsl(var(--foreground))]">{label}</p>
      {payload.map((entry: any) => (
        <p key={entry.dataKey} style={{ color: entry.color }}>
          {entry.name}: {formatBytes(entry.value)}
        </p>
      ))}
    </div>
  );
}

function BytesTooltip({ active, payload, label }: any) {
  if (!active || !payload?.length) return null;
  return (
    <div className="rounded-lg border border-[hsl(var(--border))] bg-[hsl(var(--card))] px-3 py-2 text-xs shadow-xl">
      <p className="mb-1 font-medium text-[hsl(var(--foreground))]">{label}</p>
      {payload.map((entry: any) => (
        <p key={entry.dataKey} style={{ color: entry.color }}>
          {entry.name}: {formatBytes(entry.value)}
        </p>
      ))}
    </div>
  );
}

// ── Skeleton card ────────────────────────────────────────────────
function SkeletonCard() {
  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="h-4 w-20 animate-pulse rounded bg-[hsl(var(--muted))]" />
      </CardHeader>
      <CardContent>
        <div className="h-8 w-24 animate-pulse rounded bg-[hsl(var(--muted))]" />
        <div className="mt-2 h-3 w-32 animate-pulse rounded bg-[hsl(var(--muted))]" />
      </CardContent>
    </Card>
  );
}

function SkeletonChart({ className }: { className?: string }) {
  return (
    <Card className={className}>
      <CardHeader>
        <div className="h-4 w-32 animate-pulse rounded bg-[hsl(var(--muted))]" />
      </CardHeader>
      <CardContent>
        <div className="h-72 w-full animate-pulse rounded-lg bg-[hsl(var(--muted))]" />
      </CardContent>
    </Card>
  );
}

// ── Node status type ─────────────────────────────────────────────
type NodeStatusValue = "online" | "offline" | "loading";

// ── Main page ────────────────────────────────────────────────────
export default function DashboardPage() {
  const handleAuthError = useAuthErrorHandler();
  const [data, setData] = useState<Summary | null>(null);
  const [days, setDays] = useState(7);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [nodeStatuses, setNodeStatuses] = useState<Map<string, NodeStatusValue>>(new Map());
  const [trafficMode, setTrafficMode] = useState<"actual" | "billed">("actual");

  const fetchData = useCallback((d: number) => {
    setLoading(true);
    setError(null);
    api
      .get<Summary>(`/stats?days=${d}`)
      .then(setData)
      .catch((err) => {
        if (handleAuthError(err)) return;
        setError(err instanceof Error ? err.message : "加载失败");
      })
      .finally(() => setLoading(false));
  }, [handleAuthError]);

  useEffect(() => {
    fetchData(days);
  }, [days, fetchData]);

  // ── Check node runtime statuses ─────────────────────────────
  useEffect(() => {
    const nodes = data?.node_stats;
    if (!nodes || nodes.length === 0) return;

    const initial = new Map<string, NodeStatusValue>();
    nodes.forEach((n) => initial.set(n.id, "loading"));
    setNodeStatuses(new Map(initial));

    nodes.forEach((node) => {
      api
        .get<any>(`/nodes/${node.id}/runtime/status`)
        .then(() => {
          setNodeStatuses((prev) => {
            const next = new Map(prev);
            next.set(node.id, "online");
            return next;
          });
        })
        .catch(() => {
          setNodeStatuses((prev) => {
            const next = new Map(prev);
            next.set(node.id, "offline");
            return next;
          });
        });
    });
  }, [data?.node_stats]);

  const daysOptions = data?.days_options ?? [7, 14, 30];

  // Prepare top users in descending order for horizontal bar
  const topUsersData = useMemo(() => {
    if (!data?.top_users) return [];
    return [...data.top_users].reverse();
  }, [data?.top_users]);

  // ── Error state ──────────────────────────────────────────────
  if (error && !data) {
    return (
      <div className="flex h-full items-center justify-center p-8">
        <Card className="max-w-md w-full">
          <CardContent className="pt-6 text-center">
            <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-red-500/10 text-red-500">
              <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="h-6 w-6">
                <circle cx="12" cy="12" r="10" />
                <line x1="12" y1="8" x2="12" y2="12" />
                <line x1="12" y1="16" x2="12.01" y2="16" />
              </svg>
            </div>
            <p className="mb-1 font-semibold text-[hsl(var(--foreground))]">加载失败</p>
            <p className="mb-4 text-sm text-[hsl(var(--muted-foreground))]">{error}</p>
            <Button onClick={() => fetchData(days)} size="sm">
              重试
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  // ── Loading skeleton ─────────────────────────────────────────
  if (loading && !data) {
    return (
      <div className="space-y-6 p-4 sm:p-6 lg:p-8">
        <div className="flex items-center justify-between">
          <div className="h-8 w-32 animate-pulse rounded bg-[hsl(var(--muted))]" />
          <div className="h-9 w-60 animate-pulse rounded-lg bg-[hsl(var(--muted))]" />
        </div>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-6">
          {Array.from({ length: 6 }).map((_, i) => (
            <SkeletonCard key={i} />
          ))}
        </div>
        <SkeletonChart />
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <SkeletonChart />
          <SkeletonChart />
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6 p-4 sm:p-6 lg:p-8">
      {/* Header + time range */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <h1 className="text-2xl font-bold text-[hsl(var(--foreground))]">
          仪表盘
        </h1>

        <div className="flex flex-wrap items-center gap-1 rounded-lg bg-[hsl(var(--muted))] p-1">
          {daysOptions.map((opt) => (
            <Button
              key={opt}
              variant={days === opt ? "default" : "ghost"}
              size="sm"
              onClick={() => setDays(opt)}
              className={
                days === opt
                  ? ""
                  : "text-[hsl(var(--muted-foreground))] hover:text-[hsl(var(--foreground))]"
              }
            >
              {opt}天
            </Button>
          ))}
        </div>
      </div>

      {/* Error banner (non-blocking, when we have stale data) */}
      {error && data && (
        <div className="flex items-center gap-2 rounded-lg border border-red-500/30 bg-red-500/10 px-4 py-2 text-sm text-red-400">
          <span>刷新失败: {error}</span>
          <Button variant="ghost" size="sm" onClick={() => fetchData(days)} className="ml-auto h-7 text-xs">
            重试
          </Button>
        </div>
      )}

      {/* ── Stats cards ───────────────────────────────────────── */}
      {data && (
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-6">
          {/* Total users */}
          <Card className="p-3">
            <div className="flex items-center justify-between gap-2 mb-1">
              <span className="flex items-center gap-1.5 text-xs text-[hsl(var(--muted-foreground))]">
                <UsersIcon className="h-3.5 w-3.5" />总用户
              </span>
              <span className="text-xl font-bold">{data.users_count.toLocaleString()}</span>
            </div>
            <p className="text-xs text-[hsl(var(--muted-foreground))]">
              活跃 {data.active_users_count} · 禁用 {data.disabled_users_count} · 过期 {data.expired_users_count} · 超限 {data.limited_users_count}
            </p>
          </Card>

          {/* Online users */}
          <Card className="p-3">
            <div className="flex items-center justify-between gap-2 mb-1">
              <span className="flex items-center gap-1.5 text-xs text-[hsl(var(--muted-foreground))]">
                <OnlineIcon className="h-3.5 w-3.5" />在线用户
              </span>
              <span className="text-xl font-bold">{data.online_users_count.toLocaleString()}</span>
            </div>
          </Card>

          {/* Total traffic */}
          <Card className="p-3">
            <div className="flex items-center justify-between gap-2 mb-1">
              <button
                className="flex items-center gap-1.5 text-xs text-[hsl(var(--muted-foreground))] hover:text-[hsl(var(--foreground))] transition-colors cursor-pointer select-none"
                onClick={() => setTrafficMode(m => m === "actual" ? "billed" : "actual")}
                title="点击切换实际带宽 / 计费流量"
              >
                <TrafficIcon className="h-3.5 w-3.5" />
                {trafficMode === "actual" ? "实际带宽" : "计费流量"}
                <span className="opacity-50">⇄</span>
              </button>
              <span className="text-xl font-bold">
                {formatBytes(trafficMode === "actual" ? data.total_used_bytes : data.total_billed_used_bytes)}
              </span>
            </div>
            <p className="text-xs text-[hsl(var(--muted-foreground))]">
              {trafficMode === "actual" ? (
                <>
                  <span className="text-[#3b82f6]">↑ {formatBytes(data.total_upload_bytes)}</span>
                  {" · "}
                  <span className="text-[#06b6d4]">↓ {formatBytes(data.total_download_bytes)}</span>
                </>
              ) : (
                <>
                  <span className="text-[#3b82f6]">↑ {formatBytes(data.total_billed_upload_bytes)}</span>
                  {" · "}
                  <span className="text-[#06b6d4]">↓ {formatBytes(data.total_billed_download_bytes)}</span>
                </>
              )}
            </p>
          </Card>

          {/* Nodes */}
          <Card className="p-3">
            <div className="flex items-center justify-between gap-2 mb-1">
              <span className="flex items-center gap-1.5 text-xs text-[hsl(var(--muted-foreground))]">
                <ServerIcon className="h-3.5 w-3.5" />节点
              </span>
              <span className="text-xl font-bold">{data.nodes_count}</span>
            </div>
            <p className="text-xs text-[hsl(var(--muted-foreground))]">在线节点</p>
          </Card>

          {/* Today traffic */}
          {(() => {
            const todayUp = (data.today_node_stats ?? []).reduce((s, n) => s + n.upload_bytes, 0);
            const todayDown = (data.today_node_stats ?? []).reduce((s, n) => s + n.download_bytes, 0);
            return (
              <Card className="p-3">
                <div className="flex items-center justify-between gap-2 mb-1">
                  <span className="flex items-center gap-1.5 text-xs text-[hsl(var(--muted-foreground))]">
                    <TrafficIcon className="h-3.5 w-3.5" />今日流量
                  </span>
                  <span className="text-xl font-bold">{formatBytes(todayUp + todayDown)}</span>
                </div>
                <p className="text-xs text-[hsl(var(--muted-foreground))]">
                  <span className="text-[#3b82f6]">↑ {formatBytes(todayUp)}</span>
                  {" · "}
                  <span className="text-[#06b6d4]">↓ {formatBytes(todayDown)}</span>
                </p>
              </Card>
            );
          })()}

          {/* Pending tickets */}
          <Card className="p-3">
            <div className="flex items-center justify-between gap-2 mb-1">
              <span className="flex items-center gap-1.5 text-xs text-[hsl(var(--muted-foreground))]">
                <TicketIcon className="h-3.5 w-3.5" />待处理工单
              </span>
              <span className="text-xl font-bold">{(data.open_tickets_count ?? 0).toLocaleString()}</span>
            </div>
            <p className="text-xs text-[hsl(var(--muted-foreground))]">未回复工单数</p>
          </Card>
        </div>
      )}

      {/* ── Alerts section ────────────────────────────────────── */}
      {data && (data.expiring_users_count > 0 || data.expired_users_count > 0 || data.limited_users_count > 0) && (
        <div className="flex flex-wrap gap-3">
          {data.expiring_users_count > 0 && (
            <div className="flex items-center gap-2 rounded-lg border border-yellow-500/20 bg-yellow-500/5 px-3 py-2">
              <ClockIcon className="h-4 w-4 text-yellow-500" />
              <span className="text-sm font-semibold text-yellow-600">{data.expiring_users_count}</span>
              <span className="text-sm text-[hsl(var(--muted-foreground))]">即将过期</span>
            </div>
          )}
          {data.expired_users_count > 0 && (
            <div className="flex items-center gap-2 rounded-lg border border-red-500/20 bg-red-500/5 px-3 py-2">
              <AlertIcon className="h-4 w-4 text-red-500" />
              <span className="text-sm font-semibold text-red-600">{data.expired_users_count}</span>
              <span className="text-sm text-[hsl(var(--muted-foreground))]">已过期</span>
            </div>
          )}
          {data.limited_users_count > 0 && (
            <div className="flex items-center gap-2 rounded-lg border border-orange-500/20 bg-orange-500/5 px-3 py-2">
              <BarChartIcon className="h-4 w-4 text-orange-500" />
              <span className="text-sm font-semibold text-orange-600">{data.limited_users_count}</span>
              <span className="text-sm text-[hsl(var(--muted-foreground))]">流量超限</span>
            </div>
          )}
        </div>
      )}

      {/* ── Node status indicators ────────────────────────────── */}
      {data?.node_stats && data.node_stats.length > 0 && (
        <Card>
          <CardContent className="pt-6">
            <p className="text-sm font-medium text-[hsl(var(--muted-foreground))] mb-3">节点状态</p>
            <div className="flex flex-wrap gap-3">
              {data.node_stats.map((node) => {
                const status = nodeStatuses.get(node.id);
                return (
                  <div
                    key={node.id}
                    className="flex items-center gap-2 rounded-md border border-[hsl(var(--border))] px-3 py-1.5"
                  >
                    <span
                      className={`h-2 w-2 rounded-full ${
                        status === "online"
                          ? "bg-green-500"
                          : status === "offline"
                            ? "bg-red-500"
                            : "bg-gray-400 animate-pulse"
                      }`}
                    />
                    <span className="text-sm text-[hsl(var(--foreground))]">{node.name}</span>
                  </div>
                );
              })}
            </div>
          </CardContent>
        </Card>
      )}

      {/* ── Daily traffic area chart ──────────────────────────── */}
      {data?.daily_traffic && data.daily_traffic.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-sm font-medium text-[hsl(var(--muted-foreground))]">
              每日流量
            </CardTitle>
          </CardHeader>
          <CardContent>
            <div className="h-80">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart
                  data={data.daily_traffic}
                  margin={{ top: 8, right: 8, left: 0, bottom: 0 }}
                >
                  <defs>
                    <linearGradient id="gradUpload" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor={BLUE} stopOpacity={0.3} />
                      <stop offset="95%" stopColor={BLUE} stopOpacity={0} />
                    </linearGradient>
                    <linearGradient id="gradDownload" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor={CYAN} stopOpacity={0.3} />
                      <stop offset="95%" stopColor={CYAN} stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" stroke={GRID_COLOR} vertical={false} />
                  <XAxis
                    dataKey="Label"
                    tick={{ fill: TICK_COLOR, fontSize: 12 }}
                    axisLine={{ stroke: GRID_COLOR }}
                    tickLine={false}
                  />
                  <YAxis
                    tick={{ fill: TICK_COLOR, fontSize: 12 }}
                    axisLine={false}
                    tickLine={false}
                    tickFormatter={formatBytesCompact}
                    width={64}
                  />
                  <RechartsTooltip content={<TrafficTooltip />} cursor={{ fill: "hsl(var(--muted) / 0.3)" }} />
                  <Legend
                    iconType="circle"
                    wrapperStyle={{ fontSize: 12, color: TICK_COLOR }}
                  />
                  <Area
                    type="monotone"
                    dataKey="UploadBytes"
                    name="上传"
                    stroke={BLUE}
                    strokeWidth={2}
                    fill="url(#gradUpload)"
                    stackId="traffic"
                  />
                  <Area
                    type="monotone"
                    dataKey="DownloadBytes"
                    name="下载"
                    stroke={CYAN}
                    strokeWidth={2}
                    fill="url(#gradDownload)"
                    stackId="traffic"
                  />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          </CardContent>
        </Card>
      )}

      {/* ── Two-column: Node traffic + Top users ──────────────── */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {/* Node traffic distribution */}
        {data?.node_combined_stats && data.node_combined_stats.length > 0 && (
          <Card>
            <CardHeader>
              <CardTitle className="text-sm font-medium text-[hsl(var(--muted-foreground))]">
                节点流量分布（实际流量）
              </CardTitle>
            </CardHeader>
            <CardContent>
              <div className="h-72">
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart
                    data={data.node_combined_stats}
                    margin={{ top: 8, right: 8, left: 0, bottom: 0 }}
                  >
                    <CartesianGrid strokeDasharray="3 3" stroke={GRID_COLOR} vertical={false} />
                    <XAxis
                      dataKey="name"
                      tick={{ fill: TICK_COLOR, fontSize: 11 }}
                      axisLine={{ stroke: GRID_COLOR }}
                      tickLine={false}
                      interval={0}
                      angle={data.node_combined_stats.length > 6 ? -30 : 0}
                      textAnchor={data.node_combined_stats.length > 6 ? "end" : "middle"}
                      height={data.node_combined_stats.length > 6 ? 60 : 30}
                    />
                    <YAxis
                      tick={{ fill: TICK_COLOR, fontSize: 12 }}
                      axisLine={false}
                      tickLine={false}
                      tickFormatter={formatBytesCompact}
                      width={64}
                    />
                    <RechartsTooltip content={<BytesTooltip />} cursor={{ fill: "hsl(var(--muted) / 0.3)" }} />
                    <Legend
                      iconType="circle"
                      wrapperStyle={{ fontSize: 12, color: TICK_COLOR }}
                    />
                    <Bar
                      dataKey="period_upload_bytes"
                      name="上传"
                      fill={PURPLE}
                      radius={[4, 4, 0, 0]}
                      stackId="node"
                    />
                    <Bar
                      dataKey="period_download_bytes"
                      name="下载"
                      fill={AMBER}
                      radius={[4, 4, 0, 0]}
                      stackId="node"
                    />
                  </BarChart>
                </ResponsiveContainer>
              </div>
            </CardContent>
          </Card>
        )}

        {/* Top users */}
        {topUsersData.length > 0 && (
          <Card>
            <CardHeader>
              <CardTitle className="text-sm font-medium text-[hsl(var(--muted-foreground))]">
                流量排行
              </CardTitle>
            </CardHeader>
            <CardContent>
              <div className="h-72">
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart
                    data={topUsersData}
                    layout="vertical"
                    margin={{ top: 8, right: 8, left: 0, bottom: 0 }}
                  >
                    <CartesianGrid strokeDasharray="3 3" stroke={GRID_COLOR} horizontal={false} />
                    <XAxis
                      type="number"
                      tick={{ fill: TICK_COLOR, fontSize: 12 }}
                      axisLine={{ stroke: GRID_COLOR }}
                      tickLine={false}
                      tickFormatter={formatBytesCompact}
                    />
                    <YAxis
                      type="category"
                      dataKey="Username"
                      tick={{ fill: TICK_COLOR, fontSize: 11 }}
                      axisLine={false}
                      tickLine={false}
                      width={80}
                    />
                    <RechartsTooltip content={<BytesTooltip />} cursor={{ fill: "hsl(var(--muted) / 0.3)" }} />
                    <Bar
                      dataKey="UsedBytes"
                      name="已用流量"
                      fill={CYAN}
                      radius={[0, 4, 4, 0]}
                      barSize={16}
                    />
                  </BarChart>
                </ResponsiveContainer>
              </div>
            </CardContent>
          </Card>
        )}
      </div>

      {/* ── Today's traffic breakdown ─────────────────────────── */}
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <TodayTrafficCard
          title="今日节点流量（实际流量）"
          items={(data?.today_node_stats ?? [])
            .filter((n) => n.total_bytes > 0)
            .sort((a, b) => b.total_bytes - a.total_bytes)
            .map((n) => ({ key: n.id, label: n.name, total: n.total_bytes, drilldown: { type: "node" as const, id: n.id, name: n.name } }))}
        />
        <TodayTrafficCard
          title="今日用户流量（计费流量）"
          items={(data?.today_user_stats ?? [])
            .filter((u) => u.total_bytes > 0)
            .map((u) => ({ key: u.username, label: u.username, total: u.total_bytes, drilldown: { type: "user" as const, username: u.username } }))}
        />
      </div>

      {/* ── Quick access ─────────────────────────────────────── */}
      <Card>
        <CardHeader className="pb-3">
          <p className="text-sm font-medium text-[hsl(var(--muted-foreground))]">快捷入口</p>
        </CardHeader>
        <CardContent>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
            <a
              href="/stat"
              target="_blank"
              rel="noopener noreferrer"
              className="flex items-center gap-3 rounded-lg border border-[hsl(var(--border))] px-4 py-3 text-sm transition-colors hover:bg-[hsl(var(--accent))] hover:text-[hsl(var(--accent-foreground))]"
            >
              <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="h-4 w-4 shrink-0 text-[hsl(var(--muted-foreground))]">
                <path d="M22 12h-4l-3 9L9 3l-3 9H2" />
              </svg>
              <span>状态页</span>
              <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="ml-auto h-3 w-3 shrink-0 text-[hsl(var(--muted-foreground))]">
                <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
                <polyline points="15 3 21 3 21 9" />
                <line x1="10" y1="14" x2="21" y2="3" />
              </svg>
            </a>
            <a
              href="/shop"
              target="_blank"
              rel="noopener noreferrer"
              className="flex items-center gap-3 rounded-lg border border-[hsl(var(--border))] px-4 py-3 text-sm transition-colors hover:bg-[hsl(var(--accent))] hover:text-[hsl(var(--accent-foreground))]"
            >
              <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="h-4 w-4 shrink-0 text-[hsl(var(--muted-foreground))]">
                <circle cx="9" cy="21" r="1" /><circle cx="20" cy="21" r="1" />
                <path d="M1 1h4l2.68 13.39a2 2 0 0 0 2 1.61h9.72a2 2 0 0 0 2-1.61L23 6H6" />
              </svg>
              <span>商店</span>
              <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="ml-auto h-3 w-3 shrink-0 text-[hsl(var(--muted-foreground))]">
                <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
                <polyline points="15 3 21 3 21 9" />
                <line x1="10" y1="14" x2="21" y2="3" />
              </svg>
            </a>
          </div>
        </CardContent>
      </Card>

      {/* ── Loading overlay for refetch ───────────────────────── */}
      {loading && data && (
        <div className="pointer-events-none fixed inset-0 z-50 flex items-start justify-center pt-20">
          <div className="pointer-events-auto flex items-center gap-2 rounded-full bg-[hsl(var(--card))] px-4 py-2 text-sm shadow-lg border border-[hsl(var(--border))]">
            <SpinnerIcon className="h-4 w-4 animate-spin text-[hsl(var(--muted-foreground))]" />
            <span className="text-[hsl(var(--muted-foreground))]">加载中…</span>
          </div>
        </div>
      )}
    </div>
  );
}

/* ── Inline SVG icon components ──────────────────────────────── */

function UsersIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" {...props}>
      <path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2" />
      <circle cx="9" cy="7" r="4" />
      <path d="M22 21v-2a4 4 0 0 0-3-3.87" />
      <path d="M16 3.13a4 4 0 0 1 0 7.75" />
    </svg>
  );
}

function OnlineIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" {...props}>
      <path d="M5 12.55a11 11 0 0 1 14.08 0" />
      <path d="M1.42 9a16 16 0 0 1 21.16 0" />
      <path d="M8.53 16.11a6 6 0 0 1 6.95 0" />
      <circle cx="12" cy="20" r="1" />
    </svg>
  );
}

function TrafficIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" {...props}>
      <polyline points="22 12 18 12 15 21 9 3 6 12 2 12" />
    </svg>
  );
}

function ServerIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" {...props}>
      <rect x="2" y="2" width="20" height="8" rx="2" ry="2" />
      <rect x="2" y="14" width="20" height="8" rx="2" ry="2" />
      <line x1="6" y1="6" x2="6.01" y2="6" />
      <line x1="6" y1="18" x2="6.01" y2="18" />
    </svg>
  );
}

function ClockIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" {...props}>
      <circle cx="12" cy="12" r="10" />
      <polyline points="12 6 12 12 16 14" />
    </svg>
  );
}

function TicketIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" {...props}>
      <path d="M2 9a3 3 0 0 1 0 6v2a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-2a3 3 0 0 1 0-6V7a2 2 0 0 0-2-2H4a2 2 0 0 0-2 2Z" />
      <path d="M13 5v2" />
      <path d="M13 17v2" />
      <path d="M13 11v2" />
    </svg>
  );
}

function AlertIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" {...props}>
      <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
      <line x1="12" y1="9" x2="12" y2="13" />
      <line x1="12" y1="17" x2="12.01" y2="17" />
    </svg>
  );
}

function BarChartIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" {...props}>
      <line x1="12" y1="20" x2="12" y2="10" />
      <line x1="18" y1="20" x2="18" y2="4" />
      <line x1="6" y1="20" x2="6" y2="16" />
    </svg>
  );
}

function SpinnerIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" {...props}>
      <path d="M21 12a9 9 0 1 1-6.219-8.56" />
    </svg>
  );
}

// ── Today traffic breakdown card ─────────────────────────────────
type DrilldownState =
  | { type: "node"; id: string; name: string }
  | { type: "user"; username: string }
  | null;

function DrilldownDialog({
  drilldown,
  onClose,
}: {
  drilldown: DrilldownState;
  onClose: () => void;
}) {
  const [loading, setLoading] = useState(false);
  const [nodeUsers, setNodeUsers] = useState<TodayUserStat[] | null>(null);
  const [userNodes, setUserNodes] = useState<TodayNodeStat[] | null>(null);

  useEffect(() => {
    if (!drilldown) return;
    setLoading(true);
    setNodeUsers(null);
    setUserNodes(null);

    if (drilldown.type === "node") {
      api
        .get<{ users: TodayUserStat[] }>(`/stats/nodes/${drilldown.id}/today-users`)
        .then((r) => setNodeUsers(r.users))
        .finally(() => setLoading(false));
    } else {
      api
        .get<{ nodes: TodayNodeStat[] }>(`/stats/users/${encodeURIComponent(drilldown.username)}/today-nodes`)
        .then((r) => setUserNodes(r.nodes))
        .finally(() => setLoading(false));
    }
  }, [drilldown]);

  const title =
    drilldown?.type === "node"
      ? `${drilldown.name} · 今日用户分布`
      : drilldown?.type === "user"
        ? `${drilldown.username} · 今日节点分布`
        : "";

  const items: { key: string; label: string; total: number; rawTotal: number }[] =
    drilldown?.type === "node" && nodeUsers
      ? nodeUsers.map((u) => ({ key: u.username, label: u.username, total: u.total_bytes, rawTotal: u.raw_total_bytes }))
      : drilldown?.type === "user" && userNodes
        ? userNodes.map((n) => ({ key: n.node_id, label: n.node_name, total: n.total_bytes, rawTotal: n.raw_total_bytes }))
        : [];

  const max = Math.max(...items.map((i) => i.total), 1);
  const totalAll = items.reduce((s, i) => s + i.total, 0);

  return (
    <Dialog open={!!drilldown} onOpenChange={(open) => { if (!open) onClose(); }}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle className="text-sm font-medium">{title}</DialogTitle>
          <DialogDescription className="text-xs">
            计费流量（实际流量 × 节点倍率），非实际带宽；占比为该维度内份额
          </DialogDescription>
        </DialogHeader>
        {loading ? (
          <div className="flex items-center justify-center py-8">
            <SpinnerIcon className="h-5 w-5 animate-spin text-[hsl(var(--muted-foreground))]" />
          </div>
        ) : items.length === 0 ? (
          <p className="py-6 text-center text-sm text-[hsl(var(--muted-foreground))]">暂无数据</p>
        ) : (
          <ScrollArea className="max-h-[60vh] pr-3">
            <div className="space-y-3 py-2">
              {items.map((item) => {
                const pct = totalAll > 0 ? (item.total / totalAll) * 100 : 0;
                const barPct = (item.total / max) * 100;
                return (
                  <div key={item.key}>
                    <div className="mb-1 flex items-center justify-between gap-2">
                      <span className="truncate text-xs text-[hsl(var(--foreground))]" title={item.label}>
                        {item.label}
                      </span>
                      <span className="flex shrink-0 items-center gap-1.5 text-xs tabular-nums text-[hsl(var(--muted-foreground))]">
                        <span>
                          {item.rawTotal > 0 && item.rawTotal !== item.total ? (
                            <>
                              <span className="opacity-70">{formatBytes(item.rawTotal)} → </span>
                              <span>{formatBytes(item.total)}</span>
                            </>
                          ) : (
                            formatBytes(item.total)
                          )}
                        </span>
                        <span className="opacity-50">{pct.toFixed(1)}%</span>
                      </span>
                    </div>
                    <div className="h-2 w-full overflow-hidden rounded-full bg-[hsl(var(--muted))]">
                      <div className="h-full rounded-full bg-[#3b82f6]" style={{ width: `${barPct}%` }} />
                    </div>
                  </div>
                );
              })}
            </div>
          </ScrollArea>
        )}
      </DialogContent>
    </Dialog>
  );
}

function TodayTrafficCard({
  title,
  items,
}: {
  title: string;
  items: { key: string; label: string; total: number; drilldown: DrilldownState }[];
}) {
  const [drilldown, setDrilldown] = useState<DrilldownState>(null);

  if (items.length === 0) return null;

  const max = Math.max(...items.map((i) => i.total), 1);
  const totalAll = items.reduce((s, i) => s + i.total, 0);

  return (
    <>
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-sm font-medium text-[hsl(var(--muted-foreground))]">
            {title}
          </CardTitle>
        </CardHeader>
        <CardContent>
          <div className="space-y-3">
            {items.map((item) => {
              const pct = totalAll > 0 ? (item.total / totalAll) * 100 : 0;
              const barPct = (item.total / max) * 100;
              return (
                <button
                  key={item.key}
                  className="w-full text-left group"
                  onClick={() => setDrilldown(item.drilldown)}
                >
                  <div className="mb-1 flex items-center justify-between gap-2">
                    <span
                      className="truncate text-xs text-[hsl(var(--foreground))] group-hover:text-[#3b82f6] transition-colors"
                      title={item.label}
                    >
                      {item.label}
                    </span>
                    <span className="flex shrink-0 items-center gap-1.5 text-xs tabular-nums text-[hsl(var(--muted-foreground))]">
                      <span>{formatBytes(item.total)}</span>
                      <span className="opacity-50">{pct.toFixed(1)}%</span>
                    </span>
                  </div>
                  <div className="h-2 w-full overflow-hidden rounded-full bg-[hsl(var(--muted))]">
                    <div
                      className="h-full rounded-full bg-[#3b82f6] group-hover:bg-[#60a5fa] transition-colors"
                      style={{ width: `${barPct}%` }}
                    />
                  </div>
                </button>
              );
            })}
          </div>
        </CardContent>
      </Card>

      <DrilldownDialog drilldown={drilldown} onClose={() => setDrilldown(null)} />
    </>
  );
}
