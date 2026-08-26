import { useState, useEffect, type FormEvent } from "react";
import { getTheme, toggleTheme, type Theme } from "@/lib/theme";
import { getToken } from "@/lib/auth";
import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardContent,
  CardFooter,
  Button,
  Input,
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  Badge,
} from "@/components/ui";

/* ── Types ────────────────────────────────────────────────────── */

interface Plan {
  id: string;
  name: string;
  description: string;
  type: string;
  price_cents: number;
  currency: string;
  traffic_limit: number;
  duration_days: number;
  stock_limit: number; // -1 = 无限制
  stock_sold: number;
}

const MAX_CHECKOUT_QUANTITY = 99;

/* ── Helpers ──────────────────────────────────────────────────── */

function formatPrice(cents: number, currency: string): string {
  const amount = (cents / 100).toFixed(2);
  if (currency.toUpperCase() === "CNY") return `¥${amount}`;
  if (currency.toUpperCase() === "USD") return `$${amount}`;
  return `${amount} ${currency.toUpperCase()}`;
}

function formatTraffic(bytes: number): string {
  if (bytes <= 0) return "无限";
  const gb = bytes / (1024 * 1024 * 1024);
  if (gb >= 1024) return `${(gb / 1024).toFixed(1)} TB`;
  return `${Math.round(gb)} GB`;
}

function formatDuration(days: number): string {
  if (days <= 0) return "永久";
  return `${days} 天`;
}

/* ── Pulse Logo (reused from login) ───────────────────────────── */

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

/* ── Page ──────────────────────────────────────────────────────── */

