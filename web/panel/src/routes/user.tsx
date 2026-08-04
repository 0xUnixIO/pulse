import { useState, useEffect, useCallback, useMemo, useRef } from "react";
import { marked } from "marked";
import { getTheme, toggleTheme, type Theme } from "@/lib/theme";
import MDEditor, { commands } from "@uiw/react-md-editor";
import { useParams } from "@tanstack/react-router";
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip as RechartsTooltip,
  ResponsiveContainer,
  Legend,
} from "recharts";
import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardContent,
  Badge,
  Button,
  Table,
  TableHeader,
  TableBody,
  TableHead,
  TableRow,
  TableCell,
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
} from "@/components/ui";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
  ConfirmDialog,
  Separator,
  Input,
  toast,
} from "@/components/ui";
import { ScrollArea } from "@/components/ui/scroll-area";
import { formatBytes, formatBytesCompact } from "@/lib/format";
import { copyText } from "@/lib/clipboard";

/* ── Types ────────────────────────────────────────────────────── */

interface PortalInfo {
  username: string;
  status: string;
  sub_url: string;
  upload_bytes: number;
  download_bytes: number;
  total_bytes: number;
  data_limit: number;
  expire_at: string | null;
  next_traffic_reset_at: string | null;
  nodes: { name: string; protocols: string[] }[];
  announcements?: Array<{ id: string; title: string; content: string; enabled: boolean; created_at: string }>;
  plan_name?: string;
}

interface DailyUsage {
  date: string;
  upload_bytes: number;
  download_bytes: number;
}

interface NodeUsage {
  node_id: string;
  node_name: string;
  upload_bytes: number;
  download_bytes: number;
  total_bytes: number;
  raw_upload_bytes: number;
  raw_download_bytes: number;
  raw_total_bytes: number;
}

type PortalStatus = "active" | "limited" | "expired" | "disabled" | "on_hold";

interface PortalTicket {
  id: string;
  user_id: string;
  username: string;
  title: string;
  status: "open" | "replied" | "closed";
  created_at: string;
  updated_at: string;
}

interface PortalMessage {
  id: string;
  ticket_id: string;
  content: string;
  is_admin: boolean;
  created_at: string;
}

interface PortalImage {
  id: string;
  ticket_id: string;
  filename: string;
  stored_name: string;
  size: number;
  created_at: string;
}

/* ── Constants ────────────────────────────────────────────────── */

const BLUE = "#3b82f6";
const CYAN = "#06b6d4";
const TICK_COLOR = "#a1a1aa";

const STATUS_CONFIG: Record<PortalStatus, { label: string; color: string }> = {
  active:   { label: "正常",   color: "bg-emerald-500/15 text-emerald-600 border-emerald-500/25" },
  limited:  { label: "流量耗尽", color: "bg-red-500/15 text-red-600 border-red-500/25" },
  expired:  { label: "已过期", color: "bg-red-500/15 text-red-600 border-red-500/25" },
  disabled: { label: "已禁用", color: "bg-zinc-500/15 text-zinc-500 border-zinc-500/25" },
  on_hold:  { label: "暂停",   color: "bg-orange-500/15 text-orange-600 border-orange-500/25" },
};

/* ── Helpers ──────────────────────────────────────────────────── */

function statusBadge(status: string) {
  const cfg = STATUS_CONFIG[status as PortalStatus] ?? {
    label: status,
    color: "bg-zinc-500/15 text-zinc-500 border-zinc-500/25",
  };
  return (
    <Badge variant="outline" className={`${cfg.color} font-medium`}>
      {cfg.label}
    </Badge>
  );
}

function formatExpiry(expireAt: string | null): string {
  if (!expireAt) return "永不过期";
  const d = new Date(expireAt);
  return d.toLocaleDateString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  });
}

function formatResetAt(resetAt: string | null): string | null {
  if (!resetAt) return null;
  const d = new Date(resetAt);
  return d.toLocaleDateString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  });
}

function usagePercent(used: number, limit: number): number {
  if (limit <= 0) return 0;
  return Math.min(100, (used / limit) * 100);
}

/* ── Pulse Logo ───────────────────────────────────────────────── */

