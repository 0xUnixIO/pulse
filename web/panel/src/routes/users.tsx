import { useEffect, useState, useCallback, useRef } from "react";
import {
  Card,
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
  Button,
  Input,
  Label,
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  DialogClose,
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
  MultiSelect,
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  ConfirmDialog,
  Popover,
  PopoverTrigger,
  PopoverContent,
  DatePicker,
  toast,
} from "@/components/ui";
import { ScrollArea } from "@/components/ui/scroll-area";
import { api } from "@/lib/api";
import { copyText } from "@/lib/clipboard";
import { useAuthErrorHandler } from "@/hooks/useAuthErrorHandler";
import { formatBytes, hostSubName } from "@/lib/format";
import type {
  User,
  UsersResponse,
  UserStatus,
  ResetStrategy,
  Inbound,
  InboundsResponse,
  Node,
  NodesResponse,
  SubAccessLog,
  SubAccessLogsResponse,
  Outbound,
  OutboundsResponse,
  Host,
  HostsResponse,
  UserGroup,
  UserGroupsResponse,
  Plan,
  PlansResponse,
} from "@/lib/types";

// ── Constants ────────────────────────────────────────────────────


const STATUS_OPTIONS: { value: string; label: string }[] = [
  { value: "all", label: "全部状态" },
  { value: "active", label: "活跃" },
  { value: "disabled", label: "已禁用" },
  { value: "limited", label: "流量耗尽" },
  { value: "expired", label: "已过期" },
  { value: "on_hold", label: "暂停" },
];

const STATUS_LABEL: Record<UserStatus, string> = {
  active: "活跃",
  disabled: "已禁用",
  limited: "流量耗尽",
  expired: "已过期",
  on_hold: "暂停",
};

const STATUS_DOT: Record<UserStatus, string> = {
  active: "bg-green-500",
  disabled: "bg-[hsl(var(--muted-foreground))]",
  limited: "bg-red-500",
  expired: "bg-orange-500",
  on_hold: "bg-yellow-500",
};

const RESET_STRATEGY_OPTIONS: { value: ResetStrategy; label: string }[] = [
  { value: "no_reset", label: "不重置" },
  { value: "day", label: "每天" },
  { value: "week", label: "每周" },
  { value: "month", label: "每月" },
  { value: "year", label: "每年" },
];

const RESET_STRATEGY_LABEL: Record<ResetStrategy, string> = {
  no_reset: "不重置",
  day: "每天",
  week: "每周",
  month: "每月",
  year: "每年",
};

// ── Helpers ──────────────────────────────────────────────────────

function gbToBytes(gb: number): number {
  return Math.round(gb * 1024 * 1024 * 1024);
}

function bytesToGb(bytes: number): number {
  return bytes / (1024 * 1024 * 1024);
}

/** Extract YYYY-MM-DD from ISO string for date input */
function isoToDateInput(iso?: string): string {
  if (!iso) return "";
  return iso.slice(0, 10);
}

/** Convert YYYY-MM-DD to ISO string (start of day UTC) */
function dateInputToIso(date: string): string {
  return new Date(date + "T00:00:00Z").toISOString();
}

function formatExpireDate(iso?: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return d.toLocaleDateString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  });
}

