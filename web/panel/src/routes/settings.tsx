import { useEffect, useState, useCallback } from "react";
import { useNavigate } from "@tanstack/react-router";
import {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
  Button,
  Input,
  Label,
  Separator,
  Switch,
  toast,
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
} from "@/components/ui";
import { ScrollArea } from "@/components/ui/scroll-area";
import { api, AuthError } from "@/lib/api";
import { useAuthErrorHandler } from "@/hooks/useAuthErrorHandler";
import { clearToken, getToken } from "@/lib/auth";

// ── Types ────────────────────────────────────────────────────────

// ── Main page ────────────────────────────────────────────────────

export default function SettingsPage() {
  const navigate = useNavigate();

  // DB stats (独立接口，避免阻塞主页加载)
  interface DBStats {
    file_size_bytes: number;
    wal_size_bytes: number;
    page_count: number;
    page_size: number;
    free_pages: number;
    ping_latency_ms: number;
    tables: Record<string, number>;
  }
  const [dbStats, setDbStats] = useState<DBStats | null>(null);
  const [dbStatsLoading, setDbStatsLoading] = useState(true);

  // Bark alert
  const [barkURL, setBarkURL] = useState("");
  const [barkLoading, setBarkLoading] = useState(true);
  const [barkSaving, setBarkSaving] = useState(false);
  const [barkTesting, setBarkTesting] = useState(false);
  const [barkMsg, setBarkMsg] = useState<string | null>(null);


  // Backup
  const [backupAccountID, setBackupAccountID] = useState("");
  const [backupAccessKeyID, setBackupAccessKeyID] = useState("");
  const [backupSecretKey, setBackupSecretKey] = useState("");
  const [backupBucketName, setBackupBucketName] = useState("");
  const [backupIntervalHours, setBackupIntervalHours] = useState("");
  const [backupKeepCount, setBackupKeepCount] = useState("");
  const [backupLastAt, setBackupLastAt] = useState("");
  const [backupLoading, setBackupLoading] = useState(true);
  const [backupSaving, setBackupSaving] = useState(false);
  const [backupRunning, setBackupRunning] = useState(false);
  const [backupMsg, setBackupMsg] = useState<string | null>(null);
  // Restore
  const [restoreOpen, setRestoreOpen] = useState(false);
  const [restoreList, setRestoreList] = useState<{ key: string; last_modified: string; size: number }[]>([]);
  const [restoreListLoading, setRestoreListLoading] = useState(false);
  const [restoreListError, setRestoreListError] = useState<string | null>(null);
  const [restoringKey, setRestoringKey] = useState<string | null>(null);
  const [restoreMsg, setRestoreMsg] = useState<string | null>(null);

  // Stripe settings
  const [stripeKeys, setStripeKeys] = useState({
    test: { secret_key: "", webhook_secret: "" },
    live: { secret_key: "", webhook_secret: "" },
  });
  const [stripeShow, setStripeShow] = useState({
    test: { secret_key: false, webhook_secret: false },
    live: { secret_key: false, webhook_secret: false },
  });
  const [stripeLoading, setStripeLoading] = useState(true);
  const [stripeSaving, setStripeSaving] = useState(false);
  const [stripeMsg, setStripeMsg] = useState<string | null>(null);

  // Shop settings
  const [shopBaseURL, setShopBaseURL] = useState("");
  const [shopRenewalOnly, setShopRenewalOnly] = useState(false);
  const [shopRenewalSaving, setShopRenewalSaving] = useState(false);
  const [shopLoading, setShopLoading] = useState(true);
  const [shopSaving, setShopSaving] = useState(false);
  const [shopMsg, setShopMsg] = useState<string | null>(null);

  // Cloudflare API Token
  const [cfToken, setCfToken] = useState("");
  const [cfHasToken, setCfHasToken] = useState(false);
  const [cfSaving, setCfSaving] = useState(false);
  const [cfShowToken, setCfShowToken] = useState(false);

  // DB Cleanup
  const [dbCleaning, setDbCleaning] = useState(false);

  // Log retention settings
  const [logUptimeDays, setLogUptimeDays] = useState(30);
  const [logDailyDays, setLogDailyDays] = useState(180);
  const [logRetentionLoading, setLogRetentionLoading] = useState(true);
  const [logRetentionSaving, setLogRetentionSaving] = useState(false);
  const [logCleaning, setLogCleaning] = useState(false);

  // Nodes Apply
  const [nodesApplying, setNodesApplying] = useState(false);

  // MaxMind
  const [maxmindKey, setMaxmindKey] = useState("");
  const [maxmindHasKey, setMaxmindHasKey] = useState(false);
  const [maxmindDBReady, setMaxmindDBReady] = useState(false);
  const [maxmindSaving, setMaxmindSaving] = useState(false);
  const [maxmindDownloading, setMaxmindDownloading] = useState(false);
  const [maxmindShowKey, setMaxmindShowKey] = useState(false);

  // 账户安全 — 修改密码
  const [credUsername, setCredUsername] = useState("");
  const [credOldPassword, setCredOldPassword] = useState("");
  const [credNewPassword, setCredNewPassword] = useState("");
  const [credConfirmPassword, setCredConfirmPassword] = useState("");
  const [credSaving, setCredSaving] = useState(false);
  const [credMsg, setCredMsg] = useState<string | null>(null);

  // ── Auth redirect helper ─────────────────────────────────────

  const handleAuthError = useAuthErrorHandler();

  const fetchDbStats = useCallback(() => {
    setDbStatsLoading(true);
    api
      .get<DBStats>("/system/db/stats")
      .then(setDbStats)
      .catch((err) => { if (handleAuthError(err)) return; })
      .finally(() => setDbStatsLoading(false));
  }, [handleAuthError]);

  // ── Fetch certificate (removed in gRPC push model) ───────────

  useEffect(() => {
    fetchDbStats();
    fetchStripeSettings();
    fetchShopSettings();
    fetchBarkAlert();
    fetchBackupSettings();
    fetchCFToken();
    fetchMaxmindStatus();
    api.get<{ uptime_retain_days: number; daily_retain_days: number }>("/settings/logs")
      .then((d) => { setLogUptimeDays(d.uptime_retain_days); setLogDailyDays(d.daily_retain_days); })
      .catch((err) => { if (handleAuthError(err)) return; })
      .finally(() => setLogRetentionLoading(false));
  }, [fetchDbStats]);

  function fetchCFToken() {
    api
      .get<{ has_token: boolean; token: string }>("/settings/cf-token")
      .then((d) => { setCfHasToken(d.has_token); setCfToken(d.token ?? ""); })
      .catch((err) => { if (handleAuthError(err)) return; });
  }

  function saveCFToken() {
    setCfSaving(true);
    api
      .put<{ ok: boolean }>("/settings/cf-token", { token: cfToken })
      .then(() => {
        setCfHasToken(cfToken !== "");
        toast("Cloudflare Token 已保存", "success");
      })
      .catch((err) => {
        if (handleAuthError(err)) return;
        toast(err instanceof Error ? err.message : "保存失败", "error");
      })
      .finally(() => setCfSaving(false));
  }

  // ── MaxMind ───────────────────────────────────────────────────

  function fetchMaxmindStatus() {
    api
      .get<{ has_key: boolean; key: string; db_ready: boolean }>("/settings/maxmind")
      .then((d) => { setMaxmindHasKey(d.has_key); setMaxmindKey(d.key ?? ""); setMaxmindDBReady(d.db_ready); })
      .catch((err) => { if (handleAuthError(err)) return; });
  }

  function saveMaxmindKey() {
    setMaxmindSaving(true);
    api
      .put<{ ok: boolean }>("/settings/maxmind", { key: maxmindKey })
      .then(() => {
        setMaxmindHasKey(maxmindKey !== "");
        toast("MaxMind License Key 已保存，开始下载数据库…", "success");
        downloadMaxmindDB();
      })
      .catch((err) => { if (handleAuthError(err)) return; toast(err instanceof Error ? err.message : "保存失败", "error"); })
      .finally(() => setMaxmindSaving(false));
  }

  function downloadMaxmindDB() {
    setMaxmindDownloading(true);
    api
      .post<{ ok: boolean }>("/system/geoip/download", {})
      .then(() => {
        toast("数据库下载已开始，请稍候…", "success");
        // 轮询 db_ready
        const poll = setInterval(() => {
          api.get<{ has_key: boolean; db_ready: boolean }>("/settings/maxmind")
            .then((d) => {
              if (d.db_ready) {
                setMaxmindDBReady(true);
                setMaxmindDownloading(false);
                toast("GeoIP 数据库已就绪", "success");
                clearInterval(poll);
              }
            })
            .catch(() => {});
        }, 3000);
        setTimeout(() => { clearInterval(poll); setMaxmindDownloading(false); }, 120000);
      })
      .catch((err) => { if (handleAuthError(err)) return; toast(err instanceof Error ? err.message : "下载失败", "error"); setMaxmindDownloading(false); });
  }

  // ── Stripe settings ───────────────────────────────────────────

  function fetchStripeSettings() {
    setStripeLoading(true);
    api
      .get<{
        mode: "test" | "live";
        test: { secret_key: string; webhook_secret: string };
        live: { secret_key: string; webhook_secret: string };
      }>("/settings/stripe")
      .then((d) => {
        setStripeKeys({ test: d.test, live: d.live });
      })
      .catch((err) => { if (handleAuthError(err)) return; })
      .finally(() => setStripeLoading(false));
  }

  function saveStripeSettings() {
    setStripeSaving(true);
    setStripeMsg(null);
    api
      .put<{ saved: boolean }>("/settings/stripe", {
        test_secret_key: stripeKeys.test.secret_key,
        test_webhook_secret: stripeKeys.test.webhook_secret,
        live_secret_key: stripeKeys.live.secret_key,
        live_webhook_secret: stripeKeys.live.webhook_secret,
      })
      .then(() => setStripeMsg("已保存，立即生效"))
      .catch((err) => { if (handleAuthError(err)) return; setStripeMsg("保存失败"); })
      .finally(() => { setStripeSaving(false); setTimeout(() => setStripeMsg(null), 3000); });
  }

  // ── Shop settings ─────────────────────────────────────────────

  function fetchShopSettings() {
    setShopLoading(true);
    api
      .get<{ base_url: string; renewal_only?: boolean }>("/settings/shop")
      .then((d) => {
        setShopBaseURL(d.base_url ?? "");
        setShopRenewalOnly(!!d.renewal_only);
      })
      .catch((err) => { if (handleAuthError(err)) return; })
      .finally(() => setShopLoading(false));
  }

  function saveShopSettings() {
    setShopSaving(true);
    setShopMsg(null);
    api
      .put<{ saved: boolean }>("/settings/shop", { base_url: shopBaseURL })
      .then(() => setShopMsg("已保存"))
      .catch((err) => { if (handleAuthError(err)) return; setShopMsg("保存失败"); })
      .finally(() => { setShopSaving(false); setTimeout(() => setShopMsg(null), 2500); });
  }
  function toggleShopRenewalOnly(next: boolean) {
    const prev = shopRenewalOnly;
    setShopRenewalOnly(next);
    setShopRenewalSaving(true);
    api
      .put<{ saved: boolean }>("/settings/shop", { renewal_only: next })
      .then(() => toast.success(next ? "已开启仅续费模式" : "已恢复新用户购买"))
      .catch((err) => {
        setShopRenewalOnly(prev);
        if (handleAuthError(err)) return;
        toast.error("保存失败");
      })
      .finally(() => setShopRenewalSaving(false));
  }

  // ── Fetch Bark alert settings ──────────────────────────────────

  function fetchBarkAlert() {
    setBarkLoading(true);
    api
      .get<{ bark_url: string }>("/settings/alert")
      .then((data) => {
        setBarkURL(data.bark_url);
      })
      .catch((err) => {
        if (handleAuthError(err)) return;
      })
      .finally(() => setBarkLoading(false));
  }

  function saveBarkAlert() {
    setBarkSaving(true);
    setBarkMsg(null);
    api
      .put<{ saved: boolean }>("/settings/alert", { bark_url: barkURL })
      .then(() => setBarkMsg("保存成功"))
      .catch((err) => {
        if (handleAuthError(err)) return;
        setBarkMsg(err instanceof Error ? err.message : "保存失败");
      })
      .finally(() => {
        setBarkSaving(false);
        setTimeout(() => setBarkMsg(null), 2500);
      });
  }

  function testBarkAlert() {
    setBarkTesting(true);
    setBarkMsg(null);
    api
      .post<{ sent?: boolean; error?: string }>("/settings/alert/test", {})
      .then((data) => {
        if (data.error) {
          setBarkMsg(data.error);
        } else {
          setBarkMsg("推送成功");
        }
      })
      .catch((err) => {
        if (handleAuthError(err)) return;
        setBarkMsg(err instanceof Error ? err.message : "推送失败");
      })
      .finally(() => {
        setBarkTesting(false);
        setTimeout(() => setBarkMsg(null), 2500);
      });
  }

  // ── Backup settings ──────────────────────────────────────────

  function fetchBackupSettings() {
    setBackupLoading(true);
    api
      .get<{ account_id: string; access_key_id: string; bucket_name: string; interval_hours: string; keep_count: string; last_at: string }>("/settings/backup")
      .then((d) => {
        setBackupAccountID(d.account_id ?? "");
        setBackupAccessKeyID(d.access_key_id ?? "");
        setBackupBucketName(d.bucket_name ?? "");
        setBackupIntervalHours(d.interval_hours ?? "");
        setBackupKeepCount(d.keep_count ?? "");
        setBackupLastAt(d.last_at ?? "");
      })
      .catch((err) => { if (handleAuthError(err)) return; })
      .finally(() => setBackupLoading(false));
  }

  function saveBackupSettings() {
    setBackupSaving(true);
    setBackupMsg(null);
    const body: Record<string, string> = {
      account_id: backupAccountID,
      access_key_id: backupAccessKeyID,
      bucket_name: backupBucketName,
      interval_hours: backupIntervalHours,
      keep_count: backupKeepCount,
    };
    if (backupSecretKey) body.secret_key = backupSecretKey;
    api
      .put<{ saved: boolean }>("/settings/backup", body)
      .then(() => { setBackupMsg("已保存"); setBackupSecretKey(""); })
      .catch((err) => { if (handleAuthError(err)) return; setBackupMsg("保存失败"); })
      .finally(() => { setBackupSaving(false); setTimeout(() => setBackupMsg(null), 2500); });
  }

  function runBackup() {
    setBackupRunning(true);
    setBackupMsg(null);
    api
      .post<{ ok: boolean; last_at: string }>("/settings/backup/run", {})
      .then((d) => { setBackupLastAt(d.last_at ?? ""); setBackupMsg("备份成功"); })
      .catch((err) => { if (handleAuthError(err)) return; setBackupMsg("备份失败"); })
      .finally(() => { setBackupRunning(false); setTimeout(() => setBackupMsg(null), 3000); });
  }

  function openRestoreDialog() {
    setRestoreOpen(true);
    setRestoreMsg(null);
    setRestoreListError(null);
    setRestoreListLoading(true);
    api
      .get<{ backups: { key: string; last_modified: string; size: number }[] }>("/settings/backup/list")
      .then((d) => { setRestoreList(d.backups ?? []); })
      .catch((err) => { if (handleAuthError(err)) return; setRestoreListError("加载备份列表失败"); })
      .finally(() => { setRestoreListLoading(false); });
  }

  function doRestore(key: string) {
    setRestoringKey(key);
    setRestoreMsg(null);
    api
      .post<{ ok: boolean }>("/settings/backup/restore", { key })
      .then(() => { setRestoreMsg("还原文件已准备，重启服务器后生效"); })
      .catch((err) => { if (handleAuthError(err)) return; setRestoreMsg("还原失败，请重试"); })
      .finally(() => { setRestoringKey(null); });
  }

  // ── 修改管理员凭据 ───────────────────────────────────────────────

  async function saveCredentials() {
    setCredMsg(null);
    if (!credOldPassword || !credNewPassword) {
      setCredMsg("旧密码和新密码不能为空");
      return;
    }
    if (credNewPassword !== credConfirmPassword) {
      setCredMsg("两次新密码不一致");
      return;
    }
    if (credNewPassword.length < 6) {
      setCredMsg("新密码至少 6 位");
      return;
    }
    setCredSaving(true);
    try {
      const body: Record<string, string> = {
        old_password: credOldPassword,
        new_password: credNewPassword,
      };
      if (credUsername.trim()) body.username = credUsername.trim();
      await api.put("/auth/credentials", body);
      toast("密码已更新，下次登录请使用新密码", "success");
      setCredOldPassword("");
      setCredNewPassword("");
      setCredConfirmPassword("");
      setCredUsername("");
      setCredMsg("保存成功");
    } catch (err) {
      if (handleAuthError(err as Error)) return;
      setCredMsg(err instanceof Error ? err.message : "保存失败");
    } finally {
      setCredSaving(false);
    }
  }

  // ── Render ───────────────────────────────────────────────────

  return (
    <div className="p-4 sm:p-6 lg:p-8">
      <h1 className="mb-6 text-2xl font-bold text-[hsl(var(--foreground))]">
        系统设置
      </h1>

      <Tabs defaultValue="general" className="w-full">
        <TabsList className="mb-6">
          <TabsTrigger value="general">常规</TabsTrigger>
          <TabsTrigger value="integrations">集成</TabsTrigger>
          <TabsTrigger value="database">数据库</TabsTrigger>
        </TabsList>

        {/* ── 常规 Tab ──────────────────────────────────────────── */}
        <TabsContent value="general" className="grid gap-6 mt-0">
          <Card>
            <CardHeader>
              <CardTitle className="text-base font-medium">告警推送</CardTitle>
            </CardHeader>
            <Separator />
            <CardContent className="pt-4">
              {barkLoading ? (
                <div className="space-y-3">
                  <div className="h-5 w-24 animate-pulse rounded bg-[hsl(var(--muted))]" />
                  <div className="h-9 w-full animate-pulse rounded bg-[hsl(var(--muted))]" />
                </div>
              ) : (
                <div className="space-y-4">
                  <div className="space-y-2">
                    <Label htmlFor="bark-url" className="text-sm">
                      Bark URL
                    </Label>
                    <Input
                      id="bark-url"
                      value={barkURL}
                      onChange={(e) => setBarkURL(e.target.value)}
                      placeholder="https://api.day.app/your-device-key"
                    />
                  </div>

                  <div className="flex items-center gap-3">
                    <Button
                      size="sm"
                      onClick={saveBarkAlert}
                      disabled={barkSaving}
                    >
                      {barkSaving ? "保存中…" : "保存设置"}
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={testBarkAlert}
                      disabled={barkTesting}
                    >
                      {barkTesting ? "推送中…" : "测试推送"}
                    </Button>
                    {barkMsg && (
                      <span className="text-sm text-[hsl(var(--muted-foreground))]">
                        {barkMsg}
                      </span>
                    )}
                  </div>

                  <p className="text-xs text-[hsl(var(--muted-foreground))]">
                    配置 Bark URL 后，当节点离线或出现异常时将自动发送推送通知到你的设备。
                  </p>
                </div>
              )}
            </CardContent>
          </Card>

          {/* 账户安全 */}
          <Card>
            <CardHeader>
              <CardTitle className="text-base font-medium">账户安全</CardTitle>
            </CardHeader>
            <Separator />
            <CardContent className="pt-4 space-y-4">
              <p className="text-sm text-[hsl(var(--muted-foreground))]">
                修改管理员登录用户名或密码。旧密码为必填项；用户名留空时仅修改密码。
              </p>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label className="text-sm">新用户名（可选）</Label>
                  <Input
                    type="text"
                    autoComplete="username"
                    placeholder="留空则不修改"
                    value={credUsername}
                    onChange={(e) => setCredUsername(e.target.value)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label className="text-sm">旧密码</Label>
                  <Input
                    type="password"
                    autoComplete="current-password"
                    placeholder="••••••••"
                    value={credOldPassword}
                    onChange={(e) => setCredOldPassword(e.target.value)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label className="text-sm">新密码</Label>
                  <Input
                    type="password"
                    autoComplete="new-password"
                    placeholder="••••••••"
                    value={credNewPassword}
                    onChange={(e) => setCredNewPassword(e.target.value)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label className="text-sm">确认新密码</Label>
                  <Input
                    type="password"
                    autoComplete="new-password"
                    placeholder="••••••••"
                    value={credConfirmPassword}
                    onChange={(e) => setCredConfirmPassword(e.target.value)}
                  />
                </div>
              </div>
              <div className="flex items-center gap-3">
                <Button size="sm" onClick={saveCredentials} disabled={credSaving}>
                  {credSaving ? "保存中…" : "保存"}
                </Button>
                {credMsg && (
                  <span className="text-sm text-[hsl(var(--muted-foreground))]">{credMsg}</span>
                )}
              </div>
            </CardContent>
          </Card>

        </TabsContent>

        {/* ── 集成 Tab ──────────────────────────────────────────── */}
        <TabsContent value="integrations" className="grid gap-6 mt-0">
          <Card>
            <CardHeader>
              <CardTitle className="text-base font-medium">MaxMind GeoIP</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <p className="text-sm text-[hsl(var(--muted-foreground))]">
                用于分析节点 IP 的地理位置和 ASN 信息（GeoLite2）。
                {maxmindDBReady && <span className="ml-1 text-green-600">（数据库已就绪）</span>}
                {maxmindHasKey && !maxmindDBReady && maxmindDownloading && <span className="ml-1 text-amber-500">（数据库下载中…）</span>}
                {maxmindHasKey && !maxmindDBReady && !maxmindDownloading && <span className="ml-1 text-amber-500">（数据库未就绪，可点「更新数据库」重试）</span>}
              </p>
              <div className="flex gap-2">
                <div className="relative flex-1">
                  <Input
                    type={maxmindShowKey ? "text" : "password"}
                    placeholder="MaxMind License Key"
                    value={maxmindKey}
                    onChange={(e) => setMaxmindKey(e.target.value)}
                    className="font-mono text-sm pr-9"
                  />
                  <button
                    type="button"
                    onClick={() => setMaxmindShowKey((v) => !v)}
                    className="absolute right-2 top-1/2 -translate-y-1/2 text-[hsl(var(--muted-foreground))] hover:text-[hsl(var(--foreground))]"
                    tabIndex={-1}
                  >
                    {maxmindShowKey ? (
                      <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13.875 18.825A10.05 10.05 0 0112 19c-4.478 0-8.268-2.943-9.543-7a9.97 9.97 0 011.563-3.029m5.858.908a3 3 0 114.243 4.243M9.878 9.878l4.242 4.242M9.88 9.88l-3.29-3.29m7.532 7.532l3.29 3.29M3 3l3.59 3.59m0 0A9.953 9.953 0 0112 5c4.478 0 8.268 2.943 9.543 7a10.025 10.025 0 01-4.132 5.411m0 0L21 21" />
                      </svg>
                    ) : (
                      <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z" />
                      </svg>
                    )}
                  </button>
                </div>
                <Button size="sm" onClick={saveMaxmindKey} disabled={maxmindSaving}>
                  {maxmindSaving ? "保存中…" : "保存"}
                </Button>
              </div>
              {maxmindHasKey && (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={downloadMaxmindDB}
                  disabled={maxmindDownloading}
                >
                  {maxmindDownloading ? "下载中…" : maxmindDBReady ? "更新数据库" : "下载数据库"}
                </Button>
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="text-base font-medium">Cloudflare API Token</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <p className="text-sm text-[hsl(var(--muted-foreground))]">
                用于 NodeGate DNS-01 证书申请（NAT / 无公网 80/443 的机器）。
                需要 Cloudflare API Token 具备 <code className="font-mono text-xs">Zone:DNS:Edit</code> 权限。
                {cfHasToken && <span className="ml-1 text-green-600">（已配置）</span>}
              </p>
              <div className="flex gap-2">
                <div className="relative flex-1">
                  <Input
                    type={cfShowToken ? "text" : "password"}
                    placeholder="CF API Token"
                    value={cfToken}
                    onChange={(e) => setCfToken(e.target.value)}
                    className="font-mono text-sm pr-9"
                  />
                  <button
                    type="button"
                    onClick={() => setCfShowToken((v) => !v)}
                    className="absolute right-2 top-1/2 -translate-y-1/2 text-[hsl(var(--muted-foreground))] hover:text-[hsl(var(--foreground))]"
                    tabIndex={-1}
                  >
                    {cfShowToken ? (
                      <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13.875 18.825A10.05 10.05 0 0112 19c-4.478 0-8.268-2.943-9.543-7a9.97 9.97 0 011.563-3.029m5.858.908a3 3 0 114.243 4.243M9.878 9.878l4.242 4.242M9.88 9.88l-3.29-3.29m7.532 7.532l3.29 3.29M3 3l3.59 3.59m0 0A9.953 9.953 0 0112 5c4.478 0 8.268 2.943 9.543 7a10.025 10.025 0 01-4.132 5.411m0 0L21 21" />
                      </svg>
                    ) : (
                      <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z" />
                      </svg>
                    )}
                  </button>
                </div>
                <Button size="sm" onClick={saveCFToken} disabled={cfSaving}>
                  {cfSaving ? "保存中…" : "保存"}
                </Button>
              </div>
              <p className="text-xs text-[hsl(var(--muted-foreground))]">
                保存后，点击 NodeGate 页的「同步路由」即可重新下发节点配置。
              </p>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="text-base font-medium">Stripe 密钥</CardTitle>
            </CardHeader>
            <Separator />
            <CardContent className="pt-4 space-y-4">
              {stripeLoading ? (
                <div className="space-y-3">
                  <div className="h-9 w-full animate-pulse rounded bg-[hsl(var(--muted))]" />
                  <div className="h-9 w-full animate-pulse rounded bg-[hsl(var(--muted))]" />
                </div>
              ) : (
                <>
                  <p className="text-sm text-[hsl(var(--muted-foreground))]">
                    密钥保存在数据库中，保存后立即生效，无需重启。
                  </p>

                  {/* 两套密钥，直接展开 */}
                  {(["live", "test"] as const).map((env) => (
                    <div key={env} className="space-y-3">
                      <p className="text-sm font-medium text-[hsl(var(--foreground))]">
                        {env === "live" ? "生产密钥" : "沙盒密钥"}
                      </p>
                      {(["secret_key", "webhook_secret"] as const).map((field) => {
                        const label = field === "secret_key" ? "Secret Key" : "Webhook Secret";
                        const placeholder = field === "secret_key"
                          ? env === "test" ? "sk_test_..." : "sk_live_..."
                          : "whsec_...";
                        const visible = stripeShow[env][field];
                        return (
                          <div key={field} className="space-y-1.5">
                            <Label className="text-sm">{label}</Label>
                            <div className="relative">
                              <Input
                                type={visible ? "text" : "password"}
                                value={stripeKeys[env][field]}
                                onChange={(e) =>
                                  setStripeKeys((prev) => ({
                                    ...prev,
                                    [env]: { ...prev[env], [field]: e.target.value },
                                  }))
                                }
                                placeholder={placeholder}
                                className="font-mono text-sm pr-10"
                              />
                              <button
                                type="button"
                                onClick={() =>
                                  setStripeShow((prev) => ({
                                    ...prev,
                                    [env]: { ...prev[env], [field]: !prev[env][field] },
                                  }))
                                }
                                className="absolute right-2 top-1/2 -translate-y-1/2 text-[hsl(var(--muted-foreground))] hover:text-[hsl(var(--foreground))]"
                                tabIndex={-1}
                              >
                                {visible ? (
                                  <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13.875 18.825A10.05 10.05 0 0112 19c-4.478 0-8.268-2.943-9.543-7a9.97 9.97 0 011.563-3.029m5.858.908a3 3 0 114.243 4.243M9.878 9.878l4.242 4.242M9.88 9.88l-3.29-3.29m7.532 7.532l3.29 3.29M3 3l3.59 3.59m0 0A9.953 9.953 0 0112 5c4.478 0 8.268 2.943 9.543 7a10.025 10.025 0 01-4.132 5.411m0 0L21 21" />
                                  </svg>
                                ) : (
                                  <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
                                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z" />
                                  </svg>
                                )}
                              </button>
                            </div>
                          </div>
                        );
                      })}
                    </div>
                  ))}

                  <div className="flex items-center gap-3">
                    <Button size="sm" onClick={saveStripeSettings} disabled={stripeSaving}>
                      {stripeSaving ? "保存中…" : "保存"}
                    </Button>
                    {stripeMsg && (
                      <span className="text-sm text-[hsl(var(--muted-foreground))]">{stripeMsg}</span>
                    )}
                  </div>
                </>
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="text-base font-medium">Shop 设置</CardTitle>
            </CardHeader>
            <Separator />
            <CardContent className="pt-4">
              {shopLoading ? (
                <div className="h-9 w-full animate-pulse rounded bg-[hsl(var(--muted))]" />
              ) : (
                <div className="space-y-4">
                  <div className="flex items-start justify-between gap-4 rounded-lg border border-[hsl(var(--border))] px-4 py-3">
                    <div className="space-y-1">
                      <Label htmlFor="shop-renewal-only" className="text-sm">仅允许续费</Label>
                      <p className="text-xs text-[hsl(var(--muted-foreground))]">
                        开启后 Shop 页面禁止新用户购买，只有持有效订阅链接的已有用户可以续费。
                      </p>
                    </div>
                    <Switch
                      id="shop-renewal-only"
                      checked={shopRenewalOnly}
                      onCheckedChange={toggleShopRenewalOnly}
                      disabled={shopRenewalSaving}
                    />
                  </div>
                  <Separator />
                  <p className="text-sm text-[hsl(var(--muted-foreground))]">
                    Stripe 付款后的回调跳转地址。填写站点的公开域名，例如 <code className="rounded bg-[hsl(var(--muted))] px-1 font-mono text-xs">https://example.com</code>。
                  </p>
                  <div className="space-y-2">
                    <Label className="text-sm">站点地址（Base URL）</Label>
                    <Input
                      value={shopBaseURL}
                      onChange={(e) => setShopBaseURL(e.target.value)}
                      placeholder="https://example.com"
                      className="font-mono text-sm"
                    />
                  </div>
                  <div className="flex items-center gap-3">
                    <Button size="sm" onClick={saveShopSettings} disabled={shopSaving}>
                      {shopSaving ? "保存中…" : "保存"}
                    </Button>
                    {shopMsg && (
                      <span className="text-sm text-[hsl(var(--muted-foreground))]">{shopMsg}</span>
                    )}
                  </div>
                </div>
              )}
            </CardContent>
          </Card>
        </TabsContent>

        {/* ── 数据库 Tab ────────────────────────────────────────── */}
        <TabsContent value="database" className="grid gap-6 mt-0">
          <Card>
            <CardHeader className="flex flex-row items-center justify-between">
              <CardTitle className="text-base font-medium">数据库状态</CardTitle>
              <Button
                variant="ghost"
                size="sm"
                onClick={fetchDbStats}
                disabled={dbStatsLoading}
                className="text-xs text-[hsl(var(--muted-foreground))]"
              >
                {dbStatsLoading ? "加载中…" : "刷新"}
              </Button>
            </CardHeader>
            <Separator />
            <CardContent className="pt-4 space-y-4">
              {dbStatsLoading ? (
                <div className="space-y-2">
                  <div className="h-4 w-3/4 animate-pulse rounded bg-[hsl(var(--muted))]" />
                  <div className="h-4 w-1/2 animate-pulse rounded bg-[hsl(var(--muted))]" />
                </div>
              ) : dbStats ? (
                <>
                  <div className="grid grid-cols-2 gap-x-8 gap-y-1.5 text-sm sm:grid-cols-3">
                    <div className="flex justify-between gap-2">
                      <span className="text-[hsl(var(--muted-foreground))]">数据库大小</span>
                      <span className="font-mono">{dbStats.file_size_bytes >= 1024 * 1024 ? `${(dbStats.file_size_bytes / 1024 / 1024).toFixed(1)} MB` : `${(dbStats.file_size_bytes / 1024).toFixed(1)} KB`}</span>
                    </div>
                    <div className="flex justify-between gap-2">
                      <span className="text-[hsl(var(--muted-foreground))]">Ping 延迟</span>
                      <span className="font-mono">{dbStats.ping_latency_ms.toFixed(2)} ms</span>
                    </div>
                    <div className="flex justify-between gap-2">
                      <span className="text-[hsl(var(--muted-foreground))]">表数量</span>
                      <span className="font-mono">{Object.keys(dbStats.tables).length}</span>
                    </div>
                  </div>
                  {Object.keys(dbStats.tables).length > 0 && (
                    <div className="rounded-md border border-[hsl(var(--border))] overflow-hidden">
                      <div className="grid grid-cols-2 divide-x divide-[hsl(var(--border))] sm:grid-cols-3 lg:grid-cols-4">
                        {Object.entries(dbStats.tables)
                          .sort((a, b) => b[1] - a[1])
                          .map(([tbl, count]) => (
                            <div key={tbl} className="flex items-center justify-between px-3 py-1.5 text-xs border-b border-[hsl(var(--border))]">
                              <span className="text-[hsl(var(--muted-foreground))] truncate mr-2">{tbl}</span>
                              <span className="font-mono shrink-0">{count.toLocaleString()}</span>
                            </div>
                          ))}
                      </div>
                    </div>
                  )}
                </>
              ) : (
                <p className="text-sm text-[hsl(var(--muted-foreground))]">加载失败</p>
              )}
              <div className="flex items-center gap-3 pt-1">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={dbCleaning}
                  onClick={() => {
                    setDbCleaning(true);
                    api.post<{ total: number }>("/system/db/cleanup", {})
                      .then((r) => {
                        toast(r.total > 0 ? `已清理 ${r.total} 条孤立记录` : "数据一致，无需清理", "success");
                        fetchDbStats();
                      })
                      .catch((err) => {
                        if (handleAuthError(err)) return;
                        toast(err instanceof Error ? err.message : "清理失败", "error");
                      })
                      .finally(() => setDbCleaning(false));
                  }}
                >
                  {dbCleaning ? "清理中…" : "清理冗余数据"}
                </Button>
                <p className="text-xs text-[hsl(var(--muted-foreground))]">删除数据库中已无父记录的冗余数据</p>
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="flex flex-row items-center justify-between">
              <div>
                <CardTitle className="text-base font-medium">节点配置下发</CardTitle>
                <p className="text-sm text-[hsl(var(--muted-foreground))] mt-1">强制将当前配置重新推送到所有节点</p>
              </div>
              <Button
                variant="outline"
                size="sm"
                disabled={nodesApplying}
                onClick={() => {
                  setNodesApplying(true);
                  api.post<{ nodes: number }>("/system/nodes/apply", {})
                    .then((r) => {
                      toast(`已向 ${r.nodes} 个节点下发配置`, "success");
                    })
                    .catch((err) => {
                      if (handleAuthError(err)) return;
                      toast(err instanceof Error ? err.message : "下发失败", "error");
                    })
                    .finally(() => setNodesApplying(false));
                }}
              >
                {nodesApplying ? "下发中…" : "重新下发配置"}
              </Button>
            </CardHeader>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="text-base font-medium">日志保留策略</CardTitle>
            </CardHeader>
            <Separator />
            <CardContent className="pt-4 space-y-4">
              {logRetentionLoading ? (
                <div className="space-y-3">
                  <div className="h-9 w-full animate-pulse rounded bg-[hsl(var(--muted))]" />
                  <div className="h-9 w-full animate-pulse rounded bg-[hsl(var(--muted))]" />
                </div>
              ) : (
                <>
                  <p className="text-sm text-[hsl(var(--muted-foreground))]">
                    超出保留期的历史日志每天自动清理一次，也可手动立即执行。
                  </p>
                  <div className="grid grid-cols-2 gap-4">
                    <div className="space-y-1.5">
                      <Label className="text-sm">节点可用性日志保留（天）</Label>
                      <Input
                        type="number"
                        min={1}
                        value={logUptimeDays}
                        onChange={(e) => setLogUptimeDays(Number(e.target.value) || 30)}
                        className="font-mono"
                      />
                      <p className="text-xs text-[hsl(var(--muted-foreground))]">默认 30 天</p>
                    </div>
                    <div className="space-y-1.5">
                      <Label className="text-sm">节点日流量日志保留（天）</Label>
                      <Input
                        type="number"
                        min={1}
                        value={logDailyDays}
                        onChange={(e) => setLogDailyDays(Number(e.target.value) || 180)}
                        className="font-mono"
                      />
                      <p className="text-xs text-[hsl(var(--muted-foreground))]">默认 180 天</p>
                    </div>
                  </div>
                  <div className="flex items-center gap-3">
                    <Button
                      size="sm"
                      disabled={logRetentionSaving}
                      onClick={() => {
                        setLogRetentionSaving(true);
                        api.put("/settings/logs", { uptime_retain_days: logUptimeDays, daily_retain_days: logDailyDays })
                          .then(() => toast("保留策略已保存", "success"))
                          .catch((err) => { if (handleAuthError(err)) return; toast("保存失败", "error"); })
                          .finally(() => setLogRetentionSaving(false));
                      }}
                    >
                      {logRetentionSaving ? "保存中…" : "保存"}
                    </Button>
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={logCleaning}
                      onClick={() => {
                        setLogCleaning(true);
                        api.post<{ uptime_retain_days: number; daily_retain_days: number }>("/system/logs/cleanup", {})
                          .then((r) => toast(`已清理完成（uptime >${r.uptime_retain_days}天，日流量 >${r.daily_retain_days}天）`, "success"))
                          .catch((err) => { if (handleAuthError(err)) return; toast("清理失败", "error"); })
                          .finally(() => setLogCleaning(false));
                      }}
                    >
                      {logCleaning ? "清理中…" : "立即清理"}
                    </Button>
                  </div>
                </>
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="text-base font-medium">数据库备份</CardTitle>
            </CardHeader>
            <Separator />
            <CardContent className="pt-4">
              {backupLoading ? (
                <div className="space-y-3">
                  <div className="h-9 w-full animate-pulse rounded bg-[hsl(var(--muted))]" />
                  <div className="h-9 w-full animate-pulse rounded bg-[hsl(var(--muted))]" />
                </div>
              ) : (
                <div className="space-y-4">
                  <p className="text-sm text-[hsl(var(--muted-foreground))]">
                    定时将数据库备份到 Cloudflare R2（S3 兼容）。
                  </p>
                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                    <div className="space-y-2">
                      <Label className="text-sm">Account ID</Label>
                      <Input value={backupAccountID} onChange={(e) => setBackupAccountID(e.target.value)} placeholder="Cloudflare Account ID" className="font-mono text-sm" />
                    </div>
                    <div className="space-y-2">
                      <Label className="text-sm">Bucket 名称</Label>
                      <Input value={backupBucketName} onChange={(e) => setBackupBucketName(e.target.value)} placeholder="my-pulse-backup" className="font-mono text-sm" />
                    </div>
                    <div className="space-y-2">
                      <Label className="text-sm">Access Key ID</Label>
                      <Input value={backupAccessKeyID} onChange={(e) => setBackupAccessKeyID(e.target.value)} placeholder="R2 Access Key ID" className="font-mono text-sm" />
                    </div>
                    <div className="space-y-2">
                      <Label className="text-sm">Secret Access Key</Label>
                      <Input
                        type="password"
                        value={backupSecretKey}
                        onChange={(e) => setBackupSecretKey(e.target.value)}
                        placeholder={backupAccountID ? "已设置，留空保持不变" : "R2 Secret Key"}
                        className="font-mono text-sm"
                        autoComplete="new-password"
                      />
                    </div>
                  </div>
                  <div className="flex flex-wrap gap-4">
                    <div className="space-y-2 w-44">
                      <Label className="text-sm">备份间隔（小时）</Label>
                      <Input
                        type="number"
                        value={backupIntervalHours}
                        onChange={(e) => setBackupIntervalHours(e.target.value)}
                        placeholder="0 = 禁用"
                        min={0}
                        max={720}
                      />
                    </div>
                    <div className="space-y-2 w-44">
                      <Label className="text-sm">保留份数</Label>
                      <Input
                        type="number"
                        value={backupKeepCount}
                        onChange={(e) => setBackupKeepCount(e.target.value)}
                        placeholder="0 = 不限制"
                        min={0}
                        max={365}
                      />
                    </div>
                  </div>
                  {backupLastAt && (
                    <p className="text-xs text-[hsl(var(--muted-foreground))]">
                      上次备份：{new Date(backupLastAt).toLocaleString("zh-CN")}
                    </p>
                  )}
                  <div className="flex items-center gap-3">
                    <Button size="sm" onClick={saveBackupSettings} disabled={backupSaving}>
                      {backupSaving ? "保存中…" : "保存"}
                    </Button>
                    <Button size="sm" variant="outline" onClick={runBackup} disabled={backupRunning}>
                      {backupRunning ? "备份中…" : "立即备份"}
                    </Button>
                    <Button size="sm" variant="outline" onClick={openRestoreDialog} disabled={!backupAccountID}>
                      从备份还原
                    </Button>
                    {backupMsg && <span className="text-sm text-[hsl(var(--muted-foreground))]">{backupMsg}</span>}
                  </div>
                </div>
              )}
            </CardContent>
          </Card>

          {/* Restore dialog */}
          {restoreOpen && (
            <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60" onClick={() => setRestoreOpen(false)}>
              <div className="w-full max-w-lg rounded-lg border border-[hsl(var(--border))] bg-[hsl(var(--card))] p-6 shadow-xl" onClick={(e) => e.stopPropagation()}>
                <div className="mb-4 flex items-center justify-between">
                  <h2 className="text-base font-semibold">选择备份还原</h2>
                  <button onClick={() => setRestoreOpen(false)} className="text-[hsl(var(--muted-foreground))] hover:text-[hsl(var(--foreground))]">✕</button>
                </div>
                {restoreMsg ? (
                  <div className="space-y-4">
                    <p className="text-sm text-[hsl(var(--muted-foreground))]">{restoreMsg}</p>
                    <Button size="sm" onClick={() => setRestoreOpen(false)}>关闭</Button>
                  </div>
                ) : restoreListLoading ? (
                  <div className="space-y-2">
                    {[1,2,3].map(i => <div key={i} className="h-10 animate-pulse rounded bg-[hsl(var(--muted))]" />)}
                  </div>
                ) : restoreListError ? (
                  <p className="text-sm text-[hsl(var(--destructive))]">{restoreListError}</p>
                ) : restoreList.length === 0 ? (
                  <p className="text-sm text-[hsl(var(--muted-foreground))]">R2 中暂无备份文件</p>
                ) : (
                  <ScrollArea className="max-h-80">
                    <div className="space-y-1">
                    {restoreList.map((b) => (
                      <div key={b.key} className="flex items-center justify-between rounded-md border border-[hsl(var(--border))] px-3 py-2">
                        <div>
                          <p className="text-sm font-mono">{b.key}</p>
                          <p className="text-xs text-[hsl(var(--muted-foreground))]">
                            {new Date(b.last_modified).toLocaleString("zh-CN")} · {(b.size / 1024 / 1024).toFixed(1)} MB
                          </p>
                        </div>
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => doRestore(b.key)}
                          disabled={restoringKey !== null}
                        >
                          {restoringKey === b.key ? "还原中…" : "还原"}
                        </Button>
                      </div>
                    ))}
                    </div>
                  </ScrollArea>
                )}
              </div>
            </div>
          )}

        </TabsContent>
      </Tabs>
    </div>
  );
}
