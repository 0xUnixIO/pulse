import { useEffect, useState, useCallback, type FormEvent } from "react";
import {
  Card,
  CardContent,
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
  Badge,
  Button,
  Input,
  Label,
  Dialog,
  DialogTrigger,
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
  Switch,
  Textarea,
  MultiSelect,
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
  toast,
} from "@/components/ui";
import { api } from "@/lib/api";
import { useAuthErrorHandler } from "@/hooks/useAuthErrorHandler";
import type { Plan, PlanType, PlansResponse, ResetStrategy, UserGroup, UserGroupsResponse } from "@/lib/types";

// ── Icons ────────────────────────────────────────────────────────

function TagIcon(props: React.SVGProps<SVGSVGElement>) {
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
      <path d="M20.59 13.41l-7.17 7.17a2 2 0 0 1-2.83 0L2 12V2h10l8.59 8.59a2 2 0 0 1 0 2.82z" />
      <line x1="7" y1="7" x2="7.01" y2="7" />
    </svg>
  );
}

function EditIcon(props: React.SVGProps<SVGSVGElement>) {
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
      <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7" />
      <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z" />
    </svg>
  );
}

function TrashIcon(props: React.SVGProps<SVGSVGElement>) {
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
      <polyline points="3 6 5 6 21 6" />
      <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" />
      <path d="M10 11v6" />
      <path d="M14 11v6" />
      <path d="M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2" />
    </svg>
  );
}

function AlertCircleIcon(props: React.SVGProps<SVGSVGElement>) {
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
      <line x1="12" y1="8" x2="12" y2="12" />
      <line x1="12" y1="16" x2="12.01" y2="16" />
    </svg>
  );
}

// ── Constants ────────────────────────────────────────────────────

const PLAN_TYPE_LABELS: Record<PlanType, string> = {
  subscription: "订阅",
  one_time: "一次性",
};

const PLAN_TYPE_BADGE_VARIANT: Record<PlanType, "default" | "secondary"> = {
  subscription: "default",
  one_time: "secondary",
};

const RESET_STRATEGY_LABELS: Record<ResetStrategy, string> = {
  no_reset: "不重置",
  day: "每天",
  week: "每周",
  month: "每月",
  year: "每年",
};

const RESET_STRATEGIES: ResetStrategy[] = [
  "no_reset",
  "day",
  "week",
  "month",
  "year",
];

const CURRENCY_SYMBOLS: Record<string, string> = {
  usd: "$",
  cny: "¥",
  eur: "€",
  gbp: "£",
  jpy: "¥",
};

// ── Stripe Price ─────────────────────────────────────────────────

interface StripePrice {
  id: string;
  nickname: string;
  unit_amount: number;
  currency: string;
  recurring: boolean;
  product_name: string;
}

// ── Order types ──────────────────────────────────────────────────

interface Order {
  id: string;
  user_id: string;
  plan_id: string;
  email: string;
  stripe_session_id: string;
  stripe_subscription_id: string;
  stripe_customer_id: string;
  status: string;
  amount_cents: number;
  quantity: number;
  currency: string;
  created_at: string;
  paid_at?: string;
  last_invoice_id: string;
}

const ORDER_STATUS_LABEL: Record<string, string> = { paid: "已付款", pending: "待付款", failed: "失败", refunded: "已退款" };

function orderStatusVariant(status: string): "default" | "secondary" | "destructive" | "outline" {
  switch (status) {
    case "paid": return "default";
    case "pending": return "secondary";
    case "failed": return "destructive";
    default: return "outline";
  }
}

function formatOrderAmount(cents: number, currency: string): string {
  return new Intl.NumberFormat("zh-CN", { style: "currency", currency: currency.toUpperCase(), minimumFractionDigits: 2 }).format(cents / 100);
}