function PulseLogo({ className = "h-8 w-8" }: { className?: string }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 32 32"
      className={className}
    >
      <rect width="32" height="32" rx="7" fill="#18181b" />
      <polyline
        points="4,16 9,16 12,9 16,23 20,12 23,16 28,16"
        fill="none"
        stroke="#fafafa"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

/* ── Custom recharts tooltip ──────────────────────────────────── */

function ChartTooltip({ active, payload, label }: any) {
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

/* ── NodeSelectionTab ──────────────────────────────────────────── */

interface PortalHost {
  host_id: string;
  protocol: string;
  country: string;
  region: string;
  network: string;
  entry: string;
  remark: string;
  node_name: string;
  outbound_name: string;
  excluded: boolean;
}

function NodeSelectionTab({ token }: { token: string }) {
  const [hosts, setHosts] = useState<PortalHost[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  // pending: host_id → excluded 的本地暂存，与服务端不同时才有记录
  const [pending, setPending] = useState<Map<string, boolean>>(new Map());
  const [filterProto, setFilterProto] = useState<string>("all");
  const [filterRegion, setFilterRegion] = useState<string>("all");

  useEffect(() => {
    fetch(`/v1/portal/${token}/hosts`)
      .then((r) => r.json())
      .then((d) => setHosts(d.hosts ?? []))
      .catch(() => toast.error("加载节点列表失败"))
      .finally(() => setLoading(false));
  }, [token]);

  // 某个 host 的有效 excluded 状态（pending 优先）
  const effectiveExcluded = useCallback((h: PortalHost) =>
    pending.has(h.host_id) ? pending.get(h.host_id)! : h.excluded,
  [pending]);

  const protocols = useMemo(() => {
    const set = new Set(hosts.map((h) => h.protocol.toUpperCase()));
    return Array.from(set).sort();
  }, [hosts]);

  const regions = useMemo(() => {
    const map = new Map<string, string>();
    hosts.forEach((h) => { if (h.region) map.set(h.region, h.country); });
    return Array.from(map.entries()).sort((a, b) => a[0].localeCompare(b[0]));
  }, [hosts]);

  const filtered = useMemo(() => {
    const list = hosts.filter((h) => {
      if (filterProto !== "all" && h.protocol.toUpperCase() !== filterProto) return false;
      if (filterRegion !== "all" && h.region !== filterRegion) return false;
      return true;
    });
    // 已排除（含 pending 中排除的）排到顶部
    return list.sort((a, b) => {
      const ae = pending.has(a.host_id) ? pending.get(a.host_id)! : a.excluded;
      const be = pending.has(b.host_id) ? pending.get(b.host_id)! : b.excluded;
      return Number(be) - Number(ae);
    });
  }, [hosts, filterProto, filterRegion, pending]);

  const toggle = useCallback((h: PortalHost) => {
    const cur = pending.has(h.host_id) ? pending.get(h.host_id)! : h.excluded;
    const next = !cur;
    setPending((prev) => {
      const m = new Map(prev);
      // 如果和服务端状态一致则移除（无需提交）
      if (next === h.excluded) { m.delete(h.host_id); } else { m.set(h.host_id, next); }
      return m;
    });
  }, [pending]);

  const setAllFiltered = useCallback((excluded: boolean) => {
    setPending((prev) => {
      const m = new Map(prev);
      hosts.forEach((h) => {
        if (excluded !== h.excluded) { m.set(h.host_id, excluded); } else { m.delete(h.host_id); }
      });
      return m;
    });
  }, [hosts]);

  const isDirty = pending.size > 0;

  const handleSave = async () => {
    if (!isDirty) return;
    setSaving(true);
    try {
      await Promise.all(Array.from(pending.entries()).map(([host_id, excluded]) =>
        fetch(`/v1/portal/${token}/hosts/exclude`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ host_id, excluded }),
        })
      ));
      // 保存成功：将 pending 合并到 hosts，清空 pending
      setHosts((prev) => prev.map((h) =>
        pending.has(h.host_id) ? { ...h, excluded: pending.get(h.host_id)! } : h
      ));
      setPending(new Map());
      toast.success("已保存");
    } catch {
      toast.error("保存失败，请重试");
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12">
        <svg className="h-5 w-5 animate-spin text-[hsl(var(--muted-foreground))]" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
          <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
          <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8z" />
        </svg>
      </div>
    );
  }

  if (!hosts.length) {
    return (
      <div className="py-12 text-center text-sm text-[hsl(var(--muted-foreground))]">
        暂无可用节点
      </div>
    );
  }

  const allFilteredEnabled = hosts.every((h) => !effectiveExcluded(h));
  const allFilteredDisabled = hosts.every((h) => effectiveExcluded(h));

  return (
    <div className="space-y-4">
      {/* 协议筛选 */}
      {protocols.length > 1 && (
        <div className="flex flex-wrap gap-1.5">
          {["all", ...protocols].map((p) => (
            <button
              key={p}
              onClick={() => setFilterProto(p)}
              className={`rounded-full px-3 py-0.5 text-xs font-medium transition-colors ${
                filterProto === p
                  ? "bg-[hsl(var(--primary))] text-[hsl(var(--primary-foreground))]"
                  : "bg-[hsl(var(--muted))] text-[hsl(var(--muted-foreground))] hover:bg-[hsl(var(--accent))]"
              }`}
            >
              {p === "all" ? "全部协议" : p}
            </button>
          ))}
        </div>
      )}

      {/* 地区筛选 */}
      {regions.length > 1 && (
        <div className="flex flex-wrap gap-1.5">
          {[["all", ""], ...regions].map(([region, country]) => (
            <button
              key={region}
              onClick={() => setFilterRegion(region ?? "all")}
              className={`rounded-full px-3 py-0.5 text-xs font-medium transition-colors ${
                filterRegion === region
                  ? "bg-[hsl(var(--primary))] text-[hsl(var(--primary-foreground))]"
                  : "bg-[hsl(var(--muted))] text-[hsl(var(--muted-foreground))] hover:bg-[hsl(var(--accent))]"
              }`}
            >
              {region === "all" ? "全部地区" : `${country} ${region}`}
            </button>
          ))}
        </div>
      )}

      {/* 批量操作 + 保存按钮 */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3 text-xs text-[hsl(var(--muted-foreground))]">
          <span>{hosts.filter((h) => effectiveExcluded(h)).length} / {hosts.length} 已排除</span>
          <button
            className="hover:text-[hsl(var(--foreground))] disabled:opacity-40"
            disabled={allFilteredDisabled}
            onClick={() => setAllFiltered(true)}
          >
            全部排除
          </button>
          <span>·</span>
          <button
            className="hover:text-[hsl(var(--foreground))] disabled:opacity-40"
            disabled={allFilteredEnabled}
            onClick={() => setAllFiltered(false)}
          >
            清除排除
          </button>
        </div>
        <Button size="sm" disabled={!isDirty || saving} onClick={handleSave}>
          {saving ? (
            <>
              <svg className="mr-1.5 h-3.5 w-3.5 animate-spin" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
                <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4"/>
                <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8z"/>
              </svg>
              保存中…
            </>
          ) : isDirty ? `保存（${pending.size}）` : "已保存"}
        </Button>
      </div>

      {/* 节点列表 */}
      <div className="space-y-1">
        {filtered.map((h) => {
          const excl = effectiveExcluded(h);
          const changed = pending.has(h.host_id);
          return (
            <button
              key={h.host_id}
              type="button"
              onClick={() => toggle(h)}
              className={`flex w-full items-center gap-3 rounded-lg border px-3 py-2.5 text-left transition-colors hover:bg-[hsl(var(--accent))] ${
                excl ? "opacity-50" : ""
              } ${changed ? "border-orange-400/40" : "border-[hsl(var(--border))]"}`}
            >
              {/* 复选框：勾选 = 排除 */}
              <div className={`h-4 w-4 shrink-0 rounded border-2 transition-colors flex items-center justify-center ${
                excl
                  ? "border-orange-500 bg-orange-500"
                  : "border-[hsl(var(--border))] bg-transparent"
              }`}>
                {excl && (
                  <svg className="h-2.5 w-2.5 text-white" viewBox="0 0 10 10">
                    <path d="M2 2L8 8M8 2L2 8" stroke="currentColor" strokeWidth="1.5" fill="none" strokeLinecap="round"/>
                  </svg>
                )}
              </div>

              {/* 国旗 */}
              {h.country && <span className="text-lg leading-none">{h.country}</span>}

              {/* 名称区 */}
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">
                  {[h.region, h.network, h.entry].filter(Boolean).join(" · ") || h.remark || h.outbound_name || h.node_name}
                </p>
                <p className="truncate text-xs text-[hsl(var(--muted-foreground))]">
                  {h.outbound_name || h.node_name}
                </p>
              </div>

              {/* 订阅名 + 协议 badge */}
              <div className="flex shrink-0 items-center gap-1.5">
                {h.remark && (
                  <span className="max-w-[120px] truncate text-[10px] text-[hsl(var(--muted-foreground))]">
                    {h.remark}
                  </span>
                )}
                <span className="rounded bg-[hsl(var(--muted))] px-1.5 py-0.5 text-[10px] font-mono uppercase text-[hsl(var(--muted-foreground))]">
                  {h.protocol}
                </span>
              </div>
            </button>
          );
        })}
      </div>
    </div>
  );
}

/* ── Page ──────────────────────────────────────────────────────── */

export default function UserPage() {
  const { token } = useParams({ strict: false }) as { token: string };

  const [info, setInfo] = useState<PortalInfo | null>(null);
  const [dailyUsage, setDailyUsage] = useState<DailyUsage[]>([]);
  const [nodeUsage, setNodeUsage] = useState<NodeUsage[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState<string | null>(null);
  const [resetting, setResetting] = useState(false);
  const [resettingTraffic, setResettingTraffic] = useState(false);
  const [resetTrafficConfirmOpen, setResetTrafficConfirmOpen] = useState(false);
  const [resetTokenConfirmOpen, setResetTokenConfirmOpen] = useState(false);
  const [repliedTicketCount, setRepliedTicketCount] = useState(0);

  /* ── Fetch all data ──────────────────────────────────────────── */

  useEffect(() => {
    if (!token) return;
    let cancelled = false;

    async function load() {
      try {
        const [infoRes, dailyRes, nodeRes] = await Promise.all([
          fetch(`/v1/portal/${token}/info`),
          fetch(`/v1/portal/${token}/daily-usage`),
          fetch(`/v1/portal/${token}/node-usage`),
        ]);

        if (!infoRes.ok) {
          if (infoRes.status === 401 || infoRes.status === 404) {
            throw new Error("链接无效或已过期");
          }
          throw new Error(`HTTP ${infoRes.status}`);
        }

        const infoData = await infoRes.json();
        const dailyData = dailyRes.ok ? await dailyRes.json() : { daily: [] };
        const nodeData = nodeRes.ok ? await nodeRes.json() : { usage: [] };

        if (!cancelled) {
          setInfo(infoData);
          setDailyUsage(dailyData.daily ?? []);
          setNodeUsage(nodeData.usage ?? []);
        }
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : "加载失败");
      } finally {
        if (!cancelled) setLoading(false);
      }
    }

    load();
    return () => { cancelled = true; };
  }, [token]);

  /* ── Copy sub URL ────────────────────────────────────────────── */

  const copyToClipboard = useCallback(async (text: string, label: string) => {
    try {
      await copyText(text);
      setCopied(label);
      setTimeout(() => setCopied(null), 2000);
    } catch {
      window.alert("复制失败，请手动选中内容复制");
    }
  }, []);

  const copySubUrl = useCallback(() => {
    if (!info?.sub_url) return;
    copyToClipboard(info.sub_url, "sub");
  }, [info?.sub_url, copyToClipboard]);

  const copyClashUrl = useCallback(() => {
    if (!info?.sub_url) return;
    const url = `https://mnn.qzz.io/convert?template=161573365351168&url=${encodeURIComponent(info.sub_url)}`;
    copyToClipboard(url, "clash");
  }, [info?.sub_url, copyToClipboard]);

  const copySurgeUrl = useCallback(() => {
    if (!info?.sub_url) return;
    const url = `https://mnn.qzz.io/convert?template=161782616067712&target=surge&url=${encodeURIComponent(info.sub_url)}`;
    copyToClipboard(url, "surge");
  }, [info?.sub_url, copyToClipboard]);

  const copySingboxUrl = useCallback(() => {
    if (!info?.sub_url) return;
    const url = `https://mnn.qzz.io/convert?template=161268503412384&target=sing-box&url=${encodeURIComponent(info.sub_url)}`;
    copyToClipboard(url, "singbox");
  }, [info?.sub_url, copyToClipboard]);

  /* ── Reset subscription token ─────────────────────────────────── */

  function handleResetToken() {
    setResetTokenConfirmOpen(true);
  }

  async function doResetToken() {
    setResetTokenConfirmOpen(false);
    setResetting(true);
    try {
      const res = await fetch(`/api/me/reset-token?token=${token}`, { method: "POST" });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || "重置失败");
      window.location.replace(`/user/${data.token}`);
    } catch (err) {
      toast(err instanceof Error ? err.message : "重置失败", "error");
      setResetting(false);
    }
  }

  async function doResetTraffic() {
    setResetTrafficConfirmOpen(false);
    setResettingTraffic(true);
    try {
      const res = await fetch(`/v1/portal/${token}/reset-traffic`, { method: "POST" });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || "重置流量失败");

      const infoRes = await fetch(`/v1/portal/${token}/info`);
      if (!infoRes.ok) throw new Error("刷新账户信息失败");
      setInfo(await infoRes.json());
      toast("流量已重置", "success");
    } catch (err) {
      toast(err instanceof Error ? err.message : "重置流量失败", "error");
    } finally {
      setResettingTraffic(false);
    }
  }

  /* ── Loading / Error / Password states ───────────────────────── */

  if (loading) {
    return (
      <PageShell>
        <div className="flex justify-center py-20">
          <div className="h-8 w-8 animate-spin rounded-full border-4 border-[hsl(var(--muted))] border-t-[hsl(var(--primary))]" />
        </div>
      </PageShell>
    );
  }

  if (error || !info) {
    return (
      <PageShell>
        <div className="mx-auto max-w-md py-20 text-center">
          <div className="mb-4 text-5xl">😔</div>
          <h2 className="mb-2 text-lg font-semibold">
            {error || "未找到信息"}
          </h2>
          <p className="text-sm text-[hsl(var(--muted-foreground))]">
            请检查链接是否正确
          </p>
        </div>
      </PageShell>
    );
  }

  const totalUsed = info.upload_bytes + info.download_bytes;
  const pct = usagePercent(totalUsed, info.data_limit);
  const canResetTraffic =
    !!info.expire_at &&
    new Date(info.expire_at).getTime() > Date.now() + 30 * 24 * 60 * 60 * 1000;
  const canResetTrafficStatus = info.status === "active" || info.status === "limited";
  const resetTrafficHint = !info.expire_at
    ? "永久有效账户无法自助重置流量"
    : !canResetTrafficStatus
      ? "当前账户状态无法自助重置流量"
      : !canResetTraffic
        ? "剩余有效期不足 30 天，无法自助重置流量"
        : undefined;

  /* ── Chart data ──────────────────────────────────────────────── */

  const chartData = dailyUsage.map((d) => ({
    date: d.date.slice(5), // "MM-DD"
    上传: d.upload_bytes,
    下载: d.download_bytes,
  }));

  /* ── Render ──────────────────────────────────────────────────── */

  return (
    <PageShell>
      <div className="space-y-4">
        {/* Announcements — always visible above tabs */}
        {info.announcements && info.announcements.length > 0 && (
          <AnnouncementsSection announcements={info.announcements} />
        )}

        <Tabs defaultValue="overview" className="w-full">
          <TabsList className="w-full grid" style={{ gridTemplateColumns: "repeat(5, 1fr)" }}>
            <TabsTrigger value="overview">概览</TabsTrigger>
            <TabsTrigger value="traffic">流量</TabsTrigger>
            <TabsTrigger value="nodes">排除节点</TabsTrigger>
            <TabsTrigger value="tickets" className="relative">
              工单
              {repliedTicketCount > 0 && (
                <span className="absolute -top-1 -right-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-[hsl(var(--destructive))] px-1 text-[10px] font-bold text-[hsl(var(--destructive-foreground))] leading-none">
                  {repliedTicketCount}
                </span>
              )}
            </TabsTrigger>
            <TabsTrigger value="settings">设置</TabsTrigger>
          </TabsList>

          {/* ── 概览 ──────────────────────────────────────────────── */}
          <TabsContent value="overview" className="space-y-4 mt-4">
            {/* User Info */}
            <Card>
              <CardHeader>
                <CardTitle className="text-lg">账户信息</CardTitle>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="flex flex-wrap items-center gap-3">
                  <span className="text-lg font-semibold">{info.username}</span>
                  {statusBadge(info.status)}
                  {info.plan_name && (
                    <Badge variant="secondary">{info.plan_name}</Badge>
                  )}
                </div>

                <div className="space-y-2">
                  <div className="flex items-baseline justify-between text-sm">
                    <span className="text-[hsl(var(--muted-foreground))]">已用流量</span>
                    <span className={`font-medium ${
                      info.status === "limited" || pct >= 100
                        ? "text-red-500"
                        : pct >= 80
                          ? "text-orange-500"
                          : ""
                    }`}>
                      {formatBytes(totalUsed)}
                      {info.data_limit > 0 && (
                        <span className="text-[hsl(var(--muted-foreground))]">
                          {" "}/ {formatBytes(info.data_limit)}
                        </span>
                      )}
                    </span>
                  </div>
                  {info.data_limit > 0 && (
                    <div className="h-2.5 w-full overflow-hidden rounded-full bg-[hsl(var(--muted))]">
                      <div
                        className={`h-full rounded-full transition-all ${
                          pct >= 90 ? "bg-red-500" : pct >= 70 ? "bg-amber-500" : "bg-emerald-500"
                        }`}
                        style={{ width: `${pct}%` }}
                      />
                    </div>
                  )}
                  <div className="flex flex-wrap gap-4 text-xs text-[hsl(var(--muted-foreground))]">
                    <span>↑ 上传 {formatBytes(info.upload_bytes)}</span>
                    <span>↓ 下载 {formatBytes(info.download_bytes)}</span>
                  </div>
                  <Button
                    variant="outline"
                    size="sm"
                    className="w-full"
                    disabled={resettingTraffic || totalUsed === 0 || !canResetTraffic || !canResetTrafficStatus}
                    onClick={() => setResetTrafficConfirmOpen(true)}
                    title={resetTrafficHint}
                  >
                    {resettingTraffic ? "重置中…" : "重置流量"}
                  </Button>
                </div>

                <div className="flex items-center gap-2 text-sm">
                  <ClockIcon className="h-4 w-4 text-[hsl(var(--muted-foreground))]" />
                  <span className="text-[hsl(var(--muted-foreground))]">到期时间：</span>
                  <span className={`font-medium ${(() => {
                    if (!info.expire_at) return "";
                    const ms = new Date(info.expire_at).getTime() - Date.now();
                    if (ms <= 0) return "text-red-500";
                    if (ms <= 7 * 24 * 60 * 60 * 1000) return "text-orange-500";
                    return "";
                  })()}`}>{formatExpiry(info.expire_at)}</span>
                </div>
                {formatResetAt(info.next_traffic_reset_at) && (
                  <div className="flex items-center gap-2 text-sm">
                    <ClockIcon className="h-4 w-4 text-[hsl(var(--muted-foreground))]" />
                    <span className="text-[hsl(var(--muted-foreground))]">流量重置：</span>
                    <span className="font-medium">{formatResetAt(info.next_traffic_reset_at)}</span>
                  </div>
                )}
              </CardContent>
            </Card>

            {/* Subscription URL */}
            <Card>
              <CardHeader>
                <CardTitle className="text-lg">订阅链接</CardTitle>
              </CardHeader>
              <CardContent>
                <div className="flex gap-2">
                  <div className="min-w-0 flex-1 overflow-hidden rounded-md border border-[hsl(var(--border))] bg-[hsl(var(--muted))] px-3 py-2">
                    <p className="truncate font-mono text-sm">{info.sub_url}</p>
                  </div>
                  <Button
                    variant={copied === "sub" ? "default" : "outline"}
                    className="shrink-0"
                    onClick={copySubUrl}
                  >
                    {copied === "sub" ? "已复制 ✓" : "复制"}
                  </Button>
                </div>
                <div className="mt-3 flex gap-2">
                  <Button
                    variant={copied === "clash" ? "default" : "outline"}
                    size="sm"
                    onClick={copyClashUrl}
                  >
                    {copied === "clash" ? "已复制 ✓" : "Clash"}
                  </Button>
                  <Button
                    variant={copied === "surge" ? "default" : "outline"}
                    size="sm"
                    onClick={copySurgeUrl}
                  >
                    {copied === "surge" ? "已复制 ✓" : "Surge"}
                  </Button>
                  <Button
                    variant={copied === "singbox" ? "default" : "outline"}
                    size="sm"
                    onClick={copySingboxUrl}
                  >
                    {copied === "singbox" ? "已复制 ✓" : "SingBox"}
                  </Button>
                </div>
                <p className="mt-2 text-xs text-[hsl(var(--muted-foreground))]">
                  将此链接添加到您的客户端应用中
                </p>
              </CardContent>
            </Card>

          </TabsContent>

          {/* ── 流量 ──────────────────────────────────────────────── */}
          <TabsContent value="traffic" className="space-y-4 mt-4">
            {chartData.length > 0 ? (
              <Card>
                <CardHeader>
                  <CardTitle className="text-lg">近期流量趋势</CardTitle>
                </CardHeader>
                <CardContent>
                  <div className="h-64 w-full">
                    <ResponsiveContainer width="100%" height="100%">
                      <BarChart data={chartData} margin={{ top: 5, right: 10, left: 0, bottom: 5 }}>
                        <XAxis
                          dataKey="date"
                          tick={{ fill: TICK_COLOR, fontSize: 12 }}
                          axisLine={false}
                          tickLine={false}
                        />
                        <YAxis
                          tickFormatter={formatBytesCompact}
                          tick={{ fill: TICK_COLOR, fontSize: 12 }}
                          axisLine={false}
                          tickLine={false}
                          width={60}
                        />
                        <RechartsTooltip content={<ChartTooltip />} cursor={{ fill: "hsl(var(--muted) / 0.3)" }} />
                        <Legend wrapperStyle={{ fontSize: 12, color: TICK_COLOR }} />
                        <Bar dataKey="上传" fill={CYAN} radius={[3, 3, 0, 0]} maxBarSize={32} />
                        <Bar dataKey="下载" fill={BLUE} radius={[3, 3, 0, 0]} maxBarSize={32} />
                      </BarChart>
                    </ResponsiveContainer>
                  </div>
                </CardContent>
              </Card>
            ) : (
              <p className="py-12 text-center text-sm text-[hsl(var(--muted-foreground))]">暂无流量数据</p>
            )}

            {nodeUsage.length > 0 && (
              <Card>
                <CardHeader>
                  <CardTitle className="text-lg">节点流量分布</CardTitle>
                  <CardDescription>
                    实际 = 不含倍率的真实流量；计费 = 实际 × 节点倍率
                  </CardDescription>
                </CardHeader>
                <CardContent>
                  <div className="hidden sm:block">
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>节点</TableHead>
                          <TableHead className="text-right">上传</TableHead>
                          <TableHead className="text-right">下载</TableHead>
                          <TableHead className="text-right">实际</TableHead>
                          <TableHead className="text-right">计费</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {nodeUsage.map((n) => (
                          <TableRow key={n.node_id}>
                            <TableCell className="font-medium">{n.node_name}</TableCell>
                            <TableCell className="text-right">{formatBytes(n.upload_bytes)}</TableCell>
                            <TableCell className="text-right">{formatBytes(n.download_bytes)}</TableCell>
                            <TableCell className="text-right text-[hsl(var(--muted-foreground))]">
                              {n.raw_total_bytes > 0 && n.raw_total_bytes !== n.total_bytes
                                ? formatBytes(n.raw_total_bytes)
                                : "-"}
                            </TableCell>
                            <TableCell className="text-right font-medium">{formatBytes(n.total_bytes)}</TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </div>
                  <div className="space-y-3 sm:hidden">
                    {nodeUsage.map((n) => {
                      const maxTotal = Math.max(...nodeUsage.map((x) => x.total_bytes), 1);
                      const barPct = (n.total_bytes / maxTotal) * 100;
                      return (
                        <div key={n.node_id} className="rounded-lg border border-[hsl(var(--border))] p-3">
                          <div className="mb-1 flex items-center justify-between">
                            <span className="text-sm font-medium">{n.node_name}</span>
                            <span className="text-sm font-medium">
                              {n.raw_total_bytes > 0 && n.raw_total_bytes !== n.total_bytes
                                ? `${formatBytes(n.raw_total_bytes)} → ${formatBytes(n.total_bytes)}`
                                : formatBytes(n.total_bytes)}
                            </span>
                          </div>
                          <div className="h-1.5 w-full overflow-hidden rounded-full bg-[hsl(var(--muted))]">
                            <div className="h-full rounded-full bg-blue-500" style={{ width: `${barPct}%` }} />
                          </div>
                          <div className="mt-1 flex gap-3 text-xs text-[hsl(var(--muted-foreground))]">
                            <span>↑ {formatBytes(n.upload_bytes)}</span>
                            <span>↓ {formatBytes(n.download_bytes)}</span>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                </CardContent>
              </Card>
            )}
          </TabsContent>

          {/* ── 工单 ──────────────────────────────────────────────── */}
          {/* ── 节点 ──────────────────────────────────────────────── */}
          <TabsContent value="nodes" className="mt-4">
            <NodeSelectionTab token={token} />
          </TabsContent>

          <TabsContent value="tickets" className="mt-4">
            <TicketsSection token={token} onRepliedCount={setRepliedTicketCount} />
          </TabsContent>

          {/* ── 设置 ──────────────────────────────────────────────── */}
          <TabsContent value="settings" className="mt-4">
            <Card className="border-[hsl(var(--destructive))]/20">
              <CardContent className="pt-6 space-y-3">
                <div className="text-xs font-medium text-[hsl(var(--muted-foreground))] uppercase tracking-wider">
                  重置订阅链接
                </div>
                <p className="text-xs text-[hsl(var(--muted-foreground))]">
                  重置后订阅链接和所有代理凭据（UUID、密码）将同时更换，当前链接与客户端配置立即失效。
                </p>
                <Button
                  variant="outline"
                  size="sm"
                  className="w-full text-[hsl(var(--destructive))] border-[hsl(var(--destructive))]/30 hover:bg-[hsl(var(--destructive))]/10"
                  disabled={resetting}
                  onClick={handleResetToken}
                >
                  {resetting ? "重置中…" : "重置订阅 Token"}
                </Button>
              </CardContent>
            </Card>
          </TabsContent>
        </Tabs>
      </div>

      <ConfirmDialog
        open={resetTrafficConfirmOpen}
        onOpenChange={setResetTrafficConfirmOpen}
        title="确认重置流量"
        description="重置后，当前累计的上传和下载流量将立即清零，同时账户有效期减少 30 天。此操作无法撤销。"
        confirmLabel="确认重置"
        variant="destructive"
        onConfirm={doResetTraffic}
      />

      <ConfirmDialog
        open={resetTokenConfirmOpen}
        onOpenChange={setResetTokenConfirmOpen}
        title="确认重置订阅 Token"
        description="重置后订阅链接和所有代理凭据（UUID、密码）将同时更换，所有客户端需重新导入订阅。"
        confirmLabel="确认重置"
        variant="destructive"
        onConfirm={doResetToken}
      />
    </PageShell>
  );
}

/* ── Page shell (shared header/footer for portal) ─────────────── */

function PageShell({ children }: { children: React.ReactNode }) {
  const [theme, setTheme] = useState<Theme>(getTheme);
  // /user/:token — 从当前 URL 路径提取 token
  const portalToken = window.location.pathname.split("/user/")[1]?.split("/")[0] ?? "";
  return (
    <div className="h-screen overflow-y-auto bg-[hsl(var(--background))] text-[hsl(var(--foreground))]">
      {/* Header */}
      <header className="sticky top-0 z-10 border-b border-[hsl(var(--border))] bg-[hsl(var(--card))]">
        <div className="mx-auto flex h-16 max-w-3xl items-center gap-3 px-4 sm:px-6">
          <PulseLogo />
          <span className="text-xl font-bold tracking-tight">Pulse</span>
          <span className="text-sm text-[hsl(var(--muted-foreground))]">—</span>
          <span className="text-sm font-medium text-[hsl(var(--muted-foreground))]">
            用户面板
          </span>
          <div className="ml-auto flex items-center gap-2">
            <a
              href="/stat"
              className="text-sm text-[hsl(var(--muted-foreground))] hover:text-[hsl(var(--foreground))] transition-colors"
            >
              服务状态
            </a>
            <a
              href={portalToken ? `/shop?sub_token=${encodeURIComponent(portalToken)}` : "/shop"}
              className="text-sm text-[hsl(var(--muted-foreground))] hover:text-[hsl(var(--foreground))] transition-colors"
            >
              商店
            </a>
            <button
              onClick={() => setTheme(toggleTheme())}
              className="rounded-md p-2 text-[hsl(var(--muted-foreground))] hover:bg-[hsl(var(--accent))] hover:text-[hsl(var(--accent-foreground))] transition-colors"
              title={theme === "dark" ? "切换浅色模式" : "切换深色模式"}
            >
              {theme === "dark" ? (
                <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364-6.364l-.707.707M6.343 17.657l-.707.707M17.657 17.657l-.707-.707M6.343 6.343l-.707-.707M12 8a4 4 0 100 8 4 4 0 000-8z" />
                </svg>
              ) : (
                <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M20.354 15.354A9 9 0 018.646 3.646 9.003 9.003 0 0012 21a9.003 9.003 0 008.354-5.646z" />
                </svg>
              )}
            </button>
          </div>
        </div>
      </header>

      {/* Main */}
      <main className="mx-auto max-w-3xl px-4 py-8 sm:px-6">{children}</main>

      {/* Footer */}
      <footer className="border-t border-[hsl(var(--border))] py-6 text-center text-xs text-[hsl(var(--muted-foreground))]">
        Powered by Pulse
      </footer>
    </div>
  );
}

/* ── Inline icons ─────────────────────────────────────────────── */

function ClockIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      {...props}
    >
      <circle cx="12" cy="12" r="10" />
      <polyline points="12 6 12 12 16 14" />
    </svg>
  );
}

function ServerIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      {...props}
    >
      <rect x="2" y="2" width="20" height="8" rx="2" ry="2" />
      <rect x="2" y="14" width="20" height="8" rx="2" ry="2" />
      <line x1="6" y1="6" x2="6.01" y2="6" />
      <line x1="6" y1="18" x2="6.01" y2="18" />
    </svg>
  );
}

function MegaphoneIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      {...props}
    >
      <path d="M3 11l18-5v12L3 13v-2z" />
      <path d="M11.6 16.8a3 3 0 1 1-5.8-1.6" />
    </svg>
  );
}