export default function ShopPage({ basePath = "/shop" }: { basePath?: string }) {
  const isTest = basePath === "/shop-test";
  const [plans, setPlans] = useState<Plan[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [theme, setTheme] = useState<Theme>(getTheme);

  // Checkout dialog state
  const [selectedPlan, setSelectedPlan] = useState<Plan | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [email] = useState(() => `user-${Math.random().toString(36).slice(2, 10)}@noreply.local`);
  const [subToken, setSubToken] = useState(
    () => new URLSearchParams(window.location.search).get("sub_token") ?? ""
  );
  const [checkoutLoading, setCheckoutLoading] = useState(false);
  const [checkoutError, setCheckoutError] = useState("");
  const [quantity, setQuantity] = useState(1);

  /* ── Fetch plans ─────────────────────────────────────────────── */

  useEffect(() => {
    if (isTest && !getToken()) {
      window.location.href = `/panel/login?redirect=${encodeURIComponent(window.location.pathname)}`;
      return;
    }
    let cancelled = false;
    async function load() {
      try {
        const headers: Record<string, string> = {};
        if (isTest) {
          const token = getToken();
          if (token) headers["Authorization"] = `Bearer ${token}`;
        }
        const res = await fetch(`${basePath}/plans`, { headers });
        if (!res.ok || !res.headers.get("content-type")?.includes("application/json")) {
          throw new Error("商店服务未启用");
        }
        const data = await res.json();
        if (!cancelled) setPlans(data.plans ?? []);
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : "加载失败");
      } finally {
        if (!cancelled) setLoading(false);
      }
    }
    load();
    return () => { cancelled = true; };
  }, []);

  /* ── Checkout handler ────────────────────────────────────────── */

  async function handleCheckout(e: FormEvent) {
    e.preventDefault();
    if (!selectedPlan) return;

    setCheckoutError("");
    setCheckoutLoading(true);

    try {
      const body: Record<string, string | number> = {
        plan_id: selectedPlan.id,
        email,
        quantity,
      };
      if (subToken.trim()) body.sub_token = subToken.trim();

      const reqHeaders: Record<string, string> = { "Content-Type": "application/json" };
      if (isTest) {
        const token = getToken();
        if (token) reqHeaders["Authorization"] = `Bearer ${token}`;
      }
      const res = await fetch(`${basePath}/checkout`, {
        method: "POST",
        headers: reqHeaders,
        body: JSON.stringify(body),
      });

      if (!res.ok) {
        const errData = await res.json().catch(() => ({}));
        throw new Error(errData.error ?? errData.detail ?? `HTTP ${res.status}`);
      }

      const data = await res.json();
      if (data.url) {
        window.location.href = data.url;
      } else {
        throw new Error("未返回支付链接");
      }
    } catch (err) {
      setCheckoutError(err instanceof Error ? err.message : "结账失败");
    } finally {
      setCheckoutLoading(false);
    }
  }

  function openCheckout(plan: Plan) {
    setSelectedPlan(plan);
    setQuantity(1);
    setCheckoutError("");
    setSubToken(new URLSearchParams(window.location.search).get("sub_token") ?? "");
    setDialogOpen(true);
  }

  const maxQuantity = selectedPlan
    ? Math.max(
        1,
        Math.min(
          MAX_CHECKOUT_QUANTITY,
          selectedPlan.stock_limit === -1
            ? MAX_CHECKOUT_QUANTITY
            : selectedPlan.stock_limit - selectedPlan.stock_sold,
        ),
      )
    : 1;

  /* ── Render ──────────────────────────────────────────────────── */

  return (
    <div className="h-screen overflow-y-auto bg-[hsl(var(--background))] text-[hsl(var(--foreground))]">
      {/* Header */}
      <header className="border-b border-[hsl(var(--border))] bg-[hsl(var(--card))]">
        <div className="mx-auto flex h-16 max-w-5xl items-center gap-3 px-4 sm:px-6">
          <PulseLogo />
          <span className="text-xl font-bold tracking-tight">Pulse</span>
          <span className="text-sm text-[hsl(var(--muted-foreground))]">—</span>
          <span className="text-sm font-medium text-[hsl(var(--muted-foreground))]">
            选购套餐
          </span>
          <div className="ml-auto flex items-center gap-2">
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

      {/* 沙盒 banner */}
      {isTest && (
        <div className="bg-amber-500/10 border-b border-amber-500/30 py-2 text-center text-sm font-medium text-amber-600">
          测试模式 — 不会真实扣款
        </div>
      )}

      {/* Main */}
      <main className="mx-auto max-w-5xl px-4 py-10 sm:px-6">
        {/* Page heading */}
        <div className="mb-8 text-center">
          <h1 className="text-3xl font-bold tracking-tight sm:text-4xl">
            选购套餐
          </h1>
          <p className="mt-2 text-[hsl(var(--muted-foreground))]">
            选择最适合您的方案，畅享稳定快速的网络服务
          </p>
        </div>

        {/* Loading */}
        {loading && (
          <div className="flex justify-center py-20">
            <div className="h-8 w-8 animate-spin rounded-full border-4 border-[hsl(var(--muted))] border-t-[hsl(var(--primary))]" />
          </div>
        )}

        {/* Error */}
        {error && (
          <div className="mx-auto max-w-md rounded-lg border border-[hsl(var(--destructive))]/50 bg-[hsl(var(--destructive))]/10 px-4 py-3 text-center text-sm text-[hsl(var(--destructive))]">
            {error}
          </div>
        )}

        {/* Empty state */}
        {!loading && !error && plans.length === 0 && (
          <p className="py-20 text-center text-[hsl(var(--muted-foreground))]">
            暂无可用套餐
          </p>
        )}

        {/* Plan grid */}
        {!loading && plans.length > 0 && (
          <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
            {plans.map((plan) => (
              <Card
                key={plan.id}
                className="flex flex-col transition-shadow hover:shadow-lg"
              >
                <CardHeader>
                  <div className="flex items-center justify-between">
                    <CardTitle className="text-xl">{plan.name}</CardTitle>
                    {plan.type && (
                      <Badge variant="secondary" className="text-xs">
                        {plan.type}
                      </Badge>
                    )}
                  </div>
                  {plan.description && (
                    <CardDescription className="mt-1.5">
                      {plan.description}
                    </CardDescription>
                  )}
                </CardHeader>

                <CardContent className="flex-1 space-y-3">
                  {/* Price */}
                  <div>
                    <span className="text-3xl font-bold tracking-tight">
                      {formatPrice(plan.price_cents, plan.currency)}
                    </span>
                  </div>

                  {/* Details */}
                  <div className="space-y-1.5 text-sm text-[hsl(var(--muted-foreground))]">
                    <div className="flex items-center gap-2">
                      <DataIcon className="h-4 w-4 shrink-0" />
                      <span>流量：{formatTraffic(plan.traffic_limit)}</span>
                    </div>
                    <div className="flex items-center gap-2">
                      <ClockIcon className="h-4 w-4 shrink-0" />
                      <span>时长：{formatDuration(plan.duration_days)}</span>
                    </div>
                    {/* 库存 */}
                    {plan.stock_limit !== -1 && (() => {
                      const remaining = plan.stock_limit - plan.stock_sold;
                      return (
                        <div className={[
                          "flex items-center gap-2 font-medium",
                          remaining <= 0 ? "text-[hsl(var(--destructive))]" :
                          remaining <= 5 ? "text-amber-500" : "",
                        ].join(" ")}>
                          <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4" />
                          </svg>
                          <span>
                            {remaining <= 0 ? "库存售罄" : `剩余 ${remaining} 份`}
                          </span>
                        </div>
                      );
                    })()}
                  </div>
                </CardContent>

                <CardFooter>
                  {(() => {
                    const soldOut = plan.stock_limit !== -1 && plan.stock_sold >= plan.stock_limit;
                    return (
                      <Button
                        className="w-full"
                        onClick={() => openCheckout(plan)}
                        disabled={soldOut}
                      >
                        {soldOut ? "已售罄" : "购买"}
                      </Button>
                    );
                  })()}
                </CardFooter>
              </Card>
            ))}
          </div>
        )}
      </main>

      {/* Footer */}
      <footer className="border-t border-[hsl(var(--border))] py-6 text-center text-xs text-[hsl(var(--muted-foreground))]">
        Powered by Pulse
      </footer>

      {/* Checkout Dialog */}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="sm:max-w-md">
          <form onSubmit={handleCheckout}>
            <DialogHeader>
              <DialogTitle>确认购买</DialogTitle>
              <DialogDescription>
                {selectedPlan
                  ? `${selectedPlan.name} — 单价 ${formatPrice(selectedPlan.price_cents, selectedPlan.currency)}`
                  : ""}
              </DialogDescription>
            </DialogHeader>

            <div className="space-y-4 py-4">
              {checkoutError && (
                <div className="rounded-lg border border-[hsl(var(--destructive))]/50 bg-[hsl(var(--destructive))]/10 px-4 py-2.5 text-sm text-[hsl(var(--destructive))]">
                  {checkoutError}
                </div>
              )}

              {selectedPlan && (
                <div className="space-y-4">
                  <div className="space-y-2">
                    <label htmlFor="checkout-quantity" className="text-sm font-medium">
                      购买数量
                    </label>
                    <div className="flex items-center gap-2">
                      <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        aria-label="减少数量"
                        onClick={() => setQuantity((value) => Math.max(1, value - 1))}
                        disabled={checkoutLoading || quantity <= 1}
                      >
                        −
                      </Button>
                      <Input
                        id="checkout-quantity"
                        type="number"
                        min={1}
                        max={maxQuantity}
                        step={1}
                        value={quantity}
                        onChange={(event) => {
                          const value = Number.parseInt(event.target.value, 10);
                          setQuantity(Number.isNaN(value) ? 1 : Math.min(maxQuantity, Math.max(1, value)));
                        }}
                        disabled={checkoutLoading}
                        className="text-center tabular-nums"
                      />
                      <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        aria-label="增加数量"
                        onClick={() => setQuantity((value) => Math.min(maxQuantity, value + 1))}
                        disabled={checkoutLoading || quantity >= maxQuantity}
                      >
                        +
                      </Button>
                    </div>
                    <p className="text-xs text-[hsl(var(--muted-foreground))]">
                      最多可购买 {maxQuantity} 份，流量和有效期将按数量累计。
                    </p>
                  </div>

                  <div className="flex items-center justify-between rounded-lg border border-[hsl(var(--border))] bg-[hsl(var(--muted))]/40 px-4 py-3">
                    <span className="text-sm text-[hsl(var(--muted-foreground))]">合计</span>
                    <span className="text-lg font-semibold tabular-nums">
                      {formatPrice(selectedPlan.price_cents * quantity, selectedPlan.currency)}
                    </span>
                  </div>
                </div>
              )}
            </div>

            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => setDialogOpen(false)}
                disabled={checkoutLoading}
              >
                取消
              </Button>
              <Button type="submit" disabled={checkoutLoading || !selectedPlan}>
                {checkoutLoading ? (
                  <span className="flex items-center gap-2">
                    <span className="inline-block h-4 w-4 animate-spin rounded-full border-2 border-current border-t-transparent" />
                    处理中…
                  </span>
                ) : (
                  "前往支付"
                )}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  );
}

/* ── Inline icons ─────────────────────────────────────────────── */

function DataIcon(props: React.SVGProps<SVGSVGElement>) {
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
      <path d="M21 16V8a2 2 0 0 0-1-1.73L13 2.27a2 2 0 0 0-2 0L4 6.27A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z" />
      <polyline points="3.27 6.96 12 12.01 20.73 6.96" />
      <line x1="12" y1="22.08" x2="12" y2="12" />
    </svg>
  );
}

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