function formatOrderDate(iso?: string): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleString("zh-CN", { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

// ── Helpers ──────────────────────────────────────────────────────

function formatTraffic(bytes: number): string {
  if (bytes <= 0) return "无限制";
  const gb = bytes / (1024 * 1024 * 1024);
  if (gb >= 1024) {
    return `${(gb / 1024).toFixed(2)} TB`;
  }
  return `${gb.toFixed(2)} GB`;
}

function formatPrice(priceCents: number, currency: string): string {
  const symbol = CURRENCY_SYMBOLS[currency.toLowerCase()] ?? currency.toUpperCase() + " ";
  const amount = (priceCents / 100).toFixed(2);
  return `${symbol}${amount}`;
}

function bytesToGB(bytes: number): number {
  if (bytes <= 0) return 0;
  return parseFloat((bytes / (1024 * 1024 * 1024)).toFixed(4));
}

function gbToBytes(gb: number): number {
  return Math.round(gb * 1024 * 1024 * 1024);
}

// ── Empty form state ─────────────────────────────────────────────

interface PlanForm {
  name: string;
  description: string;
  type: PlanType;
  price_cents: number;
  currency: string;
  stripe_price_id: string;
  traffic_limit_gb: number;
  duration_days: number;
  data_limit_reset_strategy: ResetStrategy;
  user_group_ids: string[];
  sort_order: number;
  enabled: boolean;
  mode: "live" | "test";
  stock_limit: number; // -1 = 无限制
}

const EMPTY_FORM: PlanForm = {
  name: "",
  description: "",
  type: "subscription",
  price_cents: 0,
  currency: "usd",
  stripe_price_id: "",
  traffic_limit_gb: 0,
  duration_days: 30,
  data_limit_reset_strategy: "no_reset",
  user_group_ids: [],
  sort_order: 0,
  enabled: true,
  mode: "live",
  stock_limit: -1,
};

// ── Skeleton rows ────────────────────────────────────────────────

function SkeletonRow() {
  return (
    <TableRow>
      {Array.from({ length: 9 }).map((_, i) => (
        <TableCell key={i} className="px-4">
          <div className="h-4 w-24 animate-pulse rounded bg-[hsl(var(--muted))]" />
        </TableCell>
      ))}
    </TableRow>
  );
}

// ── Main page ────────────────────────────────────────────────────

export default function PlansPage() {
  const handleAuthError = useAuthErrorHandler();

  const [plans, setPlans] = useState<Plan[]>([]);
  const [allUserGroups, setAllUserGroups] = useState<UserGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Dialog states
  const [createOpen, setCreateOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  // Form data
  const [form, setForm] = useState<PlanForm>(EMPTY_FORM);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [deletingPlan, setDeletingPlan] = useState<Plan | null>(null);

  // Stripe prices
  const [stripePrices, setStripePrices] = useState<StripePrice[]>([]);
  const [pricesLoading, setPricesLoading] = useState(false);
  const [pricesFetched, setPricesFetched] = useState(false);

  const fetchStripePrices = useCallback(() => {
    if (pricesLoading || pricesFetched) return;
    setPricesLoading(true);
    api
      .get<{ prices: StripePrice[] }>("/settings/stripe/prices")
      .then((res) => setStripePrices(res.prices ?? []))
      .catch(() => setStripePrices([]))
      .finally(() => { setPricesLoading(false); setPricesFetched(true); });
  }, [pricesLoading, pricesFetched]);

  // ── 账单 state ──────────────────────────────────────────────────
  const [orders, setOrders] = useState<Order[]>([]);
  const [ordersLoading, setOrdersLoading] = useState(false);
  const [orderSearch, setOrderSearch] = useState("");

  const fetchOrders = useCallback(async () => {
    setOrdersLoading(true);
    try {
      const res = await api.get<{ orders: Order[] }>("/orders");
      setOrders(res.orders ?? []);
    } catch (err) {
      if (!handleAuthError(err)) { toast(err instanceof Error ? err.message : "加载账单失败", "error"); }
    } finally {
      setOrdersLoading(false);
    }
  }, [handleAuthError]);

  const patchForm = useCallback(
    (patch: Partial<PlanForm>) =>
      setForm((prev) => ({ ...prev, ...patch })),
    [],
  );

  // ── Fetch ───────────────────────────────────────────────────────

  const fetchData = useCallback(() => {
    setLoading(true);
    setError(null);
    Promise.all([
      api.get<PlansResponse>("/plans"),
      api.get<UserGroupsResponse>("/user-groups"),
    ])
      .then(([plansRes, groupsRes]) => {
        setPlans(plansRes.plans ?? []);
        setAllUserGroups(groupsRes.user_groups ?? []);
      })
      .catch((err) => {
        if (handleAuthError(err)) return;
        setError(err instanceof Error ? err.message : "加载失败");
      })
      .finally(() => setLoading(false));
  }, [handleAuthError]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  // ── Build request body ──────────────────────────────────────────

  function buildBody() {
    return {
      name: form.name.trim(),
      description: form.description.trim(),
      type: form.type,
      price_cents: Number(form.price_cents) || 0,
      currency: form.currency.trim().toLowerCase() || "usd",
      stripe_price_id: form.stripe_price_id.trim(),
      traffic_limit: gbToBytes(Number(form.traffic_limit_gb) || 0),
      duration_days: Number(form.duration_days) || 0,
      data_limit_reset_strategy: form.data_limit_reset_strategy,
      user_group_ids: form.user_group_ids.join(","),
      sort_order: Number(form.sort_order) || 0,
      enabled: form.enabled,
      mode: form.mode,
      stock_limit: Number(form.stock_limit) || -1,
    };
  }

  // ── Create ──────────────────────────────────────────────────────

  async function handleCreate(e: FormEvent) {
    e.preventDefault();
    setFormError(null);
    setSubmitting(true);
    try {
      await api.post<Plan>("/plans", buildBody());
      setCreateOpen(false);
      setForm(EMPTY_FORM);
      fetchData();
    } catch (err) {
      if (handleAuthError(err)) return;
      setFormError(err instanceof Error ? err.message : "创建失败");
    } finally {
      setSubmitting(false);
    }
  }

  // ── Edit ────────────────────────────────────────────────────────

  function openEdit(plan: Plan) {
    setEditingId(plan.id);
    setForm({
      name: plan.name,
      description: plan.description,
      type: plan.type as PlanType,
      price_cents: plan.price_cents,
      currency: plan.currency,
      stripe_price_id: plan.stripe_price_id,
      traffic_limit_gb: bytesToGB(plan.traffic_limit),
      duration_days: plan.duration_days,
      data_limit_reset_strategy: plan.data_limit_reset_strategy,
      user_group_ids: plan.user_group_ids ? plan.user_group_ids.split(",").filter(Boolean) : [],
      sort_order: plan.sort_order,
      enabled: plan.enabled,
      mode: plan.mode ?? "live",
      stock_limit: plan.stock_limit ?? -1,
    });
    setFormError(null);
    setEditOpen(true);
  }

  async function handleEdit(e: FormEvent) {
    e.preventDefault();
    if (!editingId) return;
    setFormError(null);
    setSubmitting(true);
    try {
      await api.put<Plan>(`/plans/${editingId}`, buildBody());
      setEditOpen(false);
      setForm(EMPTY_FORM);
      setEditingId(null);
      fetchData();
    } catch (err) {
      if (handleAuthError(err)) return;
      setFormError(err instanceof Error ? err.message : "更新失败");
    } finally {
      setSubmitting(false);
    }
  }

  // ── Delete ──────────────────────────────────────────────────────

  function openDelete(plan: Plan) {
    setDeletingPlan(plan);
    setFormError(null);
    setDeleteOpen(true);
  }

  async function handleDelete() {
    if (!deletingPlan) return;
    setFormError(null);
    setSubmitting(true);
    try {
      await api.del<{ deleted: boolean }>(`/plans/${deletingPlan.id}`);
      setDeleteOpen(false);
      setDeletingPlan(null);
      fetchData();
    } catch (err) {
      if (handleAuthError(err)) return;
      setFormError(err instanceof Error ? err.message : "删除失败");
    } finally {
      setSubmitting(false);
    }
  }

  // ── Error state ─────────────────────────────────────────────────

  if (error && !plans.length) {
    return (
      <div className="flex h-full items-center justify-center p-8">
        <Card className="w-full max-w-md">
          <CardContent className="pt-6 text-center">
            <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-[hsl(var(--destructive))]/10 text-[hsl(var(--destructive))]">
              <AlertCircleIcon className="h-6 w-6" />
            </div>
            <p className="mb-1 font-semibold text-[hsl(var(--foreground))]">
              加载失败
            </p>
            <p className="mb-4 text-sm text-[hsl(var(--muted-foreground))]">
              {error}
            </p>
            <Button variant="outline" onClick={fetchData}>
              重试
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  // ── Shared form fields (for create & edit dialogs) ──────────────

  function renderFormFields() {
    return (
      <div className="grid gap-4 py-4">
        {formError && (
          <div className="rounded-lg border border-[hsl(var(--destructive))]/50 bg-[hsl(var(--destructive))]/10 px-4 py-2.5 text-sm text-[hsl(var(--destructive))]">
            {formError}
          </div>
        )}

        <div className="space-y-2">
          <Label htmlFor="plan-name">名称 *</Label>
          <Input
            id="plan-name"
            required
            value={form.name}
            onChange={(e) => patchForm({ name: e.target.value })}
            placeholder="套餐名称"
          />
        </div>

        <div className="space-y-2">
          <Label htmlFor="plan-desc">描述</Label>
          <Textarea
            id="plan-desc"
            value={form.description}
            onChange={(e) => patchForm({ description: e.target.value })}
            placeholder="套餐描述"
            rows={3}
          />
        </div>

        {/* Stripe Price 选择器（第一位），选中后自动填写以下只读字段 */}
        <div className="space-y-2">
          <Label>Stripe Price</Label>
          {stripePrices.length > 0 ? (
            <Select
              value={form.stripe_price_id}
              onValueChange={(v) => {
                if (v === "__none__") {
                  patchForm({ stripe_price_id: "", price_cents: 0, currency: "usd" });
                  return;
                }
                const price = stripePrices.find((p) => p.id === v);
                patchForm({
                  stripe_price_id: v,
                  ...(price && {
                    price_cents: price.unit_amount,
                    currency: price.currency,
                    type: price.recurring ? "subscription" : "one_time",
                  }),
                });
              }}
            >
              <SelectTrigger>
                <SelectValue placeholder="选择价格（可选）" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="__none__">— 不关联 —</SelectItem>
                {stripePrices.map((p) => {
                  const amount = (p.unit_amount / 100).toFixed(2);
                  const label = [
                    p.product_name || p.nickname || p.id,
                    `${p.currency.toUpperCase()} ${amount}`,
                    p.recurring ? "订阅" : "一次性",
                  ].filter(Boolean).join(" · ");
                  return (
                    <SelectItem key={p.id} value={p.id}>
                      {label}
                    </SelectItem>
                  );
                })}
              </SelectContent>
            </Select>
          ) : (
            <Input
              value={form.stripe_price_id}
              onChange={(e) => patchForm({ stripe_price_id: e.target.value })}
              placeholder={pricesLoading ? "加载中…" : "price_xxx（Stripe 未配置时手动填写）"}
              disabled={pricesLoading}
            />
          )}
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-2">
            <Label htmlFor="plan-type">类型</Label>
            <Input
              id="plan-type"
              value={form.type === "subscription" ? "订阅" : "一次性"}
              readOnly
              className="opacity-60 cursor-not-allowed"
            />
          </div>

          <div className="space-y-2">
            <Label htmlFor="plan-currency">货币</Label>
            <Input
              id="plan-currency"
              value={form.currency}
              readOnly
              className="opacity-60 cursor-not-allowed"
            />
          </div>
        </div>

        <div className="space-y-2">
          <Label htmlFor="plan-price">价格 (分)</Label>
          <Input
            id="plan-price"
            type="number"
            value={form.price_cents}
            readOnly
            className="opacity-60 cursor-not-allowed"
          />
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-2">
            <Label htmlFor="plan-traffic">流量限额 (GB)</Label>
            <Input
              id="plan-traffic"
              type="number"
              min={0}
              step="0.01"
              value={form.traffic_limit_gb}
              onChange={(e) =>
                patchForm({
                  traffic_limit_gb: Number(e.target.value) || 0,
                })
              }
              placeholder="0 = 无限制"
            />
          </div>

          <div className="space-y-2">
            <Label htmlFor="plan-duration">时长 (天)</Label>
            <Input
              id="plan-duration"
              type="number"
              min={0}
              value={form.duration_days}
              onChange={(e) =>
                patchForm({ duration_days: Number(e.target.value) || 0 })
              }
              placeholder="30"
            />
          </div>
        </div>

        <div className="space-y-2">
          <Label htmlFor="plan-reset">流量重置策略</Label>
          <Select
            value={form.data_limit_reset_strategy}
            onValueChange={(v) =>
              patchForm({ data_limit_reset_strategy: v as ResetStrategy })
            }
          >
            <SelectTrigger id="plan-reset">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {RESET_STRATEGIES.map((s) => (
                <SelectItem key={s} value={s}>
                  {RESET_STRATEGY_LABELS[s]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="space-y-2">
          <Label>绑定用户组</Label>
          <MultiSelect
            value={form.user_group_ids}
            onChange={(ids) => patchForm({ user_group_ids: ids })}
            options={allUserGroups.map((g) => ({
              value: g.id,
              triggerLabel: g.name,
              label: g.remark ? (
                <span><span className="font-medium">{g.name}</span><span className="ml-2 text-[hsl(var(--muted-foreground))] text-xs">{g.remark}</span></span>
              ) : g.name,
            }))}
            placeholder="不绑定用户组"
            countLabel="已选 {n} 个用户组"
          />
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-2">
            <Label htmlFor="plan-sort">排序</Label>
            <Input
              id="plan-sort"
              type="number"
              min={0}
              value={form.sort_order}
              onChange={(e) =>
                patchForm({ sort_order: Number(e.target.value) || 0 })
              }
            />
          </div>

          <div className="flex items-end space-x-3 pb-1">
            <Switch
              id="plan-enabled"
              checked={form.enabled}
              onCheckedChange={(checked) =>
                patchForm({ enabled: checked })
              }
            />
            <Label htmlFor="plan-enabled" className="cursor-pointer">
              启用
            </Label>
          </div>
        </div>

        {/* 环境 */}
        <div className="space-y-2">
          <Label>环境</Label>
          <div className="flex rounded-md border border-[hsl(var(--border))] overflow-hidden w-fit">
            {(["live", "test"] as const).map((m) => (
              <button
                key={m}
                type="button"
                onClick={() => patchForm({ mode: m })}
                className={[
                  "px-4 py-1.5 text-sm font-medium transition-colors",
                  form.mode === m
                    ? m === "test"
                      ? "bg-amber-500 text-white"
                      : "bg-green-600 text-white"
                    : "text-[hsl(var(--muted-foreground))] hover:bg-[hsl(var(--muted))]",
                ].join(" ")}
              >
                {m === "live" ? "生产" : "沙盒"}
              </button>
            ))}
          </div>
        </div>

        {/* 库存设置 */}
        <div className="space-y-2">
          <Label htmlFor="plan-stock">库存限制</Label>
          <div className="flex items-center gap-3">
            <Input
              id="plan-stock"
              type="number"
              min={-1}
              value={form.stock_limit}
              onChange={(e) => patchForm({ stock_limit: Number(e.target.value) })}
              className="w-36"
            />
            <span className="text-sm text-[hsl(var(--muted-foreground))]">
              {form.stock_limit === -1 ? "无限制" : `最多售出 ${form.stock_limit} 份`}
            </span>
          </div>
          <p className="text-xs text-[hsl(var(--muted-foreground))]">-1 表示无限制</p>
        </div>
      </div>
    );
  }

  // ── Render ──────────────────────────────────────────────────────

  return (
    <div className="flex h-full flex-col p-4 sm:p-6 lg:p-8">
      <Tabs defaultValue="plans" className="flex flex-col flex-1 min-h-0" onValueChange={(v) => { if (v === "billing" && !ordersLoading) fetchOrders(); }}>
        <div className="mb-4">
          <TabsList>
            <TabsTrigger value="plans">套餐管理</TabsTrigger>
            <TabsTrigger value="billing">账单</TabsTrigger>
          </TabsList>
        </div>

        {/* ════════════════ 套餐 Tab ════════════════ */}
        <TabsContent value="plans" className="flex flex-col flex-1 min-h-0 mt-0">

        <div className="mb-4 flex items-center justify-between gap-3">
        <span className="text-sm text-[hsl(var(--muted-foreground))]">{plans.length} 个套餐</span>

        {/* ── Create dialog ─────────────────────────────────────── */}
        <Dialog
          open={createOpen}
          onOpenChange={(open) => {
            setCreateOpen(open);
            if (open) {
              fetchStripePrices();
            }
            if (!open) {
              setForm(EMPTY_FORM);
              setFormError(null);
            }
          }}
        >
          <DialogTrigger asChild>
            <Button>+ 添加套餐</Button>
          </DialogTrigger>
          <DialogContent className="sm:max-w-lg max-h-[90vh] overflow-y-auto">
            <form onSubmit={handleCreate}>
              <DialogHeader>
                <DialogTitle>添加套餐</DialogTitle>
                <DialogDescription>
                  创建一个新的套餐计划。
                </DialogDescription>
              </DialogHeader>
              {renderFormFields()}
              <DialogFooter>
                <DialogClose asChild>
                  <Button type="button" variant="outline">
                    取消
                  </Button>
                </DialogClose>
                <Button type="submit" disabled={submitting}>
                  {submitting ? "创建中…" : "创建"}
                </Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </Dialog>
      </div>

      {/* ── Table ───────────────────────────────────────────────── */}
      <Card className="flex min-h-0 flex-1 flex-col overflow-hidden">
        <Table containerClassName="flex-1 overflow-auto">
          <TableHeader className="sticky top-0 z-10 bg-[hsl(var(--card))]">
            <TableRow>
              <TableHead className="px-4">名称</TableHead>
              <TableHead className="px-4">类型</TableHead>
              <TableHead className="px-4">价格</TableHead>
              <TableHead className="px-4">流量限额</TableHead>
              <TableHead className="px-4">时长</TableHead>
              <TableHead className="px-4">库存</TableHead>
              <TableHead className="px-4">环境</TableHead>
              <TableHead className="px-4">状态</TableHead>
              <TableHead className="px-4">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              Array.from({ length: 3 }).map((_, i) => (
                <SkeletonRow key={i} />
              ))
            ) : plans.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={9}
                  className="h-32 text-center text-[hsl(var(--muted-foreground))]"
                >
                  <div className="flex flex-col items-center gap-3">
                    <TagIcon className="h-10 w-10 opacity-40" />
                    <p>暂无套餐</p>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => setCreateOpen(true)}
                    >
                      + 添加套餐
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              plans.map((plan) => {
                const remaining = plan.stock_limit === -1 ? null : plan.stock_limit - plan.stock_sold;
                return (
                  <TableRow key={plan.id}>
                    <TableCell className="px-4 font-medium text-[hsl(var(--foreground))]">
                      {plan.name}
                    </TableCell>
                    <TableCell className="px-4">
                      <Badge variant={PLAN_TYPE_BADGE_VARIANT[plan.type as PlanType] ?? "outline"}>
                        {PLAN_TYPE_LABELS[plan.type as PlanType] ?? plan.type}
                      </Badge>
                    </TableCell>
                    <TableCell className="px-4 font-mono text-sm text-[hsl(var(--foreground))]">
                      {formatPrice(plan.price_cents, plan.currency)}
                    </TableCell>
                    <TableCell className="px-4 text-sm text-[hsl(var(--muted-foreground))]">
                      {formatTraffic(plan.traffic_limit)}
                    </TableCell>
                    <TableCell className="px-4 text-sm text-[hsl(var(--muted-foreground))]">
                      {plan.duration_days}天
                    </TableCell>
                    <TableCell className="px-4 text-sm">
                      {remaining === null ? (
                        <span className="text-[hsl(var(--muted-foreground))]">无限制</span>
                      ) : remaining <= 0 ? (
                        <span className="text-[hsl(var(--destructive))] font-medium">已售罄</span>
                      ) : (
                        <span className={remaining <= 5 ? "text-amber-500 font-medium" : "text-[hsl(var(--muted-foreground))]"}>
                          剩 {remaining} / {plan.stock_limit}
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="px-4">
                      {plan.mode === "test" ? (
                        <Badge className="bg-amber-500/15 text-amber-600 hover:bg-amber-500/15 border-0">沙盒</Badge>
                      ) : (
                        <Badge className="bg-blue-500/15 text-blue-600 hover:bg-blue-500/15 border-0">生产</Badge>
                      )}
                    </TableCell>
                    <TableCell className="px-4">
                      {plan.enabled ? (
                        <Badge className="bg-emerald-500/15 text-emerald-600 hover:bg-emerald-500/15 border-0">启用</Badge>
                      ) : (
                        <Badge variant="secondary">停用</Badge>
                      )}
                    </TableCell>
                    <TableCell className="px-4">
                      <div className="flex gap-1">
                        <Button variant="ghost" size="sm" onClick={() => openEdit(plan)}>
                          <EditIcon className="h-4 w-4" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          className="text-[hsl(var(--destructive))] hover:text-[hsl(var(--destructive))]"
                          onClick={() => openDelete(plan)}
                        >
                          <TrashIcon className="h-4 w-4" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
      </Card>

      {/* ── Edit dialog ─────────────────────────────────────────── */}
      <Dialog
        open={editOpen}
        onOpenChange={(open) => {
          setEditOpen(open);
          if (open) fetchStripePrices();
          if (!open) {
            setForm(EMPTY_FORM);
            setEditingId(null);
            setFormError(null);
          }
        }}
      >
        <DialogContent className="sm:max-w-lg max-h-[90vh] overflow-y-auto">
          <form onSubmit={handleEdit}>
            <DialogHeader>
              <DialogTitle>编辑套餐</DialogTitle>
              <DialogDescription>
                修改套餐配置。
              </DialogDescription>
            </DialogHeader>
            {renderFormFields()}
            <DialogFooter>
              <DialogClose asChild>
                <Button type="button" variant="outline">
                  取消
                </Button>
              </DialogClose>
              <Button type="submit" disabled={submitting}>
                {submitting ? "保存中…" : "保存"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* ── Delete dialog ───────────────────────────────────────── */}
      <Dialog
        open={deleteOpen}
        onOpenChange={(open) => {
          setDeleteOpen(open);
          if (!open) {
            setDeletingPlan(null);
            setFormError(null);
          }
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>确认删除</DialogTitle>
            <DialogDescription>
              确定要删除套餐{" "}
              <span className="font-semibold text-[hsl(var(--foreground))]">
                {deletingPlan?.name}
              </span>{" "}
              吗？此操作不可撤销。
            </DialogDescription>
          </DialogHeader>
          {formError && (
            <div className="rounded-lg border border-[hsl(var(--destructive))]/50 bg-[hsl(var(--destructive))]/10 px-4 py-2.5 text-sm text-[hsl(var(--destructive))]">
              {formError}
            </div>
          )}
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline">
                取消
              </Button>
            </DialogClose>
            <Button
              variant="destructive"
              disabled={submitting}
              onClick={handleDelete}
            >
              {submitting ? "删除中…" : "删除"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

        </TabsContent>

        {/* ════════════════ 账单 Tab ════════════════ */}
        <TabsContent value="billing" className="flex flex-col flex-1 min-h-0 mt-0">
          <div className="mb-4 flex items-center justify-between gap-3">
            <p className="text-sm text-[hsl(var(--muted-foreground))]">
              共 {orders.length} 笔订单 ·{" "}
              已付款 {orders.filter((o) => o.status === "paid").length} 笔 ·{" "}
              总收入{" "}
              {orders.length > 0
                ? formatOrderAmount(orders.filter((o) => o.status === "paid").reduce((s, o) => s + o.amount_cents, 0), orders[0]?.currency ?? "usd")
                : "—"}
            </p>
            <div className="flex items-center gap-2">
              <Button variant="outline" size="sm" onClick={fetchOrders} disabled={ordersLoading} className="h-8">
                {ordersLoading ? "加载中…" : "刷新"}
              </Button>
              <Input
                placeholder="搜索邮箱 / 订单 ID"
                value={orderSearch}
                onChange={(e) => setOrderSearch(e.target.value)}
                className="w-56 h-8 text-sm"
              />
            </div>
          </div>

          <Card className="flex min-h-0 flex-1 flex-col overflow-hidden">
            <Table containerClassName="flex-1 overflow-auto">
              <TableHeader className="sticky top-0 z-10 bg-[hsl(var(--card))]">
                <TableRow>
                  <TableHead className="w-[180px]">邮箱</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>数量</TableHead>
                  <TableHead>金额</TableHead>
                  <TableHead className="hidden md:table-cell">订阅</TableHead>
                  <TableHead className="hidden lg:table-cell">创建时间</TableHead>
                  <TableHead className="hidden lg:table-cell">付款时间</TableHead>
                  <TableHead className="hidden xl:table-cell text-right">订单 ID</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {ordersLoading ? (
                  <TableRow>
                    <TableCell colSpan={8} className="py-8 text-center text-sm text-[hsl(var(--muted-foreground))]">加载中…</TableCell>
                  </TableRow>
                ) : (() => {
                  const filtered = orderSearch.trim()
                    ? orders.filter((o) =>
                        o.email.toLowerCase().includes(orderSearch.toLowerCase()) ||
                        o.id.toLowerCase().includes(orderSearch.toLowerCase()) ||
                        o.stripe_customer_id.toLowerCase().includes(orderSearch.toLowerCase())
                      )
                    : orders;
                  return filtered.length === 0 ? (
                    <TableRow>
                      <TableCell colSpan={8} className="py-8 text-center text-sm text-[hsl(var(--muted-foreground))]">
                        {orderSearch ? "未找到匹配的订单" : "暂无订单"}
                      </TableCell>
                    </TableRow>
                  ) : filtered.map((order) => (
                    <TableRow key={order.id}>
                      <TableCell className="font-mono text-xs">{order.email || "—"}</TableCell>
                      <TableCell>
                        <Badge variant={orderStatusVariant(order.status)}>
                          {ORDER_STATUS_LABEL[order.status] ?? order.status}
                        </Badge>
                      </TableCell>
                      <TableCell className="tabular-nums">{order.quantity || 1}</TableCell>
                      <TableCell className="font-medium tabular-nums">
                        {formatOrderAmount(order.amount_cents, order.currency)}
                      </TableCell>
                      <TableCell className="hidden md:table-cell">
                        {order.stripe_subscription_id
                          ? <span className="text-xs text-[hsl(var(--muted-foreground))] font-mono">订阅</span>
                          : <span className="text-xs text-[hsl(var(--muted-foreground))]">一次性</span>}
                      </TableCell>
                      <TableCell className="hidden lg:table-cell text-xs text-[hsl(var(--muted-foreground))]">
                        {formatOrderDate(order.created_at)}
                      </TableCell>
                      <TableCell className="hidden lg:table-cell text-xs text-[hsl(var(--muted-foreground))]">
                        {formatOrderDate(order.paid_at)}
                      </TableCell>
                      <TableCell className="hidden xl:table-cell text-right">
                        <span className="text-xs font-mono text-[hsl(var(--muted-foreground))]">{order.id.slice(0, 12)}…</span>
                      </TableCell>
                    </TableRow>
                  ));
                })()}
              </TableBody>
            </Table>
          </Card>
        </TabsContent>

      </Tabs>
    </div>
  );
}