/* ── Announcement card with Markdown rendering ─────────────────── */

/* ── Announcements section ─────────────────────────────────────── */

type Ann = { id: string; title: string; content: string; enabled: boolean; created_at: string };

function AnnouncementsSection({ announcements }: { announcements: Ann[] }) {
  const [open, setOpen] = useState(false);
  const active = announcements.find((a) => a.enabled);
  const history = announcements.filter((a) => !a.enabled);

  if (!active && history.length === 0) return null;

  return (
    <>
      {/* 激活的公告直接展示 */}
      {active && <AnnouncementCard ann={active} defaultOpen />}

      {/* 历史公告入口 */}
      {history.length > 0 && (
        <button
          onClick={() => setOpen(true)}
          className="flex items-center gap-1.5 text-xs text-[hsl(var(--muted-foreground))] hover:text-[hsl(var(--foreground))] transition-colors"
        >
          <svg xmlns="http://www.w3.org/2000/svg" className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          历史公告（{history.length}）
        </button>
      )}

      {/* 历史弹窗 */}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-lg max-h-[80vh] overflow-y-auto gap-3">
          <DialogHeader className="pb-0">
            <DialogTitle>历史公告</DialogTitle>
          </DialogHeader>
          <div className="space-y-2">
            {history.map((ann) => (
              <AnnouncementCard key={ann.id} ann={ann} />
            ))}
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}

/* ── Tickets section ──────────────────────────────────────────── */

const TICKET_STATUS_LABEL: Record<string, string> = { open: "待处理", replied: "已回复", closed: "已关闭" };
const TICKET_STATUS_COLOR: Record<string, string> = {
  open: "bg-blue-500/15 text-blue-600 border-blue-500/25",
  replied: "bg-emerald-500/15 text-emerald-600 border-emerald-500/25",
  closed: "bg-zinc-500/15 text-zinc-500 border-zinc-500/25",
};

function TicketsSection({ token, onRepliedCount }: { token: string; onRepliedCount?: (n: number) => void }) {
  const [theme, setTheme] = useState<Theme>(getTheme);
  const [tickets, setTickets] = useState<PortalTicket[]>([]);
  const [loading, setLoading] = useState(true);
  const [createOpen, setCreateOpen] = useState(false);
  const [detailTicket, setDetailTicket] = useState<PortalTicket | null>(null);
  const [messages, setMessages] = useState<PortalMessage[]>([]);
  const [images, setImages] = useState<PortalImage[]>([]);
  const [newTitle, setNewTitle] = useState("");
  const [newContent, setNewContent] = useState("");
  const [pendingFiles, setPendingFiles] = useState<File[]>([]);
  const [creating, setCreating] = useState(false);
  const [replyContent, setReplyContent] = useState("");
  const createFileInputRef = useRef<HTMLInputElement>(null);
  const [replying, setReplying] = useState(false);
  const [uploading, setUploading] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const messagesEndRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    fetch(`/v1/portal/${token}/tickets`)
      .then((r) => r.ok ? r.json() : { tickets: [] })
      .then((data) => {
        const list: PortalTicket[] = data.tickets ?? [];
        setTickets(list);
        onRepliedCount?.(list.filter((t) => t.status === "replied").length);
      })
      .finally(() => setLoading(false));
  }, [token]);

  function openDetail(t: PortalTicket) {
    setDetailTicket(t);
    setReplyContent("");
    fetch(`/v1/portal/${token}/tickets/${t.id}`)
      .then((r) => r.json())
      .then((data) => {
        setDetailTicket(data.ticket);
        setMessages(data.messages ?? []);
        setImages(data.images ?? []);
      });
  }

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  async function createTicket() {
    if (!newTitle.trim() || !newContent.trim()) return;
    setCreating(true);
    try {
      const res = await fetch(`/v1/portal/${token}/tickets`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ title: newTitle, content: newContent }),
      });
      if (!res.ok) throw new Error("failed");
      const t: PortalTicket = await res.json();

      // 上传待传图片，成功后自动发一条图片回复
      if (pendingFiles.length > 0) {
        const links: string[] = [];
        for (const file of pendingFiles) {
          const form = new FormData();
          form.append("file", file);
          const imgRes = await fetch(`/v1/portal/${token}/tickets/${t.id}/images`, {
            method: "POST",
            body: form,
          });
          if (imgRes.ok) {
            const img: PortalImage = await imgRes.json();
            links.push(`![${img.filename}](/v1/uploads/tickets/${img.stored_name})`);
          }
        }
        if (links.length > 0) {
          await fetch(`/v1/portal/${token}/tickets/${t.id}/reply`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ content: links.join("\n") }),
          });
        }
      }

      setTickets((prev) => [t, ...prev]);
      setNewTitle("");
      setNewContent("");
      setPendingFiles([]);
      setCreateOpen(false);
      toast("工单已提交", "success");
    } catch {
      toast("提交失败", "error");
    } finally {
      setCreating(false);
    }
  }

  async function sendReply() {
    if (!detailTicket || !replyContent.trim()) return;
    setReplying(true);
    try {
      const res = await fetch(`/v1/portal/${token}/tickets/${detailTicket.id}/reply`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ content: replyContent }),
      });
      if (!res.ok) throw new Error("failed");
      const msg: PortalMessage = await res.json();
      setMessages((prev) => [...prev, msg]);
      setReplyContent("");
      setDetailTicket((t) => t ? { ...t, status: "open" } : t);
      setTickets((prev) =>
        prev.map((t) => t.id === detailTicket.id ? { ...t, status: "open", updated_at: new Date().toISOString() } : t)
      );
    } catch {
      toast("回复失败", "error");
    } finally {
      setReplying(false);
    }
  }

  async function uploadImage(file: File) {
    if (!detailTicket) return;
    setUploading(true);
    try {
      const form = new FormData();
      form.append("file", file);
      const res = await fetch(`/v1/portal/${token}/tickets/${detailTicket.id}/images`, {
        method: "POST",
        body: form,
      });
      if (!res.ok) throw new Error("upload failed");
      const img: PortalImage = await res.json();
      setImages((prev) => [...prev, img]);
      setReplyContent((prev) =>
        prev + (prev ? "\n" : "") + `![${img.filename}](/v1/uploads/tickets/${img.stored_name})`
      );
    } catch {
      toast("上传失败", "error");
    } finally {
      setUploading(false);
    }
  }

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center justify-between text-lg">
            <span>我的工单</span>
            <Button size="sm" onClick={() => setCreateOpen(true)}>提交工单</Button>
          </CardTitle>
        </CardHeader>
        <CardContent>
          {loading ? (
            <div className="space-y-2">
              {[1, 2].map((i) => <div key={i} className="h-10 animate-pulse rounded-lg bg-[hsl(var(--muted))]" />)}
            </div>
          ) : tickets.length === 0 ? (
            <p className="text-center text-sm text-[hsl(var(--muted-foreground))] py-4">暂无工单</p>
          ) : (
            <div className="space-y-2">
              {tickets.map((t) => (
                <button
                  key={t.id}
                  onClick={() => openDetail(t)}
                  className="w-full rounded-lg border border-[hsl(var(--border))] p-3 text-left hover:bg-[hsl(var(--accent))] transition-colors"
                >
                  <div className="flex items-center justify-between gap-2">
                    <div className="flex items-center gap-2 min-w-0">
                      <Badge variant="outline" className={TICKET_STATUS_COLOR[t.status]}>
                        {TICKET_STATUS_LABEL[t.status]}
                      </Badge>
                      <span className="text-sm font-medium truncate">{t.title}</span>
                    </div>
                    <span className="text-xs text-[hsl(var(--muted-foreground))] shrink-0">
                      {new Date(t.updated_at).toLocaleString("zh-CN")}
                    </span>
                  </div>
                </button>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      {/* 创建工单 Dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>提交工单</DialogTitle>
          </DialogHeader>
          <div className="space-y-3" data-color-mode={theme === "dark" ? "dark" : "light"}>
            <Input
              value={newTitle}
              onChange={(e) => setNewTitle(e.target.value)}
              placeholder="标题"
            />
            <MDEditor
              value={newContent}
              onChange={(v) => setNewContent(v ?? "")}
              height={220}
              preview="edit"
              visibleDragbar={false}
              commands={[
                commands.bold, commands.italic, commands.strikethrough,
                commands.divider,
                commands.quote, commands.code,
                commands.divider,
                commands.link,
                commands.divider,
                {
                  name: "upload-image",
                  keyCommand: "upload-image",
                  buttonProps: { "aria-label": "上传图片", title: "上传图片" },
                  icon: (
                    <svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor">
                      <path d="M6.002 5.5a1.5 1.5 0 1 1-3 0 1.5 1.5 0 0 1 3 0Z" />
                      <path d="M1.5 2A1.5 1.5 0 0 0 0 3.5v9A1.5 1.5 0 0 0 1.5 14h13a1.5 1.5 0 0 0 1.5-1.5v-9A1.5 1.5 0 0 0 14.5 2h-13Zm13 1a.5.5 0 0 1 .5.5v6l-3.775-1.947a.5.5 0 0 0-.577.093l-3.71 3.71-2.66-1.772a.5.5 0 0 0-.63.062L1.002 12v.54A.505.505 0 0 1 1 12.5v-9a.5.5 0 0 1 .5-.5h13Z" />
                    </svg>
                  ),
                  execute: () => { createFileInputRef.current?.click(); },
                },
              ]}
              extraCommands={[]}
            />
            <input
              ref={createFileInputRef}
              type="file"
              accept=".jpg,.jpeg,.png,.gif,.webp"
              multiple
              className="hidden"
              onChange={(e) => {
                const files = Array.from(e.target.files ?? []);
                setPendingFiles((prev) => [...prev, ...files]);
                e.target.value = "";
              }}
            />
            {pendingFiles.length > 0 && (
              <div className="flex flex-wrap gap-2">
                {pendingFiles.map((f, i) => (
                  <div key={i} className="relative">
                    <img
                      src={URL.createObjectURL(f)}
                      alt={f.name}
                      className="h-16 w-16 rounded object-cover border border-[hsl(var(--border))]"
                    />
                    <button
                      type="button"
                      onClick={() => setPendingFiles((prev) => prev.filter((_, j) => j !== i))}
                      className="absolute -top-1 -right-1 flex h-4 w-4 items-center justify-center rounded-full bg-[hsl(var(--destructive))] text-[hsl(var(--destructive-foreground))] text-xs leading-none"
                    >
                      ×
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => { setCreateOpen(false); setPendingFiles([]); }}>取消</Button>
            <Button onClick={createTicket} disabled={creating || !newTitle.trim() || !newContent.trim()}>
              {creating ? "提交中…" : "提交"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 工单详情 Dialog */}
      <Dialog open={detailTicket !== null} onOpenChange={(open) => { if (!open) setDetailTicket(null); }}>
        <DialogContent className="max-w-2xl max-h-[85vh] overflow-y-auto">
          {detailTicket && (
            <>
              <DialogHeader>
                <DialogTitle className="flex items-center gap-2">
                  <span className="truncate">{detailTicket.title}</span>
                  <Badge variant="outline" className={TICKET_STATUS_COLOR[detailTicket.status]}>
                    {TICKET_STATUS_LABEL[detailTicket.status]}
                  </Badge>
                </DialogTitle>
              </DialogHeader>
              <Separator />

              {/* 消息对话流 */}
              <ScrollArea className="max-h-[40vh]">
                <div className="space-y-3">
                {messages.map((m) => (
                  <div key={m.id} className={`flex ${m.is_admin ? "justify-end" : "justify-start"}`}>
                    <div className={`max-w-[80%] rounded-lg px-3 py-2 ${
                      m.is_admin
                        ? "bg-[hsl(var(--primary))] text-[hsl(var(--primary-foreground))]"
                        : "bg-[hsl(var(--muted))]"
                    }`}>
                      <div className="flex items-center gap-2 mb-1">
                        <span className="text-xs font-medium">{m.is_admin ? "管理员" : "我"}</span>
                        <span className="text-xs opacity-60">{new Date(m.created_at).toLocaleString("zh-CN")}</span>
                      </div>
                      <div
                        className="prose prose-sm max-w-none dark:prose-invert [&_img]:max-w-full [&_img]:max-h-48 [&_img]:rounded [&_img]:object-contain"
                        dangerouslySetInnerHTML={{ __html: marked.parse(m.content) as string }}
                      />
                    </div>
                  </div>
                ))}
                <div ref={messagesEndRef} />
                </div>
              </ScrollArea>

              {/* 回复框 */}
              {detailTicket.status !== "closed" && (
                <>
                  <Separator />
                  <div className="space-y-2" data-color-mode={theme === "dark" ? "dark" : "light"}>
                    <MDEditor
                      value={replyContent}
                      onChange={(v) => setReplyContent(v ?? "")}
                      height={160}
                      preview="edit"
                      visibleDragbar={false}
                      commands={[
                        commands.bold, commands.italic, commands.strikethrough,
                        commands.divider,
                        commands.quote, commands.code,
                        commands.divider,
                        commands.link,
                        commands.divider,
                        {
                          name: "upload-image",
                          keyCommand: "upload-image",
                          buttonProps: { "aria-label": "上传图片", title: "上传图片" },
                          icon: (
                            <svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor">
                              <path d="M6.002 5.5a1.5 1.5 0 1 1-3 0 1.5 1.5 0 0 1 3 0Z" />
                              <path d="M1.5 2A1.5 1.5 0 0 0 0 3.5v9A1.5 1.5 0 0 0 1.5 14h13a1.5 1.5 0 0 0 1.5-1.5v-9A1.5 1.5 0 0 0 14.5 2h-13Zm13 1a.5.5 0 0 1 .5.5v6l-3.775-1.947a.5.5 0 0 0-.577.093l-3.71 3.71-2.66-1.772a.5.5 0 0 0-.63.062L1.002 12v.54A.505.505 0 0 1 1 12.5v-9a.5.5 0 0 1 .5-.5h13Z" />
                            </svg>
                          ),
                          execute: () => { fileInputRef.current?.click(); },
                        },
                      ]}
                      extraCommands={[]}
                    />
                    <input
                      ref={fileInputRef}
                      type="file"
                      accept=".jpg,.jpeg,.png,.gif,.webp"
                      className="hidden"
                      onChange={(e) => {
                        const f = e.target.files?.[0];
                        if (f) uploadImage(f);
                        e.target.value = "";
                      }}
                    />
                    <div className="flex gap-2">
                      <Button size="sm" onClick={sendReply} disabled={replying || !replyContent.trim()}>
                        {replying ? "发送中…" : "发送"}
                      </Button>
                    </div>
                  </div>
                </>
              )}
            </>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}

function AnnouncementCard({ ann, defaultOpen = false }: {
  ann: { id: string; title: string; content: string; enabled: boolean; created_at: string };
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
  const html = useMemo(() => {
    const renderer = new marked.Renderer();
    renderer.link = ({ href, title, text }) =>
      `<a href="${href}" target="_blank" rel="noopener noreferrer"${title ? ` title="${title}"` : ""}>${text}</a>`;
    return marked.parse(ann.content || "", { renderer }) as string;
  }, [ann.content]);
  return (
    <Card className={ann.enabled ? "border-amber-500/30 bg-amber-500/5" : ""}>
      <CardHeader
        className="px-4 py-3 cursor-pointer select-none"
        onClick={() => setOpen((v) => !v)}
      >
        <CardTitle className="flex items-center gap-2 text-sm font-medium">
          <MegaphoneIcon className={`h-4 w-4 shrink-0 ${ann.enabled ? "text-amber-500" : "text-[hsl(var(--muted-foreground))]"}`} />
          <span className="flex-1">{ann.title || "公告"}</span>
          <span className="text-xs font-normal text-[hsl(var(--muted-foreground))]">
            {new Date(ann.created_at).toLocaleDateString("zh-CN")}
          </span>
          <svg
            xmlns="http://www.w3.org/2000/svg"
            className={`h-3.5 w-3.5 shrink-0 text-[hsl(var(--muted-foreground))] transition-transform duration-200 ${open ? "rotate-180" : ""}`}
            fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}
          >
            <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
          </svg>
        </CardTitle>
      </CardHeader>
      {open && ann.content && (
        <CardContent className="px-4 pb-3 pt-0">
          <div
            className="prose prose-sm dark:prose-invert max-w-none text-[hsl(var(--muted-foreground))]"
            dangerouslySetInnerHTML={{ __html: html }}
          />
        </CardContent>
      )}
    </Card>
  );
}