function formatOnlineAt(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const minutes = Math.floor(diff / 60000);
  if (minutes < 1) return "刚刚";
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days} 天前`;
  return new Date(iso).toLocaleDateString("zh-CN", { month: "2-digit", day: "2-digit" });
}

/** 根据重置策略和参考时间计算下次重置时间，no_reset 返回 null */
function calcNextResetDate(strategy: ResetStrategy, createdAt: string, lastResetAt?: string): Date | null {
  if (strategy === "no_reset") return null;
  const ref = new Date(lastResetAt ?? createdAt);
  switch (strategy) {
    case "day":   return new Date(ref.getTime() + 24 * 60 * 60 * 1000);
    case "week":  return new Date(ref.getTime() + 7 * 24 * 60 * 60 * 1000);
    case "month": {
      const d = new Date(ref);
      d.setMonth(d.getMonth() + 1);
      return d;
    }
    case "year": {
      const d = new Date(ref);
      d.setFullYear(d.getFullYear() + 1);
      return d;
    }
    default: return null;
  }
}

function generateHexToken(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

// 去掉易混淆字符：0/O/1/I/l
function generatePassword(): string {
  const chars = "ABCDEFGHJKMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789!@#$%^&*";
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => chars[b % chars.length]).join("");
}

interface UserInboundAccess {
  id: string;
  user_id: string;
  inbound_id: string;
  node_id: string;
}

interface UserInboundsResponse {
  inbounds: UserInboundAccess[];
  total: number;
}

// ── 创建用户后暂存凭证（密码仅此时可知）───────────────────────────
type PortalCreds = { userId: string; password: string };

// ── Main Component ───────────────────────────────────────────────

export default function UsersPage() {
  // ── List state ───────────────────────────────────────────────
  const [users, setUsers] = useState<User[]>([]);
  const [total, setTotal] = useState(0);
  const [search, setSearch] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState("all");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  // ── Dialog state ─────────────────────────────────────────────
  const [createOpen, setCreateOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [resetTrafficUser, setResetTrafficUser] = useState<User | null>(null);
  const [editingUser, setEditingUser] = useState<User | null>(null);
  const [deletingUser, setDeletingUser] = useState<User | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState("");

  // ── Create form state ────────────────────────────────────────
  const [createUsername, setCreateUsername] = useState("");
  const [createPassword, setCreatePassword] = useState("");
  const [createPlanId, setCreatePlanId] = useState("");
  const [createTrafficGb, setCreateTrafficGb] = useState("");
  const [createExpireAt, setCreateExpireAt] = useState("");
  const [createResetStrategy, setCreateResetStrategy] = useState<ResetStrategy>("no_reset");
  const [createNote, setCreateNote] = useState("");
  const [createInboundIds, setCreateInboundIds] = useState<string[]>([]);

  // ── Plans & user groups (for create dialog plan selector) ────
  const [allPlans, setAllPlans] = useState<Plan[]>([]);
  const [allUserGroups, setAllUserGroups] = useState<UserGroup[]>([]);

  // ── Edit form state ──────────────────────────────────────────
  const [editStatus, setEditStatus] = useState<UserStatus>("active");
  const [editTrafficGb, setEditTrafficGb] = useState("");
  const [editPlanTrafficGb, setEditPlanTrafficGb] = useState("");
  const [editExpireAt, setEditExpireAt] = useState("");
  const [editResetStrategy, setEditResetStrategy] = useState<ResetStrategy>("no_reset");
  const [editNote, setEditNote] = useState("");
  const [editSubToken, setEditSubToken] = useState("");
  const [editOnHoldExpireAt, setEditOnHoldExpireAt] = useState("");
  const [editLastResetAt, setEditLastResetAt] = useState("");
  const [editInboundIds, setEditInboundIds] = useState<string[]>([]);
  // 凭证显示/重置状态
  const [showCredentials, setShowCredentials] = useState(false);
  const [resettingCredentials, setResettingCredentials] = useState(false);
  // 门户密码（空字符串 = 不变，非空 = 覆盖；clearPassword = true 时清除）
  const [editPassword, setEditPassword] = useState("");
  const [clearPassword, setClearPassword] = useState(false);

  // ── 创建后暂存凭证 state ──────────────────────────────────────
  const [portalCreds, setPortalCreds] = useState<PortalCreds | null>(null);

  // ── 账户信息 dialog state ─────────────────────────────────────
  const [accountInfoUser, setAccountInfoUser] = useState<User | null>(null);

  // ── Copy sub link state ────────────────────────────────────
  const [copiedUserId, setCopiedUserId] = useState<string | null>(null);

  // ── Sub logs dialog state ───────────────────────────────────
  const [subLogsOpen, setSubLogsOpen] = useState(false);
  const [subLogsUserId, setSubLogsUserId] = useState<string | null>(null);

  // ── Node usage dialog state ────────────────────────────────
  const [nodeUsageOpen, setNodeUsageOpen] = useState(false);
  const [nodeUsageUserId, setNodeUsageUserId] = useState<string | null>(null);

  // ── Sub links dialog state ─────────────────────────────────
  const [subLinksOpen, setSubLinksOpen] = useState(false);
  const [subLinksUser, setSubLinksUser] = useState<User | null>(null);

  // ── 用户组 dialog state ────────────────────────────────────
  const [userGroupsDialogOpen, setUserGroupsDialogOpen] = useState(false);
  const [userGroupsDialogUser, setUserGroupsDialogUser] = useState<User | null>(null);

  // ── Inbound state (shared by create/edit dialogs) ───────────
  const [allInbounds, setAllInbounds] = useState<Inbound[]>([]);
  const [allNodes, setAllNodes] = useState<Node[]>([]);
  const [allHosts, setAllHosts] = useState<Host[]>([]);
  const [allOutbounds, setAllOutbounds] = useState<Outbound[]>([]);
  const [inboundsLoading, setInboundsLoading] = useState(false);

  // ── Debounce search ──────────────────────────────────────────
  const debounceTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  // 用 ref 而非 state 做提交锁：state 更新是异步的，双击时第二次 click
  // 可能在 React 刷新 disabled 属性之前就触发，导致重复提交。
  const createLock = useRef(false);

  const handleSearchChange = useCallback((value: string) => {
    setSearch(value);
    if (debounceTimer.current) clearTimeout(debounceTimer.current);
    debounceTimer.current = setTimeout(() => {
      setDebouncedSearch(value);
    }, 300);
  }, []);

  useEffect(() => {
    return () => {
      if (debounceTimer.current) clearTimeout(debounceTimer.current);
    };
  }, []);

  // ── Auth error handler ───────────────────────────────────────
  const handleAuthError = useAuthErrorHandler();

  // ── Fetch users ──────────────────────────────────────────────
  const fetchUsers = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const params = new URLSearchParams();
      if (debouncedSearch) params.set("search", debouncedSearch);
      if (statusFilter !== "all") params.set("status", statusFilter);

      const qs = params.toString();
      const data = await api.get<UsersResponse>(`/users?${qs}`);
      setUsers(data.users ?? []);
      setTotal(data.total ?? 0);
    } catch (err) {
      if (!handleAuthError(err)) {
        setError(err instanceof Error ? err.message : "加载用户列表失败");
      }
    } finally {
      setLoading(false);
    }
  }, [debouncedSearch, statusFilter, handleAuthError]);

  useEffect(() => {
    fetchUsers();
  }, [fetchUsers]);

  // ── Fetch all inbounds (for dialog checkboxes) ──────────────
  const fetchAllInbounds = useCallback(async () => {
    setInboundsLoading(true);
    try {
      const [inboundsData, nodesData, outboundsData, hostsData, plansData, userGroupsData] = await Promise.all([
        api.get<InboundsResponse>("/inbounds"),
        api.get<NodesResponse>("/nodes"),
        api.get<OutboundsResponse>("/outbounds").catch(() => ({ outbounds: [] }) as OutboundsResponse),
        api.get<HostsResponse>("/hosts").catch(() => ({ hosts: [] }) as HostsResponse),
        api.get<PlansResponse>("/plans").catch(() => ({ plans: [] }) as PlansResponse),
        api.get<UserGroupsResponse>("/user-groups").catch(() => ({ user_groups: [] }) as UserGroupsResponse),
      ]);
      setAllInbounds(inboundsData.inbounds ?? []);
      setAllNodes(nodesData.nodes ?? []);
      setAllOutbounds(outboundsData.outbounds ?? []);
      setAllHosts(hostsData.hosts ?? []);
      setAllPlans(plansData.plans ?? []);
      setAllUserGroups(userGroupsData.user_groups ?? []);
    } catch (err) {
      handleAuthError(err);
    } finally {
      setInboundsLoading(false);
    }
  }, [handleAuthError]);

  // ── Fetch user inbounds ─────────────────────────────────────
  const fetchUserInbounds = useCallback(
    async (userId: string): Promise<string[]> => {
      try {
        const data = await api.get<UserInboundsResponse>(
          `/users/${userId}/inbounds`,
        );
        return (data.inbounds ?? []).map((ib) => ib.inbound_id);
      } catch (err) {
        handleAuthError(err);
        return [];
      }
    },
    [handleAuthError],
  );

  // ── Status filter change ─────────────────────────────────────
  const handleStatusFilterChange = (value: string) => {
    setStatusFilter(value);
  };

  // ── Create user ──────────────────────────────────────────────
  const resetCreateForm = () => {
    const in30Days = new Date();
    in30Days.setDate(in30Days.getDate() + 30);
    setCreateUsername("");
    setCreatePassword("");
    setCreatePlanId("");
    setCreateTrafficGb("100");
    setCreateExpireAt(in30Days.toISOString().slice(0, 10));
    setCreateResetStrategy("month");
    setCreateNote("");
    setCreateInboundIds([]);
    setFormError("");
  };

  const openCreateDialog = () => {
    resetCreateForm();
    fetchAllInbounds();
    setCreateOpen(true);
  };

  const applyPlan = (planId: string) => {
    setCreatePlanId(planId);
    if (!planId) return;
    const plan = allPlans.find((p) => p.id === planId);
    if (!plan) return;
    if (plan.traffic_limit > 0) {
      setCreateTrafficGb(String(parseFloat(bytesToGb(plan.traffic_limit).toFixed(2))));
    }
    if (plan.duration_days > 0) {
      const expiry = new Date();
      expiry.setDate(expiry.getDate() + plan.duration_days);
      setCreateExpireAt(expiry.toISOString().slice(0, 10));
    }
    if (plan.data_limit_reset_strategy) {
      setCreateResetStrategy(plan.data_limit_reset_strategy as ResetStrategy);
    }
    if (plan.user_group_ids) {
      const groupIds = plan.user_group_ids.split(",").map((s) => s.trim()).filter(Boolean);
      const ibIds = new Set<string>();
      for (const gid of groupIds) {
        const group = allUserGroups.find((g) => g.id === gid);
        if (group?.inbound_ids) {
          group.inbound_ids.split(",").map((s) => s.trim()).filter(Boolean).forEach((id) => ibIds.add(id));
        }
      }
      if (ibIds.size > 0) setCreateInboundIds([...ibIds]);
    }
  };

  const handleCreate = async () => {
    if (createLock.current) return;
    createLock.current = true;

    const username = createUsername.trim();
    if (!username) {
      createLock.current = false;
      setFormError("用户名不能为空");
      return;
    }

    setSubmitting(true);
    setFormError("");
    try {
      const body: Record<string, unknown> = { username };
      const trafficGb = parseFloat(createTrafficGb);
      if (createTrafficGb && !isNaN(trafficGb) && trafficGb > 0) {
        body.traffic_limit_bytes = gbToBytes(trafficGb);
      }
      if (createExpireAt) {
        body.expire_at = dateInputToIso(createExpireAt);
      }
      if (createResetStrategy !== "no_reset") {
        body.data_limit_reset_strategy = createResetStrategy;
      }
      if (createNote.trim()) {
        body.note = createNote.trim();
      }
      if (createInboundIds.length > 0) {
        body.inbound_ids = createInboundIds;
      }
      if (createPassword.trim()) {
        body.password = createPassword.trim();
      }

      const createdUser = await api.post<User>("/users", body);
      // 创建成功后暂存凭证（密码仅此时可知）
      if (createPassword.trim()) {
        setPortalCreds({ userId: createdUser.id, password: createPassword.trim() });
      }
      setCreateOpen(false);
      fetchUsers();
    } catch (err) {
      if (!handleAuthError(err)) {
        setFormError(err instanceof Error ? err.message : "创建用户失败");
      }
    } finally {
      createLock.current = false;
      setSubmitting(false);
    }
  };

  // ── Edit user ────────────────────────────────────────────────
  const openEditDialog = (user: User) => {
    setEditingUser(user);
    setEditStatus(user.status);
    setEditTrafficGb(
      user.traffic_limit_bytes > 0
        ? String(parseFloat(bytesToGb(user.traffic_limit_bytes).toFixed(2)))
        : "",
    );
    setEditPlanTrafficGb(
      user.plan_traffic_limit_bytes > 0 && user.plan_traffic_limit_bytes !== user.traffic_limit_bytes
        ? String(parseFloat(bytesToGb(user.plan_traffic_limit_bytes).toFixed(2)))
        : "",
    );
    setEditExpireAt(isoToDateInput(user.expire_at));
    setEditResetStrategy(user.data_limit_reset_strategy);
    setEditNote(user.note ?? "");
    setEditSubToken(user.sub_token ?? "");
    setEditOnHoldExpireAt(isoToDateInput(user.on_hold_expire_at));
    setEditLastResetAt(isoToDateInput(user.last_traffic_reset_at));
    setEditInboundIds([]);
    setShowCredentials(false);
    setEditPassword("");
    setClearPassword(false);
    setFormError("");
    setEditOpen(true);
    // Fetch inbounds in parallel
    fetchAllInbounds();
    fetchUserInbounds(user.id).then(setEditInboundIds);
  };

  const handleEdit = async () => {
    if (!editingUser) return;

    setSubmitting(true);
    setFormError("");
    try {
      const body: Record<string, unknown> = {
        status: editStatus,
        data_limit_reset_strategy: editResetStrategy,
        note: editNote,
        inbound_ids: editInboundIds,
      };

      if (editStatus === "on_hold") {
        if (editOnHoldExpireAt) {
          body.on_hold_expire_at = dateInputToIso(editOnHoldExpireAt);
        } else {
          body.clear_on_hold_expire_at = true;
        }
      }

      const trafficGb = parseFloat(editTrafficGb);
      if (editTrafficGb && !isNaN(trafficGb) && trafficGb > 0) {
        body.traffic_limit_bytes = gbToBytes(trafficGb);
      } else {
        body.traffic_limit_bytes = 0;
      }

      const planTrafficGb = parseFloat(editPlanTrafficGb);
      if (editPlanTrafficGb && !isNaN(planTrafficGb) && planTrafficGb > 0) {
        body.plan_traffic_limit_bytes = gbToBytes(planTrafficGb);
      } else if (editingUser && editingUser.plan_traffic_limit_bytes !== editingUser.traffic_limit_bytes) {
        // 用户清空了覆盖字段，重置为与流量限额相同（让后端同步）
        body.plan_traffic_limit_bytes = body.traffic_limit_bytes ?? 0;
      }

      if (editExpireAt) {
        body.expire_at = dateInputToIso(editExpireAt);
      } else {
        body.expire_at = null;
      }

      if (editLastResetAt) {
        body.last_traffic_reset_at = dateInputToIso(editLastResetAt);
      } else {
        body.clear_last_traffic_reset_at = true;
      }

      if (clearPassword) {
        body.password = "";
      } else if (editPassword.trim() !== "") {
        body.password = editPassword.trim();
      }

      await api.put<User>(`/users/${editingUser.id}`, body);
      setEditOpen(false);
      setEditingUser(null);
      fetchUsers();
    } catch (err) {
      if (!handleAuthError(err)) {
        setFormError(err instanceof Error ? err.message : "更新用户失败");
      }
    } finally {
      setSubmitting(false);
    }
  };

  // ── Delete user ──────────────────────────────────────────────
  const openDeleteDialog = (user: User) => {
    setDeletingUser(user);
    setFormError("");
    setDeleteOpen(true);
  };

  const handleDelete = async () => {
    if (!deletingUser) return;

    setSubmitting(true);
    setFormError("");
    try {
      await api.del(`/users/${deletingUser.id}`);
      setDeleteOpen(false);
      setDeletingUser(null);
      fetchUsers();
    } catch (err) {
      if (!handleAuthError(err)) {
        setFormError(err instanceof Error ? err.message : "删除用户失败");
      }
    } finally {
      setSubmitting(false);
    }
  };

  // ── Traffic display ──────────────────────────────────────────
  const renderTraffic = (user: User) => {
    const used = formatBytes(user.used_bytes);
    const limit =
      user.traffic_limit_bytes > 0
        ? formatBytes(user.traffic_limit_bytes)
        : "无限制";
    const rawTotal = user.raw_upload_bytes + user.raw_download_bytes;
    const ratio = user.traffic_limit_bytes > 0 ? user.used_bytes / user.traffic_limit_bytes : 0;
    const trafficColor =
      user.status === "limited" || ratio >= 1
        ? "text-red-500"
        : ratio >= 0.8
          ? "text-orange-500"
          : "";
    return (
      <div className="flex flex-col gap-0.5">
        <span className={trafficColor}>{used} / {limit}</span>
        <span className="text-xs text-[hsl(var(--muted-foreground))] opacity-70">
          实际 {formatBytes(rawTotal)}
        </span>
      </div>
    );
  };

  // ── Copy subscription link ────────────────────────────────────
  const copySubLink = useCallback((user: User) => {
    if (!user.sub_token) return;
    const url = `${window.location.origin}/sub/${user.sub_token}`;
    copyText(url).then(() => {
      setCopiedUserId(user.id);
      setTimeout(() => setCopiedUserId(null), 1500);
    }).catch(() => {});
  }, []);

  // ── Reset traffic ─────────────────────────────────────────────
  const resetTraffic = useCallback((user: User) => {
    setResetTrafficUser(user);
  }, []);

  const doResetTraffic = useCallback(async () => {
    const user = resetTrafficUser;
    setResetTrafficUser(null);
    if (!user) return;
    try {
      await api.post(`/users/${user.id}/reset-traffic`, {});
      fetchUsers();
    } catch (err) {
      if (!handleAuthError(err)) {
        setError(err instanceof Error ? err.message : "重置流量失败");
      }
    }
  }, [resetTrafficUser, fetchUsers, handleAuthError]);

  // ── Open sub links dialog ─────────────────────────────────────
  const openSubLinks = useCallback((user: User) => {
    setSubLinksUser(user);
    setSubLinksOpen(true);
  }, []);

  // ── Open sub logs dialog ──────────────────────────────────────
  const openSubLogs = useCallback((userId: string) => {
    setSubLogsUserId(userId);
    setSubLogsOpen(true);
  }, []);

  // ── Open node usage dialog ─────────────────────────────────────
  const openNodeUsage = useCallback((userId: string) => {
    setNodeUsageUserId(userId);
    setNodeUsageOpen(true);
  }, []);

  // ── Render ───────────────────────────────────────────────────
  return (
    <div className="flex h-full flex-col p-4 sm:p-6 lg:p-8">
      {/* Header */}
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <h1 className="text-2xl font-bold text-[hsl(var(--foreground))]">
          用户管理
        </h1>
        <Button onClick={openCreateDialog}>+ 添加用户</Button>
      </div>

      {/* Filters */}
      <div className="mb-4 flex flex-col gap-3 sm:flex-row sm:items-center">
        <div className="relative w-full sm:max-w-xs">
          <Input
            placeholder="搜索用户名..."
            value={search}
            onChange={(e) => handleSearchChange(e.target.value)}
            className="w-full"
          />
        </div>
        <div className="w-full sm:w-44">
          <Select value={statusFilter} onValueChange={handleStatusFilterChange}>
            <SelectTrigger className="w-full">
              <SelectValue placeholder="全部状态" />
            </SelectTrigger>
            <SelectContent>
              {STATUS_OPTIONS.map((opt) => (
                <SelectItem key={opt.value} value={opt.value}>
                  {opt.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <span className="text-sm text-[hsl(var(--muted-foreground))]">
          共 {total} 个用户
        </span>
      </div>

      {/* Error */}
      {error && (
        <div className="mb-4 rounded-lg border border-[hsl(var(--destructive))] bg-[hsl(var(--destructive))]/10 px-4 py-3 text-sm text-[hsl(var(--destructive))]">
          {error}
          <Button
            variant="ghost"
            size="sm"
            className="ml-2"
            onClick={fetchUsers}
          >
            重试
          </Button>
        </div>
      )}

      {/* Table */}
      <Card className="flex min-h-0 flex-1 flex-col overflow-hidden">
        <Table containerClassName="flex-1 overflow-auto">
          <TableHeader className="sticky top-0 z-10 bg-[hsl(var(--card))]">
            <TableRow>
              <TableHead className="px-4">用户名</TableHead>
              <TableHead className="px-4">状态</TableHead>
              <TableHead className="px-4">流量</TableHead>
              <TableHead className="hidden px-4 sm:table-cell">重置</TableHead>
              <TableHead className="hidden px-4 md:table-cell">到期</TableHead>
              <TableHead className="px-4 text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && users.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={7}
                  className="h-32 text-center text-[hsl(var(--muted-foreground))]"
                >
                  <div className="flex items-center justify-center gap-2">
                    <svg
                      className="h-5 w-5 animate-spin text-[hsl(var(--muted-foreground))]"
                      xmlns="http://www.w3.org/2000/svg"
                      fill="none"
                      viewBox="0 0 24 24"
                    >
                      <circle
                        className="opacity-25"
                        cx="12"
                        cy="12"
                        r="10"
                        stroke="currentColor"
                        strokeWidth="4"
                      />
                      <path
                        className="opacity-75"
                        fill="currentColor"
                        d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
                      />
                    </svg>
                    加载中...
                  </div>
                </TableCell>
              </TableRow>
            ) : !loading && users.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={7}
                  className="h-32 text-center text-[hsl(var(--muted-foreground))]"
                >
                  {debouncedSearch || statusFilter !== "all"
                    ? "没有匹配的用户"
                    : "暂无用户，点击「添加用户」创建第一个用户"}
                </TableCell>
              </TableRow>
            ) : (
              users.map((user) => (
                <TableRow key={user.id}>
                  <TableCell className="px-4 font-medium text-[hsl(var(--foreground))]">
                    <a
                      href={`/user/${user.sub_token}`}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="hover:underline hover:text-[hsl(var(--primary))]"
                    >
                      {user.username}
                    </a>
                  </TableCell>
                  <TableCell className="px-4 whitespace-nowrap">
                    <div className="flex items-center gap-1.5">
                      <span className={`h-2 w-2 shrink-0 rounded-full ${STATUS_DOT[user.status] ?? "bg-[hsl(var(--muted-foreground))]"}`} />
                      <div className="flex flex-col">
                        <span className="text-sm text-[hsl(var(--foreground))]">{STATUS_LABEL[user.status] ?? user.status}</span>
                        {user.online_at && (
                          <span className="text-xs text-[hsl(var(--muted-foreground))]">{formatOnlineAt(user.online_at)}</span>
                        )}
                      </div>
                    </div>
                  </TableCell>
                  <TableCell className="px-4 text-sm text-[hsl(var(--muted-foreground))] whitespace-nowrap">
                    {renderTraffic(user)}
                  </TableCell>
                  <TableCell className="hidden px-4 text-sm text-[hsl(var(--muted-foreground))] sm:table-cell">
                    {RESET_STRATEGY_LABEL[user.data_limit_reset_strategy] ??
                      user.data_limit_reset_strategy}
                  </TableCell>
                  <TableCell className="hidden px-4 text-sm text-[hsl(var(--muted-foreground))] md:table-cell">
                    <div className="flex flex-col gap-0.5">
                      {(() => {
                        const expireColor = (() => {
                          if (!user.expire_at) return "";
                          const ms = new Date(user.expire_at).getTime() - Date.now();
                          if (ms <= 0) return "text-red-500";
                          if (ms <= 7 * 24 * 60 * 60 * 1000) return "text-orange-500";
                          return "";
                        })();
                        return <span className={expireColor}>{formatExpireDate(user.expire_at)}</span>;
                      })()}
                      {(() => {
                        const next = calcNextResetDate(user.data_limit_reset_strategy, user.created_at, user.last_traffic_reset_at);
                        return next ? (
                          <span className="text-xs opacity-60">重置 {formatExpireDate(next.toISOString())}</span>
                        ) : null;
                      })()}
                    </div>
                  </TableCell>
                  <TableCell className="px-4 text-right">
                    <div className="flex justify-end gap-1">
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button variant="ghost" size="sm" className="h-8 w-8 p-0">
                            <span className="sr-only">更多操作</span>
                            <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="h-4 w-4"><circle cx="12" cy="5" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="12" cy="19" r="1"/></svg>
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem onClick={() => setAccountInfoUser(user)}>
                            账户信息
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem
                            onClick={() => copySubLink(user)}
                            disabled={!user.sub_token}
                          >
                            {copiedUserId === user.id ? "已复制" : "复制订阅链接"}
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            onClick={() => openSubLinks(user)}
                            disabled={!user.sub_token}
                          >
                            查看订阅内容
                          </DropdownMenuItem>
                          <DropdownMenuItem onClick={() => { setUserGroupsDialogUser(user); setUserGroupsDialogOpen(true); }}>
                            加入/移出用户组
                          </DropdownMenuItem>
                          <DropdownMenuItem onClick={() => resetTraffic(user)}>
                            重置流量
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem onClick={() => openSubLogs(user.id)}>
                            订阅日志
                          </DropdownMenuItem>
                          <DropdownMenuItem onClick={() => openNodeUsage(user.id)}>
                            节点流量
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-8 px-3"
                        onClick={() => openEditDialog(user)}
                      >
                        编辑
                      </Button>
                      <Button
                        variant="destructive"
                        size="sm"
                        className="h-8 px-3"
                        onClick={() => openDeleteDialog(user)}
                      >
                        删除
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>

      </Card>

      {/* ── Create User Dialog ──────────────────────────────────── */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>添加用户</DialogTitle>
            <DialogDescription>创建一个新的用户账户。</DialogDescription>
          </DialogHeader>

          <ScrollArea className="max-h-[60vh]">
            <div className="grid gap-4 py-2 px-1">
            {/* Plan selector */}
            {allPlans.length > 0 && (
              <div className="grid gap-2">
                <Label>分配套餐</Label>
                <Select value={createPlanId} onValueChange={applyPlan}>
                  <SelectTrigger className="w-full">
                    <SelectValue placeholder="选择套餐自动填入配置…" />
                  </SelectTrigger>
                  <SelectContent>
                    {allPlans.map((p) => (
                      <SelectItem key={p.id} value={p.id}>
                        {p.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            )}

            {/* Username */}
            <div className="grid gap-2">
              <Label htmlFor="create-username">
                用户名 <span className="text-[hsl(var(--destructive))]">*</span>
              </Label>
              <Input
                id="create-username"
                placeholder="输入用户名"
                value={createUsername}
                onChange={(e) => setCreateUsername(e.target.value)}
                autoFocus
              />
            </div>

            {/* Password */}
            <div className="grid gap-2">
              <Label htmlFor="create-password">门户密码</Label>
              <div className="flex gap-2">
                <Input
                  id="create-password"
                  type="text"
                  placeholder="留空则不设置密码"
                  value={createPassword}
                  onChange={(e) => setCreatePassword(e.target.value)}
                  autoComplete="new-password"
                  className="flex-1 font-mono text-sm"
                />
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="shrink-0"
                  onClick={() => setCreatePassword(generatePassword())}
                  title="随机生成密码"
                >
                  随机
                </Button>
              </div>
            </div>

            {/* Traffic limit */}
            <div className="grid gap-2">
              <Label htmlFor="create-traffic">流量限额（GB）</Label>
              <Input
                id="create-traffic"
                type="number"
                min="0"
                step="0.1"
                placeholder="留空或 0 表示无限制"
                value={createTrafficGb}
                onChange={(e) => setCreateTrafficGb(e.target.value)}
              />
            </div>

            {/* Expire date */}
            <div className="grid gap-2">
              <Label>到期时间</Label>
              <DatePicker
                value={createExpireAt}
                onChange={setCreateExpireAt}
                placeholder="不限期"
                fromDate={new Date()}
              />
            </div>

            {/* Reset strategy */}
            <div className="grid gap-2">
              <Label>流量重置策略</Label>
              <Select
                value={createResetStrategy}
                onValueChange={(v) => setCreateResetStrategy(v as ResetStrategy)}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {RESET_STRATEGY_OPTIONS.map((opt) => (
                    <SelectItem key={opt.value} value={opt.value}>
                      {opt.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            {/* Note */}
            <div className="grid gap-2">
              <Label htmlFor="create-note">备注</Label>
              <textarea
                id="create-note"
                placeholder="可选备注信息"
                value={createNote}
                onChange={(e) => setCreateNote(e.target.value)}
                rows={3}
                className="flex w-full rounded-md border border-[hsl(var(--input))] bg-transparent px-3 py-2 text-sm shadow-sm placeholder:text-[hsl(var(--muted-foreground))] focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-[hsl(var(--ring))] disabled:cursor-not-allowed disabled:opacity-50"
              />
            </div>

            {/* Error */}
            {formError && (
              <p className="text-sm text-[hsl(var(--destructive))]">{formError}</p>
            )}
            </div>
          </ScrollArea>

          <DialogFooter>
            <DialogClose asChild>
              <Button variant="outline" disabled={submitting}>
                取消
              </Button>
            </DialogClose>
            <Button onClick={handleCreate} disabled={submitting}>
              {submitting ? "创建中..." : "创建"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* ── Edit User Dialog ────────────────────────────────────── */}
      <Dialog
        open={editOpen}
        onOpenChange={(open) => {
          setEditOpen(open);
          if (!open) setEditingUser(null);
        }}
      >
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>编辑用户</DialogTitle>
            <DialogDescription>
              修改用户{" "}
              <span className="font-medium text-[hsl(var(--foreground))]">
                {editingUser?.username}
              </span>{" "}
              的配置。
            </DialogDescription>
          </DialogHeader>

          <ScrollArea className="max-h-[60vh]">
            <div className="grid gap-4 py-2 px-1">
            {/* Status */}
            <div className="grid gap-2">
              <Label>状态</Label>
              <Select
                value={editStatus}
                onValueChange={(v) => setEditStatus(v as UserStatus)}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {STATUS_OPTIONS.filter((o) => o.value !== "all").map((opt) => (
                    <SelectItem key={opt.value} value={opt.value}>
                      {opt.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            {/* Traffic limit */}
            <div className="grid gap-2">
              <Label htmlFor="edit-traffic">流量限额（GB）</Label>
              <Input
                id="edit-traffic"
                type="number"
                min="0"
                step="0.1"
                placeholder="留空或 0 表示无限制"
                value={editTrafficGb}
                onChange={(e) => setEditTrafficGb(e.target.value)}
              />
              <div className="grid gap-1">
                <Label htmlFor="edit-plan-traffic" className="text-xs text-muted-foreground">
                  重置目标（GB）
                </Label>
                <Input
                  id="edit-plan-traffic"
                  type="number"
                  min="0"
                  step="0.1"
                  placeholder="留空则与流量限额相同"
                  value={editPlanTrafficGb}
                  onChange={(e) => setEditPlanTrafficGb(e.target.value)}
                  className="h-8 text-sm"
                />
                <p className="text-xs text-muted-foreground">
                  叠加后下个周期恢复的基础额度，留空时等同于流量限额
                </p>
              </div>
            </div>

            {/* Expire date */}
            <div className="grid gap-2">
              <Label>到期时间</Label>
              <DatePicker
                value={editExpireAt}
                onChange={setEditExpireAt}
                placeholder="不限期"
              />
            </div>

            {/* Reset strategy */}
            <div className="grid gap-2">
              <Label>流量重置策略</Label>
              <Select
                value={editResetStrategy}
                onValueChange={(v) => setEditResetStrategy(v as ResetStrategy)}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {RESET_STRATEGY_OPTIONS.map((opt) => (
                    <SelectItem key={opt.value} value={opt.value}>
                      {opt.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            {/* Last reset at */}
            {editResetStrategy !== "no_reset" && (
              <div className="grid gap-2">
                <Label>上次重置时间</Label>
                <DatePicker
                  value={editLastResetAt}
                  onChange={setEditLastResetAt}
                  placeholder="选择日期"
                />
                {(() => {
                  const ref = editLastResetAt || (editingUser?.created_at ?? "");
                  const next = ref ? calcNextResetDate(editResetStrategy, ref, editLastResetAt || undefined) : null;
                  return next ? (
                    <p className="text-xs text-[hsl(var(--muted-foreground))]">
                      下次重置：{formatExpireDate(next.toISOString())}
                    </p>
                  ) : null;
                })()}
              </div>
            )}

            {/* Note */}
            <div className="grid gap-2">
              <Label htmlFor="edit-note">备注</Label>
              <textarea
                id="edit-note"
                placeholder="可选备注信息"
                value={editNote}
                onChange={(e) => setEditNote(e.target.value)}
                rows={3}
                className="flex w-full rounded-md border border-[hsl(var(--input))] bg-transparent px-3 py-2 text-sm shadow-sm placeholder:text-[hsl(var(--muted-foreground))] focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-[hsl(var(--ring))] disabled:cursor-not-allowed disabled:opacity-50"
              />
            </div>

            {/* Sub Token */}
            <div className="grid gap-2">
              <Label htmlFor="edit-sub-token">订阅令牌</Label>
              <div className="flex gap-2">
                <Input
                  id="edit-sub-token"
                  value={editSubToken}
                  readOnly
                  className="flex-1 font-mono text-xs"
                />
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="shrink-0"
                  onClick={(e) => {
                    if (editSubToken) {
                      const container = e.currentTarget.closest<HTMLElement>('[role="dialog"]');
                      copyText(editSubToken, container).catch(() => {});
                    }
                  }}
                  title="复制"
                >
                  <svg
                    xmlns="http://www.w3.org/2000/svg"
                    className="h-4 w-4"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  >
                    <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
                    <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
                  </svg>
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="shrink-0"
                  onClick={async () => {
                    if (!editingUser) return;
                    try {
                      const res = await api.post<{ sub_token: string }>(
                        `/users/${editingUser.id}/regenerate-sub-token`,
                        {},
                      );
                      setEditSubToken(res.sub_token);
                      toast("sub_token 已重新生成", "success");
                    } catch (err) {
                      toast(
                        `重生成失败：${err instanceof Error ? err.message : "未知错误"}`,
                        "error",
                      );
                    }
                  }}
                >
                  重新生成
                </Button>
              </div>
            </div>

            {/* 全局凭证 */}
            <div className="grid gap-2">
              <Label>全局凭证</Label>
              <div className="rounded-md border border-[hsl(var(--input))] bg-[hsl(var(--muted)/0.3)] p-3 space-y-2">
                <div className="flex items-center gap-2">
                  <span className="w-14 shrink-0 text-xs text-[hsl(var(--muted-foreground))]">UUID</span>
                  <code className="flex-1 font-mono text-xs break-all">
                    {showCredentials ? (editingUser?.uuid ?? "—") : "••••••••-••••-••••-••••-••••••••••••"}
                  </code>
                </div>
                <div className="flex items-center gap-2">
                  <span className="w-14 shrink-0 text-xs text-[hsl(var(--muted-foreground))]">Secret</span>
                  <code className="flex-1 font-mono text-xs break-all">
                    {showCredentials ? (editingUser?.secret ?? "—") : "••••••••••••••••••••••••••••••••"}
                  </code>
                </div>
              </div>
              <div className="flex gap-2">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setShowCredentials((v) => !v)}
                >
                  {showCredentials ? "隐藏" : "显示"}
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={resettingCredentials}
                  onClick={async () => {
                    if (!editingUser) return;
                    const ok = window.confirm(
                      "重置后所有节点将使用新凭证，现有客户端需重新导入订阅。确认继续？"
                    );
                    if (!ok) return;
                    setResettingCredentials(true);
                    try {
                      const updated = await api.put<{ uuid: string; secret: string }>(
                        `/users/${editingUser.id}/credentials`,
                        {}
                      );
                      setEditingUser((u) =>
                        u ? { ...u, uuid: updated.uuid, secret: updated.secret } : u
                      );
                      setShowCredentials(true);
                      toast("凭证已重置，节点配置将自动下发", "success");
                      fetchUsers();
                    } catch (err) {
                      toast(err instanceof Error ? err.message : "重置凭证失败", "error");
                    } finally {
                      setResettingCredentials(false);
                    }
                  }}
                >
                  {resettingCredentials ? "重置中..." : "重置凭证"}
                </Button>
              </div>
            </div>

            {/* 门户密码 */}
            <div className="grid gap-2">
              <Label htmlFor="edit-portal-password">门户密码</Label>
              {clearPassword ? (
                <div className="flex items-center gap-2 rounded-md border border-[hsl(var(--destructive))]/40 bg-[hsl(var(--destructive))]/5 px-3 py-2 text-xs text-[hsl(var(--destructive))]">
                  <span className="flex-1">保存后将清除门户密码</span>
                  <button
                    type="button"
                    className="text-[hsl(var(--muted-foreground))] hover:text-[hsl(var(--foreground))]"
                    onClick={() => setClearPassword(false)}
                  >
                    撤销
                  </button>
                </div>
              ) : (
                <div className="flex gap-2">
                  <Input
                    id="edit-portal-password"
                    type="text"
                    placeholder="留空保持不变；输入新密码覆盖"
                    value={editPassword}
                    onChange={(e) => setEditPassword(e.target.value)}
                    autoComplete="new-password"
                    className="flex-1 font-mono text-sm"
                  />
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="shrink-0"
                    onClick={() => setEditPassword(generatePassword())}
                    title="随机生成密码"
                  >
                    随机
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="shrink-0 text-[hsl(var(--destructive))] border-[hsl(var(--destructive))]/30 hover:bg-[hsl(var(--destructive))]/10"
                    onClick={() => { setClearPassword(true); setEditPassword(""); }}
                  >
                    清除
                  </Button>
                </div>
              )}
            </div>

            {/* On Hold Expire At — only when status is on_hold */}
            {editStatus === "on_hold" && (
              <div className="grid gap-2">
                <Label>保留到期时间</Label>
                <DatePicker
                  value={editOnHoldExpireAt}
                  onChange={setEditOnHoldExpireAt}
                  placeholder="选择日期"
                />
              </div>
            )}

            {/* Inbound association */}
            <div className="grid gap-2">
              <Label>入站关联</Label>
              {inboundsLoading ? (
                <p className="text-sm text-[hsl(var(--muted-foreground))]">
                  加载中...
                </p>
              ) : allInbounds.length === 0 ? (
                <p className="text-sm text-[hsl(var(--muted-foreground))]">
                  暂无可用入站
                </p>
              ) : (
                <MultiSelect
                  value={editInboundIds}
                  onChange={setEditInboundIds}
                  options={allInbounds.map((ib) => {
                    const nodeName = allNodes.find((n) => n.id === ib.node_id)?.name ?? ib.node_id;
                    const primaryHost = allHosts.find((h) => h.inbound_id === ib.id);
                    const subName = primaryHost ? hostSubName(primaryHost, ib.traffic_rate) : "";
                    const displayName = subName || ib.tag || `${ib.protocol}:${ib.port}`;
                    return {
                      value: ib.id,
                      triggerLabel: `${nodeName} · ${ib.protocol} · ${displayName}`,
                      label: (
                        <span>
                          <span className="text-[hsl(var(--muted-foreground))]">{nodeName} · {ib.protocol} · </span>
                          <span className="font-medium">{displayName}</span>
                        </span>
                      ),
                    };
                  })}
                  placeholder="选择入站..."
                  countLabel="已选 {n} 个入站"
                />
              )}
            </div>


            {/* Error */}
            {formError && (
              <p className="text-sm text-[hsl(var(--destructive))]">{formError}</p>
            )}
            </div>
          </ScrollArea>

          <DialogFooter>
            <DialogClose asChild>
              <Button variant="outline" disabled={submitting}>
                取消
              </Button>
            </DialogClose>
            <Button onClick={handleEdit} disabled={submitting}>
              {submitting ? "保存中..." : "保存"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* ── Delete Confirmation Dialog ──────────────────────────── */}
      <Dialog
        open={deleteOpen}
        onOpenChange={(open) => {
          setDeleteOpen(open);
          if (!open) setDeletingUser(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>确认删除</DialogTitle>
            <DialogDescription>
              确定要删除用户{" "}
              <span className="font-medium text-[hsl(var(--foreground))]">
                {deletingUser?.username}
              </span>{" "}
              吗？此操作不可撤销。
            </DialogDescription>
          </DialogHeader>

          {formError && (
            <p className="text-sm text-[hsl(var(--destructive))]">{formError}</p>
          )}

          <DialogFooter>
            <DialogClose asChild>
              <Button variant="outline" disabled={submitting}>
                取消
              </Button>
            </DialogClose>
            <Button
              variant="destructive"
              onClick={handleDelete}
              disabled={submitting}
            >
              {submitting ? "删除中..." : "确认删除"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* ── Sub Links Dialog ────────────────────────────────────── */}
      <SubLinksDialog
        open={subLinksOpen}
        onOpenChange={setSubLinksOpen}
        user={subLinksUser}
      />

      {/* ── Sub Logs Dialog ─────────────────────────────────────── */}
      <SubLogsDialog
        open={subLogsOpen}
        onOpenChange={setSubLogsOpen}
        userId={subLogsUserId}
        handleAuthError={handleAuthError}
      />

      {/* ── Node Usage Dialog ──────────────────────────────────── */}
      <NodeUsageDialog
        open={nodeUsageOpen}
        onOpenChange={setNodeUsageOpen}
        userId={nodeUsageUserId}
        handleAuthError={handleAuthError}
      />

      {/* ── 加入/移出用户组 Dialog ─────────────────────────── */}
      <UserGroupsDialog
        open={userGroupsDialogOpen}
        onOpenChange={(v) => {
          setUserGroupsDialogOpen(v);
          if (!v) setUserGroupsDialogUser(null);
        }}
        user={userGroupsDialogUser}
        onSuccess={fetchUsers}
        handleAuthError={handleAuthError}
      />

      <ConfirmDialog
        open={resetTrafficUser !== null}
        onOpenChange={(open) => { if (!open) setResetTrafficUser(null); }}
        title="确认重置流量"
        description={
          <>
            确定要重置用户{" "}
            <span className="font-medium text-[hsl(var(--foreground))]">
              {resetTrafficUser?.username}
            </span>{" "}
            的流量吗？
          </>
        }
        confirmLabel="重置"
        variant="default"
        onConfirm={doResetTraffic}
      />

      {/* ── 账户信息 Dialog ────────────────────────────────────── */}
      <AccountInfoDialog
        user={accountInfoUser}
        portalCreds={portalCreds}
        onOpenChange={(open) => {
          if (!open) setAccountInfoUser(null);
        }}
      />
    </div>
  );
}

// ── 账户信息 Dialog Component ──────────────────────────────────

function AccountInfoDialog({
  user,
  portalCreds,
  onOpenChange,
}: {
  user: User | null;
  portalCreds: PortalCreds | null;
  onOpenChange: (open: boolean) => void;
}) {
  // 记录刚刚复制的字段名，1.5s 后清空
  const [copiedField, setCopiedField] = useState<string | null>(null);

  // 关闭时重置复制状态
  useEffect(() => {
    if (!user) setCopiedField(null);
  }, [user]);

  if (!user) return null;

  const portalUrl = `${window.location.origin}/user/${user.sub_token}`;
  const password =
    portalCreds?.userId === user.id ? portalCreds.password : null;

  const copyField = (field: string, text: string, container?: HTMLElement | null) => {
    copyText(text, container).then(() => {
      setCopiedField(field);
      setTimeout(() => setCopiedField(null), 1500);
    }).catch(() => {});
  };

  const copyAll = (container?: HTMLElement | null) => {
    const lines = [
      `门户地址：${portalUrl}`,
      `用户名：${user.username}`,
    ];
    if (password) lines.push(`密码：${password}`);
    copyText(lines.join("\n"), container).then(() => {
      setCopiedField("all");
      setTimeout(() => setCopiedField(null), 1500);
    }).catch(() => {});
  };

  // 复制图标 SVG
  const CopyIcon = () => (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      className="h-4 w-4"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
    </svg>
  );

  return (
    <Dialog open={!!user} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>账户信息</DialogTitle>
          <DialogDescription>
            用户{" "}
            <span className="font-medium text-[hsl(var(--foreground))]">
              {user.username}
            </span>{" "}
            的门户登录信息。
          </DialogDescription>
        </DialogHeader>

        <div className="grid gap-4 py-2">
          {/* 门户地址 */}
          <div className="grid gap-1.5">
            <span className="text-xs font-medium text-[hsl(var(--muted-foreground))]">门户地址</span>
            <div className="flex items-center gap-2">
              <code className="flex-1 rounded-md border border-[hsl(var(--input))] bg-[hsl(var(--muted)/0.3)] px-3 py-1.5 font-mono text-xs break-all">
                {portalUrl}
              </code>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="shrink-0"
                title="复制门户地址"
                onClick={(e) => copyField("url", portalUrl, e.currentTarget.closest<HTMLElement>('[role="dialog"]'))}
              >
                {copiedField === "url" ? "已复制" : <CopyIcon />}
              </Button>
            </div>
          </div>

          {/* 用户名 */}
          <div className="grid gap-1.5">
            <span className="text-xs font-medium text-[hsl(var(--muted-foreground))]">用户名</span>
            <div className="flex items-center gap-2">
              <code className="flex-1 rounded-md border border-[hsl(var(--input))] bg-[hsl(var(--muted)/0.3)] px-3 py-1.5 font-mono text-xs break-all">
                {user.username}
              </code>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="shrink-0"
                title="复制用户名"
                onClick={(e) => copyField("username", user.username, e.currentTarget.closest<HTMLElement>('[role="dialog"]'))}
              >
                {copiedField === "username" ? "已复制" : <CopyIcon />}
              </Button>
            </div>
          </div>

          {/* 密码 */}
          <div className="grid gap-1.5">
            <span className="text-xs font-medium text-[hsl(var(--muted-foreground))]">密码</span>
            <div className="flex items-center gap-2">
              <code className="flex-1 rounded-md border border-[hsl(var(--input))] bg-[hsl(var(--muted)/0.3)] px-3 py-1.5 font-mono text-xs break-all">
                {password ?? "—"}
              </code>
              {password && (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="shrink-0"
                  title="复制密码"
                  onClick={(e) => copyField("password", password, e.currentTarget.closest<HTMLElement>('[role="dialog"]'))}
                >
                  {copiedField === "password" ? "已复制" : <CopyIcon />}
                </Button>
              )}
            </div>
          </div>
        </div>

        <DialogFooter className="flex-row justify-between sm:justify-between">
          <Button
            variant="outline"
            onClick={(e) => copyAll(e.currentTarget.closest<HTMLElement>('[role="dialog"]'))}
          >
            {copiedField === "all" ? "已复制" : "一键复制全部"}
          </Button>
          <DialogClose asChild>
            <Button variant="ghost">关闭</Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── Sub Logs Dialog Component ──────────────────────────────────

function SubLogsDialog({
  open,
  onOpenChange,
  userId,
  handleAuthError,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  userId: string | null;
  handleAuthError: (err: unknown) => boolean;
}) {
  const [logs, setLogs] = useState<SubAccessLog[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!open || !userId) return;
    let cancelled = false;

    const fetchLogs = async () => {
      setLoading(true);
      setError("");
      setLogs([]);
      try {
        const data = await api.get<SubAccessLogsResponse>(
          `/users/${userId}/sub-logs?limit=50`,
        );
        if (!cancelled) {
          setLogs(data.logs ?? []);
        }
      } catch (err) {
        if (!cancelled) {
          if (!handleAuthError(err)) {
            setError(err instanceof Error ? err.message : "加载日志失败");
          }
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    };

    fetchLogs();
    return () => {
      cancelled = true;
    };
  }, [open, userId, handleAuthError]);

  const formatLogTime = (iso: string) => {
    const d = new Date(iso);
    return d.toLocaleString("zh-CN", {
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
  };

  const truncate = (str: string, max: number) =>
    str.length > max ? str.slice(0, max) + "…" : str;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>订阅访问日志</DialogTitle>
          <DialogDescription>最近 50 条订阅链接访问记录。</DialogDescription>
        </DialogHeader>

        <ScrollArea className="max-h-[60vh]">
          {error && (
            <p className="mb-3 text-sm text-[hsl(var(--destructive))]">
              {error}
            </p>
          )}

          {loading ? (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="px-3">IP</TableHead>
                  <TableHead className="px-3">User Agent</TableHead>
                  <TableHead className="px-3">时间</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {Array.from({ length: 5 }).map((_, i) => (
                  <TableRow key={i}>
                    <TableCell className="px-3">
                      <div className="h-4 w-24 animate-pulse rounded bg-[hsl(var(--muted))]" />
                    </TableCell>
                    <TableCell className="px-3">
                      <div className="h-4 w-48 animate-pulse rounded bg-[hsl(var(--muted))]" />
                    </TableCell>
                    <TableCell className="px-3">
                      <div className="h-4 w-28 animate-pulse rounded bg-[hsl(var(--muted))]" />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          ) : logs.length === 0 ? (
            <div className="flex h-32 items-center justify-center text-sm text-[hsl(var(--muted-foreground))]">
              暂无访问日志
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="px-3">IP</TableHead>
                  <TableHead className="px-3">User Agent</TableHead>
                  <TableHead className="px-3">时间</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {logs.map((log) => (
                  <TableRow key={log.id}>
                    <TableCell className="px-3 text-sm font-mono">
                      {log.ip}
                    </TableCell>
                    <TableCell
                      className="px-3 text-sm text-[hsl(var(--muted-foreground))]"
                      title={log.user_agent}
                    >
                      {truncate(log.user_agent, 40)}
                    </TableCell>
                    <TableCell className="px-3 text-sm text-[hsl(var(--muted-foreground))]">
                      {formatLogTime(log.accessed_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </ScrollArea>

        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline">关闭</Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── Sub Links Dialog Component ──────────────────────────────────

const PROTOCOL_LABELS: Record<string, string> = {
  vless: "VLESS",
  trojan: "Trojan",
  ss: "Shadowsocks",
  hysteria2: "Hysteria2",
  tuic: "TUIC",
  anytls: "AnyTLS",
};

function getProtocol(link: string): string {
  const scheme = link.split("://")[0]?.toLowerCase() ?? "";
  return PROTOCOL_LABELS[scheme] ?? scheme.toUpperCase();
}

function getLinkName(link: string): string {
  try {
    const hash = link.split("#")[1];
    return hash ? decodeURIComponent(hash) : "";
  } catch {
    return "";
  }
}

function SubLinksDialog({
  open,
  onOpenChange,
  user,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  user: User | null;
}) {
  const [links, setLinks] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [copiedIdx, setCopiedIdx] = useState<number | null>(null);

  useEffect(() => {
    if (!open || !user?.sub_token) return;
    let cancelled = false;

    const fetchLinks = async () => {
      setLoading(true);
      setError("");
      setLinks([]);
      try {
        const res = await fetch(`/sub/${user.sub_token}`);
        if (!res.ok) throw new Error(`请求失败 (${res.status})`);
        const text = await res.text();
        const decoded = atob(text.trim());
        const parsed = decoded.split("\n").map((l) => l.trim()).filter(Boolean);
        if (!cancelled) setLinks(parsed);
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : "加载失败");
      } finally {
        if (!cancelled) setLoading(false);
      }
    };

    fetchLinks();
    return () => { cancelled = true; };
  }, [open, user]);

  const copyLink = (link: string, idx: number, container?: HTMLElement | null) => {
    copyText(link, container).then(() => {
      setCopiedIdx(idx);
      setTimeout(() => setCopiedIdx(null), 1500);
    }).catch(() => {});
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>订阅内容</DialogTitle>
          <DialogDescription>
            {user?.username} 的订阅中实际包含的代理链接。
          </DialogDescription>
        </DialogHeader>

        <ScrollArea className="max-h-[60vh]">
          {error && (
            <p className="mb-3 text-sm text-[hsl(var(--destructive))]">{error}</p>
          )}
          {loading ? (
            <div className="flex h-32 items-center justify-center text-sm text-[hsl(var(--muted-foreground))]">
              加载中…
            </div>
          ) : links.length === 0 && !error ? (
            <div className="flex h-32 items-center justify-center text-sm text-[hsl(var(--muted-foreground))]">
              暂无可用链接
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="px-3 w-[240px] max-w-[240px]">名称</TableHead>
                  <TableHead className="px-3 w-[100px]">协议</TableHead>
                  <TableHead className="px-3 w-px text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {links.map((link, idx) => (
                  <TableRow key={idx}>
                    <TableCell className="px-3 w-[240px] max-w-[240px] truncate text-sm">
                      {getLinkName(link) || <span className="text-[hsl(var(--muted-foreground))]">—</span>}
                    </TableCell>
                    <TableCell className="px-3 w-[100px] text-sm">
                      {getProtocol(link)}
                    </TableCell>
                    <TableCell className="px-3 text-right">
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-7 w-12 px-0 text-xs"
                        onClick={(e) => copyLink(link, idx, e.currentTarget.closest<HTMLElement>('[role="dialog"]'))}
                      >
                        {copiedIdx === idx ? "已复制" : "复制"}
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </ScrollArea>

        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline">关闭</Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── Node Usage Dialog Component ────────────────────────────────

interface NodeUsageItem {
  node_id: string;
  node_name: string;
  upload_bytes: number;
  download_bytes: number;
  total_bytes: number;
}

function NodeUsageDialog({
  open,
  onOpenChange,
  userId,
  handleAuthError,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  userId: string | null;
  handleAuthError: (err: unknown) => boolean;
}) {
  const [usage, setUsage] = useState<NodeUsageItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!open || !userId) return;
    let cancelled = false;

    const fetchUsage = async () => {
      setLoading(true);
      setError("");
      setUsage([]);
      try {
        const data = await api.get<{ usage: NodeUsageItem[] }>(
          `/users/${userId}/node-usage`,
        );
        if (!cancelled) {
          setUsage(data.usage ?? []);
        }
      } catch (err) {
        if (!cancelled) {
          if (!handleAuthError(err)) {
            setError(err instanceof Error ? err.message : "加载节点流量失败");
          }
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    };

    fetchUsage();
    return () => {
      cancelled = true;
    };
  }, [open, userId, handleAuthError]);

  const maxTotal = Math.max(...usage.map((u) => u.total_bytes), 1);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>节点流量</DialogTitle>
          <DialogDescription>该用户在各节点的流量使用详情。</DialogDescription>
        </DialogHeader>

        <ScrollArea className="max-h-[60vh]">
          {error && (
            <p className="mb-3 text-sm text-[hsl(var(--destructive))]">
              {error}
            </p>
          )}

          {loading ? (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="px-3">节点名称</TableHead>
                  <TableHead className="px-3">上传</TableHead>
                  <TableHead className="px-3">下载</TableHead>
                  <TableHead className="px-3">总计</TableHead>
                  <TableHead className="px-3 w-24">占比</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {Array.from({ length: 4 }).map((_, i) => (
                  <TableRow key={i}>
                    <TableCell className="px-3">
                      <div className="h-4 w-24 animate-pulse rounded bg-[hsl(var(--muted))]" />
                    </TableCell>
                    <TableCell className="px-3">
                      <div className="h-4 w-16 animate-pulse rounded bg-[hsl(var(--muted))]" />
                    </TableCell>
                    <TableCell className="px-3">
                      <div className="h-4 w-16 animate-pulse rounded bg-[hsl(var(--muted))]" />
                    </TableCell>
                    <TableCell className="px-3">
                      <div className="h-4 w-16 animate-pulse rounded bg-[hsl(var(--muted))]" />
                    </TableCell>
                    <TableCell className="px-3">
                      <div className="h-4 w-full animate-pulse rounded bg-[hsl(var(--muted))]" />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          ) : usage.length === 0 ? (
            <div className="flex h-32 items-center justify-center text-sm text-[hsl(var(--muted-foreground))]">
              暂无节点流量数据
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="px-3">节点名称</TableHead>
                  <TableHead className="px-3">上传</TableHead>
                  <TableHead className="px-3">下载</TableHead>
                  <TableHead className="px-3">总计</TableHead>
                  <TableHead className="px-3 w-24">占比</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {usage.map((item) => (
                  <TableRow key={item.node_id}>
                    <TableCell className="px-3 text-sm font-medium text-[hsl(var(--foreground))]">
                      {item.node_name}
                    </TableCell>
                    <TableCell className="px-3 text-sm text-[hsl(var(--muted-foreground))]">
                      {formatBytes(item.upload_bytes)}
                    </TableCell>
                    <TableCell className="px-3 text-sm text-[hsl(var(--muted-foreground))]">
                      {formatBytes(item.download_bytes)}
                    </TableCell>
                    <TableCell className="px-3 text-sm font-mono text-[hsl(var(--foreground))]">
                      {formatBytes(item.total_bytes)}
                    </TableCell>
                    <TableCell className="px-3">
                      <div className="h-2 w-full rounded-full bg-[hsl(var(--muted))]">
                        <div
                          className="h-2 rounded-full bg-[hsl(var(--primary))]"
                          style={{ width: `${(item.total_bytes / maxTotal) * 100}%` }}
                        />
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </ScrollArea>

        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline">关闭</Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── UserGroupsDialog ─────────────────────────────────────────────

interface UserGroupsDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  user: User | null;
  onSuccess: () => void;
  handleAuthError: (err: unknown) => boolean;
}

function UserGroupsDialog({ open, onOpenChange, user, onSuccess, handleAuthError }: UserGroupsDialogProps) {
  const [allGroups, setAllGroups] = useState<UserGroup[]>([]);
  const [currentGroupIds, setCurrentGroupIds] = useState<Set<string>>(new Set());
  const [selectedGroupIds, setSelectedGroupIds] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!open || !user) return;
    setLoading(true);
    setError("");
    api.get<UserGroupsResponse>("/user-groups")
      .then(async (res) => {
        const groups = res.user_groups ?? [];
        setAllGroups(groups);
        // 并行查询每个组的成员，确定当前用户所属组
        const memberResults = await Promise.all(
          groups.map((g) =>
            api.get<{ members: { user_id: string }[] }>(`/user-groups/${g.id}/members`)
              .then((r) => ({ groupId: g.id, members: r.members ?? [] }))
              .catch(() => ({ groupId: g.id, members: [] }))
          )
        );
        const currentIds = new Set(
          memberResults
            .filter((r) => r.members.some((m) => m.user_id === user.id))
            .map((r) => r.groupId)
        );
        setCurrentGroupIds(currentIds);
        setSelectedGroupIds(new Set(currentIds));
      })
      .catch((err) => {
        if (!handleAuthError(err)) {
          setError(err instanceof Error ? err.message : "加载用户组失败");
        }
      })
      .finally(() => setLoading(false));
  }, [open, user, handleAuthError]);

  function toggleGroup(groupId: string) {
    setSelectedGroupIds((prev) => {
      const next = new Set(prev);
      if (next.has(groupId)) {
        next.delete(groupId);
      } else {
        next.add(groupId);
      }
      return next;
    });
  }

  async function handleConfirm() {
    if (!user) return;
    setSubmitting(true);
    setError("");
    try {
      const toAdd = [...selectedGroupIds].filter((id) => !currentGroupIds.has(id));
      const toRemove = [...currentGroupIds].filter((id) => !selectedGroupIds.has(id));

      await Promise.all([
        ...toAdd.map((gid) => api.post(`/user-groups/${gid}/members`, { user_id: user.id })),
        ...toRemove.map((gid) => api.del(`/user-groups/${gid}/members/${user.id}`)),
      ]);

      if (toAdd.length > 0 || toRemove.length > 0) {
        toast.success(`用户组已更新：添加 ${toAdd.length} 个，移除 ${toRemove.length} 个`);
      } else {
        toast.success("无变更");
      }
      onOpenChange(false);
      onSuccess();
    } catch (err) {
      if (handleAuthError(err)) return;
      setError(err instanceof Error ? err.message : "操作失败");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>加入 / 移出用户组</DialogTitle>
          <DialogDescription>
            管理用户{" "}
            <span className="font-medium text-[hsl(var(--foreground))]">
              {user?.username}
            </span>{" "}
            所属的用户组。
          </DialogDescription>
        </DialogHeader>
        <ScrollArea className="max-h-80">
          <div className="py-4">
          {error && (
            <div className="mb-3 rounded-lg border border-[hsl(var(--destructive))]/50 bg-[hsl(var(--destructive))]/10 px-4 py-2.5 text-sm text-[hsl(var(--destructive))]">
              {error}
            </div>
          )}
          {loading ? (
            <div className="flex h-20 items-center justify-center text-sm text-[hsl(var(--muted-foreground))]">
              加载中…
            </div>
          ) : allGroups.length === 0 ? (
            <div className="flex h-20 items-center justify-center text-sm text-[hsl(var(--muted-foreground))]">
              暂无用户组
            </div>
          ) : (
            <div className="space-y-2">
              {allGroups.map((g) => (
                <label
                  key={g.id}
                  className="flex cursor-pointer items-center gap-3 rounded-md border border-[hsl(var(--border))] px-4 py-2.5 hover:bg-[hsl(var(--accent))]/50"
                >
                  <input
                    type="checkbox"
                    className="h-4 w-4 rounded border-[hsl(var(--border))] accent-[hsl(var(--primary))]"
                    checked={selectedGroupIds.has(g.id)}
                    onChange={() => toggleGroup(g.id)}
                  />
                  <div className="flex flex-col">
                    <span className="text-sm font-medium">{g.name}</span>
                    {g.remark && (
                      <span className="text-xs text-[hsl(var(--muted-foreground))]">
                        {g.remark}
                      </span>
                    )}
                  </div>
                </label>
              ))}
            </div>
          )}
          </div>
        </ScrollArea>
        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant="outline" disabled={submitting}>
              取消
            </Button>
          </DialogClose>
          <Button onClick={handleConfirm} disabled={submitting || loading}>
            {submitting ? "保存中…" : "确认"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
