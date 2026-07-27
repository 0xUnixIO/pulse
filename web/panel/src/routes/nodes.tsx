import { useEffect, useState, useCallback, useRef, useMemo } from "react";
import { Link } from "@tanstack/react-router";
import {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
  CardDescription,
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
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  Separator,
  ConfirmDialog,
  JsonViewer,
  toast,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
} from "@/components/ui";
import { ScrollArea } from "@/components/ui/scroll-area";
import { api } from "@/lib/api";
import { copyText } from "@/lib/clipboard";
import { getToken } from "@/lib/auth";
import { useAuthErrorHandler } from "@/hooks/useAuthErrorHandler";
import { formatBytes, formatSpeed } from "@/lib/format";
import type { Node, NodesResponse, CreateNodeRequest } from "@/lib/types";

type DetailMode = "status" | "config" | "logs" | null;

interface RuntimeInfo {
  available: boolean;
  version?: string;      // xray 版本
  node_version?: string; // pulse-node 版本
  last_error?: string;
}

interface SpeedtestResult {
  down_bps: number;
  up_bps: number;
  tested_at: string;
}

interface CheckItem {
  service: string;
  unlocked: boolean;
  region?: string;
  note?: string;
}

interface CheckResult {
  direct: CheckItem[];
  proxied?: CheckItem[];
  proxy_available: boolean;
}

interface NodeMetrics {
  node_id: string;
  running: boolean;
  upload_speed: number;   // bytes/s
  download_speed: number; // bytes/s
  connections: number;
}

interface TracerouteHop {
  hop: number;
  ip?: string;
  rtt_ms?: number[];
  timeout?: boolean;
}

interface TracerouteResult {
  id: string;
  node_id: string;
  direction: "inbound" | "outbound";
  target: string;
  hops: string;
  quality: string;
  created_at: string;
}

function traceQualityColor(quality: string): string {
  if (quality === "CN2 GIA") return "text-emerald-600 bg-emerald-500/10";
  if (quality === "CN2 GT")  return "text-blue-600 bg-blue-500/10";
  return "text-amber-600 bg-amber-500/10";
}

function traceFormatTime(iso: string): string {
  try {
    return new Date(iso).toLocaleString("zh-CN", {
      month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit",
    });
  } catch { return iso; }
}

// ── Icons (inline SVG) ───────────────────────────────────────────

function IconPlus({ className }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <line x1="12" y1="5" x2="12" y2="19" />
      <line x1="5" y1="12" x2="19" y2="12" />
    </svg>
  );
}

function IconMore({ className }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <circle cx="12" cy="12" r="1" />
      <circle cx="12" cy="5" r="1" />
      <circle cx="12" cy="19" r="1" />
    </svg>
  );
}

function IconUpload({ className }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <polyline points="17 11 12 6 7 11" />
      <line x1="12" y1="6" x2="12" y2="18" />
    </svg>
  );
}

function IconDownload({ className }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <polyline points="7 13 12 18 17 13" />
      <line x1="12" y1="18" x2="12" y2="6" />
    </svg>
  );
}

function IconServer({ className }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <rect x="2" y="2" width="20" height="8" rx="2" ry="2" />
      <rect x="2" y="14" width="20" height="8" rx="2" ry="2" />
      <line x1="6" y1="6" x2="6.01" y2="6" />
      <line x1="6" y1="18" x2="6.01" y2="18" />
    </svg>
  );
}

function IconAlert({ className }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <circle cx="12" cy="12" r="10" />
      <line x1="12" y1="8" x2="12" y2="12" />
      <line x1="12" y1="16" x2="12.01" y2="16" />
    </svg>
  );
}

function IconRefresh({ className }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <polyline points="23 4 23 10 17 10" />
      <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
    </svg>
  );
}

// ── Skeleton ─────────────────────────────────────────────────────

function SkeletonCard() {
  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex items-center justify-between">
          <div className="h-5 w-32 animate-pulse rounded bg-[hsl(var(--muted))]" />
          <div className="h-5 w-14 animate-pulse rounded bg-[hsl(var(--muted))]" />
        </div>
      </CardHeader>
      <CardContent>
        <div className="h-4 w-48 animate-pulse rounded bg-[hsl(var(--muted))]" />
        <Separator className="my-3" />
        <div className="flex gap-6">
          <div className="h-4 w-20 animate-pulse rounded bg-[hsl(var(--muted))]" />
          <div className="h-4 w-20 animate-pulse rounded bg-[hsl(var(--muted))]" />
        </div>
      </CardContent>
    </Card>
  );
}

// ── Create / Edit Dialog ─────────────────────────────────────────

interface NodeFormDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  node?: Node | null;
  onSubmit: (data: CreateNodeRequest) => Promise<void>;
  submitting: boolean;
}

function NodeFormDialog({ open, onOpenChange, node, onSubmit, submitting }: NodeFormDialogProps) {
  const [name, setName] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [expireAt, setExpireAt] = useState("");
  const [panelURL, setPanelURL] = useState("");
  const [remark, setRemark] = useState("");
  const [ipOverride, setIpOverride] = useState("");
  const [disabled, setDisabled] = useState(false);
  const [isLanding, setIsLanding] = useState(true);

  useEffect(() => {
    if (open) {
      setName(node?.name ?? "");
      setBaseUrl(node?.base_url ?? "");
      setExpireAt(node?.expire_at ? node.expire_at.slice(0, 10) : "");
      setPanelURL(node?.panel_url ?? "");
      setRemark(node?.remark ?? "");
      setIpOverride(node?.ip_override ?? "");
      setDisabled(node?.disabled ?? false);
      setIsLanding(node?.is_landing ?? true);
    }
  }, [open, node]);

  const isEdit = !!node;
  const canSubmit = name.trim() !== "" && (!isEdit || baseUrl.trim() !== "");

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canSubmit || submitting) return;
    await onSubmit({
      name: name.trim(),
      ...(isEdit ? { base_url: baseUrl.trim() } : {}),
      expire_at: expireAt || null,
      panel_url: panelURL.trim(),
      remark: remark.trim(),
      ip_override: ipOverride.trim(),
      disabled,
      is_landing: isLanding,
    });
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={handleSubmit}>
          <DialogHeader>
            <DialogTitle>{isEdit ? "编辑节点" : "添加节点"}</DialogTitle>
            <DialogDescription>
              {isEdit ? "修改节点的名称和地址。" : "添加后将生成安装命令，在节点机器上运行即可自动连接。"}
            </DialogDescription>
          </DialogHeader>

          <div className="mt-4 space-y-4">
            <div className="space-y-2">
              <Label htmlFor="node-name">节点名称</Label>
              <Input
                id="node-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="DE-Frankfurt-01"
                required
                autoFocus
              />
            </div>
            {isEdit && (
              <div className="space-y-2">
                <Label htmlFor="node-url">地址</Label>
                <Input
                  id="node-url"
                  value={baseUrl}
                  onChange={(e) => setBaseUrl(e.target.value)}
                  placeholder="https://ip:8081"
                  required
                />
              </div>
            )}
            <div className="space-y-2">
              <Label htmlFor="node-expire">到期日期</Label>
              <Input
                id="node-expire"
                type="date"
                value={expireAt}
                onChange={(e) => setExpireAt(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="node-panel">控制面板地址</Label>
              <Input
                id="node-panel"
                value={panelURL}
                onChange={(e) => setPanelURL(e.target.value)}
                placeholder="https://my.host.com"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="node-remark">备注</Label>
              <Input
                id="node-remark"
                value={remark}
                onChange={(e) => setRemark(e.target.value)}
                placeholder="CN2 GIA · 2C4G"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="node-ip-override">GeoIP 地址（可选）</Label>
              <Input
                id="node-ip-override"
                value={ipOverride}
                onChange={(e) => setIpOverride(e.target.value)}
                placeholder="留空则从 base_url 解析，内网地址时填写公网 IP"
                className="font-mono text-sm"
              />
            </div>
            {isEdit && (
              <>
                <div className="flex items-center justify-between rounded-lg border px-3 py-2">
                  <div>
                    <p className="text-sm font-medium">落地机</p>
                    <p className="text-xs text-muted-foreground">落地机不采集流量审计、不做延迟检测和路由追踪</p>
                  </div>
                  <Switch
                    id="node-is-landing"
                    checked={isLanding}
                    onCheckedChange={setIsLanding}
                  />
                </div>
                <div className="flex items-center justify-between rounded-lg border px-3 py-2">
                  <div>
                    <p className="text-sm font-medium">禁用节点</p>
                    <p className="text-xs text-muted-foreground">禁用后停止流量同步和配置下发，保留所有配置</p>
                  </div>
                  <Switch
                    id="node-disabled"
                    checked={disabled}
                    onCheckedChange={setDisabled}
                  />
                </div>
              </>
            )}
          </div>

          <DialogFooter className="mt-6">
            <DialogClose asChild>
              <Button type="button" variant="outline" disabled={submitting}>
                取消
              </Button>
            </DialogClose>
            <Button type="submit" disabled={!canSubmit || submitting}>
              {submitting ? "保存中…" : isEdit ? "保存" : "添加"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// ── Install Command Dialog ───────────────────────────────────────

interface EnrollTokenResponse {
  token: string;
  expires_at: string;
  install_command: string;
  manual_command: string;
  server_url: string;
}

function InstallCmdDialog({
  node,
  open,
  onClose,
}: {
  node: Node | null;
  open: boolean;
  onClose: () => void;
}) {
  const [enroll, setEnroll] = useState<EnrollTokenResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [registered, setRegistered] = useState(false);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    if (!open || !node) return;
    setEnroll(null);
    setError(null);
    setRegistered(false);
    api
      .post<EnrollTokenResponse>(`/nodes/${node.id}/enroll-token`, {})
      .then(setEnroll)
      .catch((e: Error) => setError(e.message || "生成安装 token 失败"));
  }, [open, node]);

  useEffect(() => {
    if (!open || !node || registered) return;
    pollRef.current = setInterval(() => {
      api.get<Node>(`/nodes/${node.id}`)
        .then((n) => { if (n.online) setRegistered(true); })
        .catch(() => {});
    }, 3000);
    return () => { if (pollRef.current) clearInterval(pollRef.current); };
  }, [open, node, registered]);

  const installCmd = enroll?.install_command ?? (error ?? "正在生成安装 token…");

  const handleCopy = async (text: string, container?: HTMLElement | null) => {
    if (!text) return;
    try {
      await copyText(text, container);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // 复制失败静默处理，clipboard.ts 已覆盖 HTTP fallback
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>安装命令</DialogTitle>
          <DialogDescription>
            在目标节点机器上以 root 权限运行以下命令，节点将自动安装并连接到控制面板。Token 1 小时内有效。
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <ScrollArea className="h-36 rounded-md border bg-[hsl(var(--muted))]">
            <pre className="whitespace-pre-wrap break-all p-3 text-xs font-mono">{installCmd}</pre>
          </ScrollArea>
          <div className="flex items-center gap-2">
            <button
              onClick={(e) => handleCopy(installCmd, e.currentTarget.closest<HTMLElement>('[role="dialog"]'))}
              disabled={!enroll}
              className="inline-flex items-center gap-1.5 rounded-md border border-[hsl(var(--border))] bg-transparent px-3 py-1.5 text-xs font-medium transition-colors hover:bg-[hsl(var(--accent))] disabled:opacity-50"
            >
              {copied ? (
                <>
                  <svg xmlns="http://www.w3.org/2000/svg" className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" /></svg>
                  已复制
                </>
              ) : (
                <>
                  <svg xmlns="http://www.w3.org/2000/svg" className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><rect x="9" y="9" width="13" height="13" rx="2" ry="2" /><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" /></svg>
                  复制命令
                </>
              )}
            </button>
          </div>
        </div>
        <DialogFooter>
          <div className="flex w-full items-center justify-between">
            <div className="flex items-center gap-2 text-sm">
              {registered ? (
                <>
                  <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4 text-emerald-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" /></svg>
                  <span className="text-emerald-500">节点已就绪</span>
                </>
              ) : (
                <>
                  <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4 animate-spin text-[hsl(var(--muted-foreground))]" fill="none" viewBox="0 0 24 24"><circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" /><path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" /></svg>
                  <span className="text-[hsl(var(--muted-foreground))]">等待节点上线…</span>
                </>
              )}
            </div>
            <Button onClick={onClose}>完成</Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// TODO(panel-add-node): 为节点列表行新增"重新生成安装命令"操作，复用 /nodes/{id}/enroll-token
// 端点。当前已通过添加流程触发，未实现行级重发。

// ── Manual Update Dialog ─────────────────────────────────────────

function ManualUpdateDialog({
  node,
  open,
  onClose,
}: {
  node: Node | null;
  open: boolean;
  onClose: () => void;
}) {
  const [enroll, setEnroll] = useState<EnrollTokenResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [updated, setUpdated] = useState(false);
  // 两阶段检测：先等节点离线（确认重启已开始），再等重新上线（确认更新完成）
  const seenOffline = useRef(false);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    if (!open || !node) return;
    setEnroll(null);
    setError(null);
    setUpdated(false);
    seenOffline.current = false;
    api
      .post<EnrollTokenResponse>(`/nodes/${node.id}/enroll-token`, {})
      .then(setEnroll)
      .catch((e: Error) => setError(e.message || "生成安装 token 失败"));
  }, [open, node]);

  useEffect(() => {
    if (!open || !node || updated) return;
    pollRef.current = setInterval(() => {
      api.get<Node>(`/nodes/${node.id}`)
        .then((n) => {
          if (!n.online) {
            seenOffline.current = true;
          } else if (seenOffline.current) {
            setUpdated(true);
          }
        })
        .catch(() => {});
    }, 3000);
    return () => { if (pollRef.current) clearInterval(pollRef.current); };
  }, [open, node, updated]);

  const installCmd = enroll?.install_command ?? (error ?? "正在获取安装信息…");

  const handleCopy = async (container?: HTMLElement | null) => {
    if (!enroll?.install_command) return;
    try {
      await copyText(enroll.install_command, container);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // 复制失败静默处理，clipboard.ts 已覆盖 HTTP fallback
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>手动更新节点</DialogTitle>
          <DialogDescription>
            在目标节点机器上以 root 权限运行以下命令，重新安装节点程序以完成更新。
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <ScrollArea className="h-36 rounded-md border bg-[hsl(var(--muted))]">
            <pre className="whitespace-pre-wrap break-all p-3 text-xs font-mono">{installCmd}</pre>
          </ScrollArea>
          <button
            onClick={(e) => handleCopy(e.currentTarget.closest<HTMLElement>('[role="dialog"]'))}
            disabled={!enroll?.install_command}
            className="inline-flex items-center gap-1.5 rounded-md border border-[hsl(var(--border))] bg-transparent px-3 py-1.5 text-xs font-medium transition-colors hover:bg-[hsl(var(--accent))] disabled:opacity-50"
          >
            {copied ? (
              <>
                <svg xmlns="http://www.w3.org/2000/svg" className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" /></svg>
                已复制
              </>
            ) : (
              <>
                <svg xmlns="http://www.w3.org/2000/svg" className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><rect x="9" y="9" width="13" height="13" rx="2" ry="2" /><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" /></svg>
                复制命令
              </>
            )}
          </button>
        </div>
        <DialogFooter>
          <div className="flex w-full items-center justify-between">
            <div className="flex items-center gap-2 text-sm">
              {updated ? (
                <>
                  <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4 text-emerald-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" /></svg>
                  <span className="text-emerald-500">节点已就绪</span>
                </>
              ) : (
                <>
                  <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4 animate-spin text-[hsl(var(--muted-foreground))]" fill="none" viewBox="0 0 24 24"><circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" /><path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" /></svg>
                  <span className="text-[hsl(var(--muted-foreground))]">等待节点上线…</span>
                </>
              )}
            </div>
            <Button onClick={onClose}>完成</Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── Delete Confirmation Dialog ───────────────────────────────────

interface DeleteDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  node: Node | null;
  onConfirm: () => Promise<void>;
  deleting: boolean;
}

function DeleteDialog({ open, onOpenChange, node, onConfirm, deleting }: DeleteDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>确认删除</DialogTitle>
          <DialogDescription>
            确定要删除节点 <span className="font-semibold text-[hsl(var(--foreground))]">{node?.name}</span> 吗？此操作不可撤销。
          </DialogDescription>
        </DialogHeader>
        <DialogFooter className="mt-4">
          <DialogClose asChild>
            <Button type="button" variant="outline" disabled={deleting}>
              取消
            </Button>
          </DialogClose>
          <Button variant="destructive" onClick={onConfirm} disabled={deleting}>
            {deleting ? "删除中…" : "确认删除"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── Node Detail Dialog (status / config / logs) ─────────────────

interface NodeDetailDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  node: Node | null;
  mode: DetailMode;
  handleAuthError: (err: unknown) => boolean;
}

function NodeDetailDialog({ open, onOpenChange, node, mode, handleAuthError }: NodeDetailDialogProps) {
  const [content, setContent] = useState<string>("");
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState<string | null>(null);
  const preRef = useRef<HTMLPreElement>(null);


  // config: 一次性拉取
  useEffect(() => {
    if (!open || !node || !mode || mode === "logs") {
      if (!open || !mode) {
        setContent("");
        setDetailError(null);
      }
      return;
    }

    setDetailLoading(true);
    setDetailError(null);
    setContent("");

    api
      .get<any>(`/nodes/${node.id}/runtime/config`)
      .then((res) => {
        let configStr = res.config ?? "";
        try {
          configStr = JSON.stringify(JSON.parse(configStr), null, 2);
        } catch {
          // keep as-is
        }
        setContent(configStr);
      })
      .catch((err) => {
        if (!handleAuthError(err)) {
          setDetailError(err instanceof Error ? err.message : "加载失败");
        }
      })
      .finally(() => setDetailLoading(false));
  }, [open, node, mode, handleAuthError]);

  // logs: SSE 流式追加
  useEffect(() => {
    if (!open || !node || mode !== "logs") return;

    setDetailLoading(true);
    setDetailError(null);
    setContent("");

    const controller = new AbortController();
    const token = getToken();
    const headers: Record<string, string> = {};
    if (token) headers["Authorization"] = `Bearer ${token}`;

    (async () => {
      try {
        const res = await fetch(`/v1/nodes/${node.id}/runtime/logs/stream`, {
          headers,
          signal: controller.signal,
        });
        if (!res.ok || !res.body) throw new Error(`HTTP ${res.status}`);
        setDetailLoading(false);

        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buf = "";

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          buf += decoder.decode(value, { stream: true });
          // SSE 格式：每个事件以 \n\n 分隔，行以 "data: " 开头
          const blocks = buf.split("\n\n");
          buf = blocks.pop() ?? "";
          for (const block of blocks) {
            for (const line of block.split("\n")) {
              if (line.startsWith("data: ")) {
                const data = line.slice(6);
                setContent((prev) => (prev ? prev + "\n" + data : data));
              }
            }
          }
        }
      } catch (err: any) {
        if (err?.name === "AbortError") return;
        setDetailLoading(false);
        setDetailError(err instanceof Error ? err.message : "连接失败");
      }
    })();

    return () => controller.abort();
  }, [open, node, mode]);

  // 新内容到来时滚动到底部
  useEffect(() => {
    if (mode === "logs" && preRef.current) {
      preRef.current.scrollTop = preRef.current.scrollHeight;
    }
  }, [content, mode]);

  const titleMap: Record<string, string> = {
    config: "节点配置",
    logs: "节点日志",
  };

  const dialogWidth = "sm:max-w-2xl";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className={`${dialogWidth} flex flex-col max-h-[90vh] overflow-hidden`}>
        <DialogHeader className="shrink-0">
          <DialogTitle>
            {mode ? titleMap[mode] : ""} — {node?.name}
            {mode === "logs" && !detailLoading && !detailError && (
              <span className="ml-2 inline-flex items-center gap-1 text-xs font-normal text-green-500">
                <span className="h-1.5 w-1.5 rounded-full bg-green-500 animate-pulse" />
                实时
              </span>
            )}
          </DialogTitle>
          <DialogDescription>
            {mode === "config" && "当前节点 Xray 配置。"}
            {mode === "logs" && "实时流式日志，关闭弹窗断开连接。"}
          </DialogDescription>
        </DialogHeader>

        <div className="mt-2 flex-1 min-h-0 overflow-hidden flex flex-col">
          {detailLoading && (
            <div className="flex items-center justify-center py-12">
              <div className="h-6 w-6 animate-spin rounded-full border-2 border-[hsl(var(--muted-foreground))] border-t-transparent" />
              <span className="ml-3 text-sm text-[hsl(var(--muted-foreground))]">加载中…</span>
            </div>
          )}

          {detailError && (
            <div className="rounded-md bg-[hsl(var(--destructive))]/10 p-4 text-sm text-[hsl(var(--destructive))]">
              {detailError}
            </div>
          )}

          {!detailLoading && !detailError && content && (
            mode === "logs" ? (
              <pre
                ref={preRef}
                className="flex-1 min-h-0 overflow-auto rounded-md bg-[hsl(var(--muted))] p-3 font-mono text-xs leading-relaxed text-[hsl(var(--foreground))] sm:p-4 whitespace-pre-wrap break-all"
              >
                {content}
              </pre>
            ) : mode === "config" ? (
              <div className="flex-1 min-h-0 overflow-auto rounded-md bg-[#0f1117] p-3 sm:p-4">
                <JsonViewer src={content} collapseDepth={3} />
              </div>
            ) : (
              <div className="flex-1 min-h-0 overflow-auto rounded-md bg-[#0f1117] p-3 sm:p-4">
                <JsonViewer src={content} collapseDepth={3} />
              </div>
            )
          )}

          {!detailLoading && !detailError && !content && (
            <p className="py-8 text-center text-sm text-[hsl(var(--muted-foreground))]">
              无数据
            </p>
          )}
        </div>

        <DialogFooter className="mt-4 shrink-0">
          <DialogClose asChild>
            <Button variant="outline">关闭</Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── Node Card ────────────────────────────────────────────────────

interface GeoInfo {
  ip: string;
  country_code: string;
  country_name: string;
  city: string;
  asn: number;
  asn_org: string;
}

interface NodeCardProps {
  node: Node;
  runtime: RuntimeInfo | null;
  metrics: NodeMetrics | null;
  onEdit: (node: Node) => void;
  onDelete: (node: Node) => void;
  onOpenDetail: (node: Node, mode: DetailMode) => void;
  onRestart: (node: Node) => void;
  onSpeedtest: (node: Node) => void;
  speedtestLoading: boolean;
  speedtestResult: SpeedtestResult | null;
  onCheck: (node: Node) => void;
  checkLoading: boolean;
  checkResult: CheckResult | null;
  prevMetrics: NodeMetrics | null;
  onUpdate: (node: Node) => void;
  updateLoading: boolean;
  onManualUpdate: (node: Node) => void;
  latestVersion: string | null;
  geoInfo?: GeoInfo | null;
}

// ── Traceroute Dialog ────────────────────────────────────────────

// ── Globalping 类型 ──────────────────────────────────────────────

interface GPProbe {
  location: { city: string; country: string; network: string; asn: number };
}

interface GPHop {
  resolvedAddress: string | null;
  resolvedHostname: string | null;
  timings: { rtt: number }[];
}

const CITY_ZH: Record<string, string> = {
  Beijing: "北京", Shanghai: "上海", Guangzhou: "广州", Shenzhen: "深圳",
  Chengdu: "成都", Hangzhou: "杭州", Wuhan: "武汉", Nanjing: "南京",
  Xian: "西安", Chongqing: "重庆", Tianjin: "天津", Zhengzhou: "郑州",
  Fuzhou: "福州", Qingdao: "青岛", Hefei: "合肥", Changsha: "长沙",
  Suzhou: "苏州", Dongguan: "东莞", Kunming: "昆明", Xiamen: "厦门",
  Jinan: "济南", Harbin: "哈尔滨", Shenyang: "沈阳", Changchun: "长春",
};

const PROVINCE_ZH: Record<string, string> = {
  bj:"北京", sh:"上海", gd:"广东", zj:"浙江", js:"江苏", fj:"福建",
  sd:"山东", hb:"湖北", hn:"湖南", sc:"四川", cq:"重庆", tj:"天津",
  he:"河北", ha:"河南", jx:"江西", ah:"安徽", ln:"辽宁", jl:"吉林",
  hlj:"黑龙江", sx:"山西", sn:"陕西", yn:"云南", gz:"贵州", gx:"广西",
};

const CARRIER_ZH: Record<string, string> = { ct:"上海电信", cu:"上海联通", cm:"上海移动" };

/** 根据 IP 前缀判断线路质量标签（纯函数，不依赖 GeoIP 状态）。*/
function detectQualityLabel(hops: TracerouteHop[]): string {
  if (hops.length === 0) return "";
  const types = new Set<string>();
  for (const hop of hops) {
    if (!hop.ip) continue;
    if (hop.ip.startsWith("59.43."))  { types.add("CN2"); continue; }
    if (hop.ip.startsWith("202.97.")) { types.add("163"); continue; }
    if (hop.ip.startsWith("219.158.")) { types.add("CU"); continue; }
  }
  if (types.has("CN2") && !types.has("163")) return "CN2 GIA";
  if (types.has("CN2") && types.has("163"))  return "CN2 GT";
  if (types.has("163")) return "电信 163";
  if (types.has("CU"))  return "联通 AS4837";
  return "";
}

function TracerouteDialog({ node, open, onOpenChange }: {
  node: Node;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  // ── 回程状态 ──────────────────────────────────────────────────
  const [activeTab, setActiveTab] = useState<"inbound" | "outbound" | "history">("inbound");

  // ── 历史记录状态 ──────────────────────────────────────────────
  const [histResults, setHistResults] = useState<TracerouteResult[]>([]);
  const [histLoading, setHistLoading] = useState(false);
  const [histExpandedId, setHistExpandedId] = useState<string | null>(null);

  const fetchHistory = useCallback(async () => {
    setHistLoading(true);
    try {
      const res = await api.get<{ results: TracerouteResult[] }>(
        `/nodes/${node.id}/traceroute/results?limit=50`
      );
      setHistResults(res.results ?? []);
    } catch {
      // 静默失败
    } finally {
      setHistLoading(false);
    }
  }, [node.id]);
  const [host, setHost] = useState("8.8.8.8");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [hops, setHops] = useState<TracerouteHop[]>([]);
  const [done, setDone] = useState(false);
  const [geoMap, setGeoMap] = useState<Map<string, GeoInfo>>(new Map());
  const abortRef = useRef<AbortController | null>(null);

  // ── 去程（Globalping）状态 ────────────────────────────────────
  const [gpCities, setGpCities] = useState<{ city: string; count: number }[]>([]);
  const [gpCitiesLoading, setGpCitiesLoading] = useState(false);
  const [selectedCity, setSelectedCity] = useState("");
  const [gpLoading, setGpLoading] = useState(false);
  const [gpHops, setGpHops] = useState<TracerouteHop[]>([]);
  const [gpError, setGpError] = useState<string | null>(null);
  const [gpProbeInfo, setGpProbeInfo] = useState<{ city: string; network: string } | null>(null);
  const gpAbortRef = useRef<AbortController | null>(null);

  const nodeTarget = useMemo(() => {
    // 优先用 ip_override（GeoIP 地址），它是真实公网 IP
    if (node.ip_override?.trim()) return node.ip_override.trim();
    // 否则从 base_url 提取 hostname
    for (const url of [node.base_url, "https://" + node.base_url]) {
      try { return new URL(url).hostname; } catch {}
    }
    return node.base_url.replace(/:\d+$/, "");
  }, [node.base_url, node.ip_override]);

  // ── 快速回程选择状态 ──────────────────────────────────────────
  const [qProvince, setQProvince] = useState("bj");
  const [qCarrier, setQCarrier] = useState<"ct" | "cu" | "cm">("ct");
  const [qResolving, setQResolving] = useState(false);

  const handleQuickSelect = async () => {
    setQResolving(true);
    try {
      const domain = `${qProvince}-${qCarrier}-v4.ip.zstaticcdn.com`;
      const res = await fetch(
        `https://cloudflare-dns.com/dns-query?name=${domain}&type=A`,
        { headers: { Accept: "application/dns-json" } },
      );
      const data = await res.json();
      const a = (data.Answer ?? []).find((r: any) => r.type === 1);
      if (!a) throw new Error(`无法解析 ${domain}`);
      setHost(a.data);
      const label = `${PROVINCE_ZH[qProvince] ?? qProvince} ${CARRIER_ZH[qCarrier]}（${a.data}）`;
      await handleStart(a.data, label);
    } catch (err: any) {
      setError(err.message ?? "DNS 解析失败");
    } finally {
      setQResolving(false);
    }
  };

  // 判断是否为私有/保留 IP，这类地址 GeoIP 数据库没有记录
  const isPrivateIP = (ip: string): boolean => {
    if (ip.startsWith("10.") || ip.startsWith("127.") || ip === "::1") return true;
    if (ip.startsWith("192.168.")) return true;
    if (ip.startsWith("100.")) {
      const second = parseInt(ip.split(".")[1] ?? "0");
      if (second >= 64 && second <= 127) return true; // RFC 6598 运营商级 NAT
    }
    // 11.x.x.x 虽属美国国防部申请段，但国内运营商内部大量使用，GeoIP 标注不准
    if (ip.startsWith("11.")) return true;
    const parts = ip.split(".").map(Number);
    if (parts.length === 4 && (parts[0] ?? 0) === 172 && (parts[1] ?? 0) >= 16 && (parts[1] ?? 0) <= 31) return true;
    return false;
  };

  // 查询每跳 IP 的 GeoIP 信息（私有 IP 跳过，静默失败）
  const fetchGeoForHops = async (hops: TracerouteHop[]) => {
    const ips = hops.filter((h) => !h.timeout && h.ip && !isPrivateIP(h.ip!)).map((h) => h.ip!);
    if (!ips.length) return;
    const token = getToken();
    const headers: Record<string, string> = token ? { Authorization: `Bearer ${token}` } : {};
    const entries = await Promise.all(
      ips.map(async (ip) => {
        try {
          const res = await fetch(`/v1/system/geoip/lookup?host=${encodeURIComponent(ip)}`, { headers });
          if (!res.ok) return null;
          const info: GeoInfo = await res.json();
          return [ip, info] as [string, GeoInfo];
        } catch {
          return null;
        }
      }),
    );
    setGeoMap((prev) => {
      const next = new Map(prev);
      for (const e of entries) {
        if (e) next.set(e[0], e[1]);
      }
      return next;
    });
  };

  const handleStart = async (overrideHost?: string, targetLabel?: string) => {
    const target = overrideHost ?? host;
    if (!target.trim()) return;
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    setLoading(true);
    setError(null);
    setHops([]);
    setDone(false);
    setGeoMap(new Map());

    try {
      const token = getToken();
      const res = await fetch(
        `/v1/nodes/${node.id}/runtime/traceroute?host=${encodeURIComponent(target.trim())}&method=tcp&port=443`,
        {
          headers: token ? { Authorization: `Bearer ${token}` } : {},
          signal: ctrl.signal,
        },
      );
      if (!res.ok || !res.body) {
        setError(`请求失败 (HTTP ${res.status})`);
        return;
      }

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buf = "";
      const collectedHops: TracerouteHop[] = [];

      while (true) {
        const { done: streamDone, value } = await reader.read();
        if (streamDone) break;
        buf += decoder.decode(value, { stream: true });

        // 解析 SSE 行（每个事件以 \n\n 结尾）
        const parts = buf.split("\n\n");
        buf = parts.pop() ?? "";
        for (const part of parts) {
          for (const line of part.split("\n")) {
            if (line.startsWith("event: error")) continue;
            if (line.startsWith("event: done")) {
              setDone(true);
              continue;
            }
            if (line.startsWith("data: ")) {
              const json = line.slice(6);
              try {
                const obj = JSON.parse(json);
                if (obj.error) {
                  setError(obj.error);
                } else if (obj.hop != null) {
                  collectedHops.push(obj as TracerouteHop);
                  setHops([...collectedHops]);
                  // 每收到一个有 IP 的跳，立即查 GeoIP
                  if (obj.ip) fetchGeoForHops([obj]);
                }
              } catch {}
            }
          }
        }
      }
      setDone(true);
      if (collectedHops.length > 0) {
        const quality = detectQualityLabel(collectedHops);
        api.post(`/nodes/${node.id}/traceroute/results`, {
          direction: "inbound",
          target: target.trim(),
          hops: JSON.stringify(collectedHops),
          quality,
        }).then(() => fetchHistory()).catch(() => {});
      }
    } catch (err: any) {
      if (err?.name !== "AbortError") {
        setError(err?.message ?? "请求失败");
      }
    } finally {
      setLoading(false);
    }
  };

  // ── Globalping 去程 ───────────────────────────────────────────

  const loadGpCities = useCallback(async () => {
    setGpCitiesLoading(true);
    try {
      const res = await fetch("https://api.globalping.io/v1/probes");
      const data: GPProbe[] = await res.json();
      const map = new Map<string, number>();
      data.filter(p => p.location.country === "CN").forEach(p => {
        const c = p.location.city;
        map.set(c, (map.get(c) ?? 0) + 1);
      });
      const cities = Array.from(map.entries())
        .sort((a, b) => b[1] - a[1])
        .map(([city, count]) => ({ city, count }));
      setGpCities(cities);
      if (cities.length > 0) setSelectedCity(c => { const first = cities[0]?.city ?? ""; return c || first; });
    } catch {
      // 静默失败
    } finally {
      setGpCitiesLoading(false);
    }
  }, []);

  useEffect(() => {
    if (activeTab === "outbound" && gpCities.length === 0 && !gpCitiesLoading) {
      loadGpCities();
    }
    if (activeTab === "history") {
      fetchHistory();
    }
  }, [activeTab, gpCities.length, gpCitiesLoading, loadGpCities, fetchHistory]);

  const handleGpStart = async () => {
    if (!selectedCity || gpLoading) return;
    gpAbortRef.current?.abort();
    const ctrl = new AbortController();
    gpAbortRef.current = ctrl;
    setGpLoading(true);
    setGpHops([]);
    setGpError(null);
    setGpProbeInfo(null);

    try {
      const createRes = await fetch("https://api.globalping.io/v1/measurements", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          type: "traceroute",
          target: nodeTarget,
          limit: 1,
          locations: [{ city: selectedCity, country: "CN" }],
          measurementOptions: { protocol: "TCP", port: 443 },
        }),
        signal: ctrl.signal,
      });
      if (!createRes.ok) {
        const e = await createRes.json().catch(() => ({}));
        const params = e.error?.params ? ` (${JSON.stringify(e.error.params)})` : "";
        throw new Error((e.error?.message ?? `创建测量失败 (${createRes.status})`) + params);
      }
      const { id } = await createRes.json();

      // 轮询，最多 40 次 × 1.5s = 60s
      for (let i = 0; i < 40; i++) {
        await new Promise(r => setTimeout(r, 1500));
        if (ctrl.signal.aborted) return;
        const pollRes = await fetch(`https://api.globalping.io/v1/measurements/${id}`, { signal: ctrl.signal });
        const data = await pollRes.json();
        if (data.status !== "in-progress") {
          const result = data.results?.[0];
          if (!result) throw new Error("未收到结果");
          setGpProbeInfo({ city: result.probe.city, network: result.probe.network });
          if (result.result.status === "failed") throw new Error(result.result.rawOutput || "追踪失败");
          const converted: TracerouteHop[] = (result.result.hops ?? []).map((h: GPHop, idx: number) => ({
            hop: idx + 1,
            ip: h.resolvedAddress ?? undefined,
            timeout: !h.resolvedAddress,
            rtt_ms: (h.timings ?? []).map(t => t.rtt).filter(v => v != null),
          }));
          setGpHops(converted);
          fetchGeoForHops(converted);
          if (converted.length > 0) {
            const quality = detectQualityLabel(converted);
            api.post(`/nodes/${node.id}/traceroute/results`, {
              direction: "outbound",
              target: nodeTarget,
              hops: JSON.stringify(converted),
              quality,
            }).then(() => fetchHistory()).catch(() => {});
          }
          break;
        }
      }
    } catch (err: any) {
      if (err?.name !== "AbortError") setGpError(err?.message ?? "请求失败");
    } finally {
      setGpLoading(false);
    }
  };

  // 关闭时中止请求并重置状态
  const handleOpenChange = (v: boolean) => {
    if (!v) {
      abortRef.current?.abort();
      gpAbortRef.current?.abort();
      setLoading(false);
      setGpLoading(false);
      setError(null);
      setHops([]);
      setDone(false);
      setGpHops([]);
      setGpError(null);
      setGpProbeInfo(null);
      setGeoMap(new Map());
      setHistResults([]);
      setHistExpandedId(null);
      setActiveTab("inbound");
    }
    onOpenChange(v);
  };

  const [copied, setCopied] = useState(false);

  // ── 网络类型识别 ──────────────────────────────────────────────

  /** 根据 IP 前缀和 ASN 判断每跳所属网络。 */
  const getNetworkType = (ip?: string, geo?: GeoInfo): string => {
    if (!ip) return "";
    // IP 前缀优先（比 ASN 更精准）
    if (ip.startsWith("59.43."))  return "CN2";
    if (ip.startsWith("202.97.")) return "163";
    if (ip.startsWith("219.158."))return "CU";
    // ASN 兜底
    const asn = geo?.asn;
    if (asn === 4809)  return "CN2";
    if (asn === 4134)  return "163";
    if (asn === 4837)  return "CU";
    if (asn === 9929)  return "CU2";
    if (asn === 58453 || asn === 9808 || asn === 56040) return "CMI";
    return "";
  };

  /** 汇总所有跳推断整体线路质量。 */
  const detectQuality = (targetHops: TracerouteHop[]): { label: string; color: string } | null => {
    if (targetHops.length === 0) return null;
    const types = new Set<string>();
    for (const hop of targetHops) {
      if (hop.ip && !isPrivateIP(hop.ip)) {
        const t = getNetworkType(hop.ip, geoMap.get(hop.ip));
        if (t) types.add(t);
      }
    }
    if (types.has("CN2") && !types.has("163")) return { label: "CN2 GIA", color: "text-emerald-600 bg-emerald-500/10" };
    if (types.has("CN2") && types.has("163"))  return { label: "CN2 GT",  color: "text-blue-600 bg-blue-500/10" };
    if (types.has("CU2"))                       return { label: "联通 AS9929", color: "text-emerald-600 bg-emerald-500/10" };
    if (types.has("163"))                       return { label: "电信 163",    color: "text-amber-600 bg-amber-500/10" };
    if (types.has("CU"))                        return { label: "联通 AS4837", color: "text-amber-600 bg-amber-500/10" };
    if (types.has("CMI"))                       return { label: "移动 CMI",    color: "text-amber-600 bg-amber-500/10" };
    return null;
  };

  const networkBadgeColor = (type: string): string => {
    switch (type) {
      case "CN2":  return "text-emerald-600 bg-emerald-500/10";
      case "163":  return "text-amber-600 bg-amber-500/10";
      case "CU2":  return "text-emerald-600 bg-emerald-500/10";
      case "CU":   return "text-blue-600 bg-blue-500/10";
      case "CMI":  return "text-purple-600 bg-purple-500/10";
      default:     return "text-[hsl(var(--muted-foreground))] bg-[hsl(var(--muted))]";
    }
  };

  const avgRtt = (rtts?: number[]) => {
    if (!rtts?.length) return null;
    return (rtts.reduce((a, b) => a + b, 0) / rtts.length).toFixed(1);
  };

  const geoLabel = (ip?: string) => {
    if (!ip) return "";
    const g = geoMap.get(ip);
    if (!g) return "";
    const parts: string[] = [];
    if (g.country_code) parts.push(countryFlag(g.country_code));
    if (g.country_name) parts.push(g.country_name);
    if (g.asn_org) parts.push(g.asn_org);
    return parts.join(" ");
  };

  const handleCopy = (e?: React.MouseEvent) => {
    const isOutbound = activeTab === "outbound";
    const targetHops = isOutbound ? gpHops : hops;
    if (!targetHops.length) return;
    const header = isOutbound
      ? `traceroute (TCP/443) from ${CITY_ZH[selectedCity] ?? selectedCity} → ${nodeTarget}`
      : `traceroute (TCP/443) to ${host.trim()}`;
    const lines = [header, ""];
    for (const hop of targetHops) {
      if (hop.timeout) {
        lines.push(`${String(hop.hop).padStart(2)}  * * *`);
      } else {
        const rtt = hop.rtt_ms?.length ? avgRtt(hop.rtt_ms) + " ms" : "—";
        const geo = hop.ip && isPrivateIP(hop.ip) ? "内网" : geoLabel(hop.ip);
        lines.push(
          `${String(hop.hop).padStart(2)}  ${(hop.ip ?? "").padEnd(18)}  ${rtt.padEnd(12)}  ${geo}`.trimEnd(),
        );
      }
    }
    const container = e?.currentTarget.closest<HTMLElement>('[role="dialog"]');
    copyText(lines.join("\n"), container).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }).catch(() => {});
  };

  // ── 共用表格渲染 ─────────────────────────────────────────────
  const renderTable = (targetHops: TracerouteHop[], isStreaming: boolean) => (
    <table className="w-full text-xs font-mono">
      <thead>
        <tr className="border-b border-[hsl(var(--border))] bg-[hsl(var(--muted))]">
          <th className="py-2 pl-3 pr-2 text-left font-medium text-[hsl(var(--muted-foreground))]">#</th>
          <th className="py-2 pr-2 text-left font-medium text-[hsl(var(--muted-foreground))]">IP</th>
          <th className="py-2 pr-2 text-left font-medium text-[hsl(var(--muted-foreground))]">网络</th>
          <th className="py-2 pr-2 text-left font-medium text-[hsl(var(--muted-foreground))]">地区 / ASN</th>
          <th className="py-2 pr-3 text-right font-medium text-[hsl(var(--muted-foreground))]">平均 RTT</th>
        </tr>
      </thead>
      <tbody>
        {targetHops.map((hop) => {
          const geo = hop.ip ? geoMap.get(hop.ip) : undefined;
          const netType = hop.ip && !isPrivateIP(hop.ip) ? getNetworkType(hop.ip, geo) : "";
          return (
            <tr key={hop.hop} className="border-b border-[hsl(var(--border))] last:border-0 hover:bg-[hsl(var(--muted)/0.4)]">
              <td className="py-2 pl-3 pr-2 text-[hsl(var(--muted-foreground))]">{hop.hop}</td>
              <td className="py-2 pr-2">
                {hop.timeout ? <span className="text-[hsl(var(--muted-foreground))]">* * *</span> : <span>{hop.ip}</span>}
              </td>
              <td className="py-2 pr-2">
                {hop.ip && isPrivateIP(hop.ip) ? (
                  <span className="text-[hsl(var(--muted-foreground))] opacity-40">内网</span>
                ) : netType ? (
                  <span className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${networkBadgeColor(netType)}`}>{netType}</span>
                ) : geo?.asn_org ? (
                  <span className="text-[hsl(var(--muted-foreground))] opacity-60" title={geo.asn_org}>
                    {geo.asn_org.length > 14 ? geo.asn_org.slice(0, 13) + "…" : geo.asn_org}
                  </span>
                ) : null}
              </td>
              <td className="py-2 pr-2">
                {hop.ip && isPrivateIP(hop.ip) ? null : geo ? (
                  <span className="text-[hsl(var(--muted-foreground))]">
                    {geo.country_code && <span className="mr-1">{countryFlag(geo.country_code)}</span>}
                    {[geo.country_name, geo.city].filter(Boolean).join(" · ")}
                    {geo.asn_org && <span className="ml-1.5 opacity-70">{geo.asn_org}</span>}
                  </span>
                ) : hop.ip ? (
                  <span className="text-[hsl(var(--muted-foreground))] opacity-30">查询中…</span>
                ) : null}
              </td>
              <td className="py-2 pr-3 text-right">
                {hop.timeout || !hop.rtt_ms?.length ? (
                  <span className="text-[hsl(var(--muted-foreground))]">—</span>
                ) : (
                  <span className="text-[hsl(var(--foreground))]" title={hop.rtt_ms.map(v => v.toFixed(2) + " ms").join("  ")}>
                    {avgRtt(hop.rtt_ms)} ms
                  </span>
                )}
              </td>
            </tr>
          );
        })}
        {isStreaming && (
          <tr>
            <td colSpan={5} className="py-2 pl-3 text-[hsl(var(--muted-foreground))] opacity-50">追踪中…</td>
          </tr>
        )}
      </tbody>
    </table>
  );

  const Spinner = () => (
    <svg className="mr-1.5 h-3.5 w-3.5 animate-spin" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
    </svg>
  );

  const currentHops = activeTab === "inbound" ? hops : gpHops;
  const quality = detectQuality(currentHops);
  const hasCopyable = activeTab !== "history" && (activeTab === "inbound" ? (!error && hops.length > 0) : (!gpError && gpHops.length > 0));

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>路由追踪 — {node.name}</DialogTitle>
          <DialogDescription className="font-mono text-xs">{nodeTarget}</DialogDescription>
        </DialogHeader>

        {/* ── Tab 切换 ── */}
        <div className="flex rounded-md border border-[hsl(var(--border))] overflow-hidden text-xs w-fit">
          {([["inbound", "回程（节点出发）"], ["outbound", "去程（Globalping）"], ["history", "历史记录"]] as const).map(([tab, label]) => (
            <button
              key={tab}
              onClick={() => setActiveTab(tab)}
              className={`px-3 py-1.5 transition-colors ${
                activeTab === tab
                  ? "bg-[hsl(var(--primary))] text-[hsl(var(--primary-foreground))]"
                  : "bg-transparent text-[hsl(var(--muted-foreground))] hover:bg-[hsl(var(--muted))]"
              }`}
            >
              {label}
            </button>
          ))}
        </div>

        {/* ── 回程 Tab ── */}
        {activeTab === "inbound" && (
          <div className="space-y-2">
            {/* 快速省份 + 运营商选择 */}
            <div className="flex items-center gap-2">
              <select
                value={qProvince}
                onChange={(e) => setQProvince(e.target.value)}
                disabled={loading || qResolving}
                className="rounded-md border border-[hsl(var(--border))] bg-[hsl(var(--background))] px-2 py-1.5 text-xs font-mono text-[hsl(var(--foreground))] focus:outline-none focus:ring-1 focus:ring-[hsl(var(--ring))]"
              >
                {[
                  ["bj","北京"],["sh","上海"],["gd","广东"],["zj","浙江"],
                  ["js","江苏"],["fj","福建"],["sd","山东"],["hb","湖北"],
                  ["hn","湖南"],["sc","四川"],["cq","重庆"],["tj","天津"],
                  ["he","河北"],["ha","河南"],["jx","江西"],["ah","安徽"],
                  ["ln","辽宁"],["jl","吉林"],["hlj","黑龙江"],["sx","山西"],
                  ["sn","陕西"],["yn","云南"],["gz","贵州"],["gx","广西"],
                ].map(([code, name]) => (
                  <option key={code} value={code}>{name}</option>
                ))}
              </select>
              <div className="flex rounded-md border border-[hsl(var(--border))] overflow-hidden text-xs font-mono">
                {(["ct","cu","cm"] as const).map((c) => (
                  <button
                    key={c}
                    onClick={() => setQCarrier(c)}
                    disabled={loading || qResolving}
                    className={`px-2.5 py-1.5 transition-colors ${
                      qCarrier === c
                        ? "bg-[hsl(var(--primary))] text-[hsl(var(--primary-foreground))]"
                        : "bg-transparent text-[hsl(var(--muted-foreground))] hover:bg-[hsl(var(--muted))]"
                    }`}
                  >
                    {CARRIER_ZH[c] ?? c}
                  </button>
                ))}
              </div>
              <Button
                size="sm"
                variant="outline"
                onClick={handleQuickSelect}
                disabled={loading || qResolving}
                className="shrink-0 text-xs"
              >
                {qResolving ? <><Spinner />解析中…</> : "快速追踪"}
              </Button>
            </div>
            {/* 手动输入 */}
            <div className="flex gap-2">
              <Input
                value={host}
                onChange={(e) => setHost(e.target.value)}
                placeholder="或手动输入目标地址，如 8.8.8.8"
                onKeyDown={(e) => e.key === "Enter" && !loading && handleStart()}
                className="font-mono text-sm"
              />
              <Button onClick={() => handleStart()} disabled={loading || !host.trim()} className="shrink-0">
                {loading ? <><Spinner />追踪中…</> : "追踪"}
              </Button>
            </div>
          </div>
        )}

        {/* ── 去程 Tab ── */}
        {activeTab === "outbound" && (
          <div className="flex items-center gap-2">
            <select
              value={selectedCity}
              onChange={(e) => setSelectedCity(e.target.value)}
              disabled={gpCitiesLoading || gpLoading}
              className="flex-1 rounded-md border border-[hsl(var(--border))] bg-[hsl(var(--background))] px-3 py-1.5 text-sm font-mono text-[hsl(var(--foreground))] focus:outline-none focus:ring-1 focus:ring-[hsl(var(--ring))]"
            >
              {gpCitiesLoading ? (
                <option>加载探针列表中…</option>
              ) : gpCities.length === 0 ? (
                <option>暂无中国探针</option>
              ) : (
                gpCities.map(({ city, count }) => (
                  <option key={city} value={city}>
                    {CITY_ZH[city] ?? city}（{count} 个探针）
                  </option>
                ))
              )}
            </select>
            <Button onClick={handleGpStart} disabled={gpLoading || gpCitiesLoading || !selectedCity} className="shrink-0">
              {gpLoading ? <><Spinner />追踪中…</> : "开始追踪"}
            </Button>
          </div>
        )}

        {/* ── 探针信息 & 线路质量 ── */}
        {(gpProbeInfo || quality) && (
          <div className="flex items-center gap-3 text-xs">
            {activeTab === "outbound" && gpProbeInfo && (
              <span className="text-[hsl(var(--muted-foreground))]">
                探针：{CITY_ZH[gpProbeInfo.city] ?? gpProbeInfo.city} · {gpProbeInfo.network}
              </span>
            )}
            {quality && (
              <>
                <span className="text-[hsl(var(--muted-foreground))]">线路</span>
                <span className={`rounded-full px-2 py-0.5 font-medium ${quality.color}`}>{quality.label}</span>
              </>
            )}
          </div>
        )}

        {/* ── 结果表格（回程 / 去程）── */}
        {activeTab !== "history" && (() => {
          const err = activeTab === "inbound" ? error : gpError;
          const targetHops = activeTab === "inbound" ? hops : gpHops;
          const isStreaming = activeTab === "inbound" ? loading : gpLoading;
          if (!err && targetHops.length === 0) return null;
          return (
            <ScrollArea className="max-h-80 rounded-md border border-[hsl(var(--border))]">
              {err ? (
                <p className="p-3 text-sm text-[hsl(var(--destructive))]">{err}</p>
              ) : (
                renderTable(targetHops, isStreaming)
              )}
            </ScrollArea>
          );
        })()}

        {/* ── 历史记录 Tab ── */}
        {activeTab === "history" && (
          <div className="rounded-md border border-[hsl(var(--border))] overflow-hidden">
            {histLoading ? (
              <div className="space-y-0">
                {[1, 2, 3].map((i) => (
                  <div key={i} className="flex items-center gap-3 border-b border-[hsl(var(--border))] px-4 py-3 last:border-0">
                    <div className="h-4 w-10 animate-pulse rounded bg-[hsl(var(--muted))]" />
                    <div className="h-4 flex-1 animate-pulse rounded bg-[hsl(var(--muted))]" />
                    <div className="h-4 w-14 animate-pulse rounded bg-[hsl(var(--muted))]" />
                    <div className="h-4 w-24 animate-pulse rounded bg-[hsl(var(--muted))]" />
                  </div>
                ))}
              </div>
            ) : histResults.length === 0 ? (
              <div className="py-10 text-center text-sm text-[hsl(var(--muted-foreground))]">
                暂无历史记录
              </div>
            ) : (
              <ScrollArea className="max-h-96">
                <div className="flex items-center gap-3 border-b border-[hsl(var(--border))] bg-[hsl(var(--muted)/0.5)] px-4 py-2 text-xs font-medium text-[hsl(var(--muted-foreground))]">
                  <span className="w-10 shrink-0">方向</span>
                  <span className="flex-1">目标</span>
                  <span className="shrink-0">线路</span>
                  <span className="shrink-0 w-28 text-right">时间</span>
                  <span className="shrink-0 w-4" />
                  <span className="shrink-0 w-4" />
                </div>
                {histResults.map((r) => {
                  const isExpanded = histExpandedId === r.id;
                  let parsedHops: TracerouteHop[] = [];
                  try { parsedHops = JSON.parse(r.hops) as TracerouteHop[]; } catch {}
                  return (
                    <div key={r.id} className="border-b border-[hsl(var(--border))] last:border-0">
                      <div
                        className="flex cursor-pointer items-center gap-3 px-4 py-2.5 hover:bg-[hsl(var(--muted)/0.4)] transition-colors"
                        onClick={() => setHistExpandedId(isExpanded ? null : r.id)}
                      >
                        <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${
                          r.direction === "inbound" ? "text-purple-600 bg-purple-500/10" : "text-sky-600 bg-sky-500/10"
                        }`}>
                          {r.direction === "inbound" ? "回程" : "去程"}
                        </span>
                        <span className="flex-1 truncate font-mono text-xs text-[hsl(var(--muted-foreground))]" title={r.target}>{r.target}</span>
                        {r.quality ? (
                          <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-semibold ${traceQualityColor(r.quality)}`}>
                            {r.quality}
                          </span>
                        ) : <span className="shrink-0 w-14" />}
                        <span className="shrink-0 w-28 text-right text-xs text-[hsl(var(--muted-foreground))]">
                          {traceFormatTime(r.created_at)}
                        </span>
                        <button
                          onClick={(e) => {
                            e.stopPropagation();
                            if (!confirm("确认删除这条追踪记录？")) return;
                            api.del(`/nodes/${r.node_id}/traceroute/results/${r.id}`)
                              .then(() => setHistResults((prev) => prev.filter((x) => x.id !== r.id)))
                              .catch(() => {});
                          }}
                          className="shrink-0 rounded p-0.5 text-[hsl(var(--muted-foreground))] hover:text-[hsl(var(--destructive))] hover:bg-[hsl(var(--destructive)/0.1)] transition-colors"
                          title="删除"
                        >
                          <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="h-3.5 w-3.5">
                            <polyline points="3 6 5 6 21 6" />
                            <path d="M19 6l-1 14a2 2 0 01-2 2H8a2 2 0 01-2-2L5 6" />
                            <path d="M10 11v6M14 11v6" />
                            <path d="M9 6V4a1 1 0 011-1h4a1 1 0 011 1v2" />
                          </svg>
                        </button>
                        <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round"
                          className={`shrink-0 h-4 w-4 text-[hsl(var(--muted-foreground))] transition-transform duration-150 ${isExpanded ? "rotate-180" : ""}`}>
                          <polyline points="6 9 12 15 18 9" />
                        </svg>
                      </div>
                      {isExpanded && parsedHops.length > 0 && (
                        <div className="border-t border-[hsl(var(--border))] bg-[hsl(var(--muted)/0.3)] overflow-x-auto">
                          {renderTable(parsedHops, false)}
                        </div>
                      )}
                    </div>
                  );
                })}
              </ScrollArea>
            )}
          </div>
        )}

        <DialogFooter className="flex-row items-center justify-between sm:justify-between">
          {hasCopyable ? (
            <Button variant="outline" size="sm" onClick={(e) => handleCopy(e)}>
              {copied ? "已复制" : "复制结果"}
            </Button>
          ) : <span />}
          <DialogClose asChild>
            <Button variant="outline">关闭</Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── IP Sentinel 类型 ─────────────────────────────────────────────

interface NodeIPSentinelConfig {
  region_code: string;
  region_name: string;
}

interface IPDetectResult {
  ip: string;
  country: string;
  country_code: string;
  city: string;
}

interface IPSentinelRun {
  id: string;
  task_type: string;
  triggered_by: string;
  status: "pending" | "running" | "success" | "failed";
  output: string[];
  started_at: string;
  finished_at: string | null;
  result?: unknown;
}

interface SentinelSchedule {
  interval_hours: number;
  last_run_at: string | null;
  next_run_at: string | null;
}

const REGION_PRESETS: { code: string; name: string }[] = [
  { code: "US", name: "United States" },
  { code: "JP", name: "Japan" },
  { code: "SG", name: "Singapore" },
  { code: "GB", name: "United Kingdom" },
  { code: "DE", name: "Germany" },
  { code: "FR", name: "France" },
  { code: "HK", name: "Hong Kong" },
  { code: "KR", name: "South Korea" },
  { code: "AU", name: "Australia" },
  { code: "CA", name: "Canada" },
  { code: "TW", name: "Taiwan" },
  { code: "NL", name: "Netherlands" },
];

function sentinelFormatTime(iso: string | null | undefined): string {
  if (!iso) return "--";
  try {
    return new Date(iso).toLocaleString("zh-CN", { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
  } catch { return iso; }
}

function sentinelDuration(startedAt: string, finishedAt: string | null): string {
  if (!finishedAt) return "—";
  try {
    const ms = new Date(finishedAt).getTime() - new Date(startedAt).getTime();
    return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`;
  } catch { return "—"; }
}

function sentinelStatusClass(status: IPSentinelRun["status"] | undefined): string {
  switch (status) {
    case "pending": case "running": return "text-blue-600 bg-blue-500/10";
    case "success": return "text-emerald-600 bg-emerald-500/10";
    case "failed":  return "text-red-600 bg-red-500/10";
    default:        return "text-[hsl(var(--muted-foreground))] bg-[hsl(var(--muted))]";
  }
}

function sentinelStatusLabel(status: IPSentinelRun["status"] | undefined): string {
  switch (status) {
    case "pending": return "等待中";
    case "running": return "运行中";
    case "success": return "成功";
    case "failed":  return "失败";
    default:        return "--";
  }
}

function sentinelTriggeredBy(v: string): string {
  if (v === "auto" || v === "scheduler") return "自动";
  if (v === "manual" || v === "user")    return "手动";
  return v || "—";
}

function SentinelRunRow({ run }: { run: IPSentinelRun }) {
  const [expanded, setExpanded] = useState(false);
  return (
    <>
      <tr className="border-b border-[hsl(var(--border))] last:border-0 hover:bg-[hsl(var(--muted)/0.4)] cursor-pointer select-none" onClick={() => setExpanded(v => !v)}>
        <td className="py-1.5 pl-3 pr-2 text-[11px] text-[hsl(var(--muted-foreground))] whitespace-nowrap">{sentinelFormatTime(run.started_at)}</td>
        <td className="py-1.5 pr-2"><span className="inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-medium bg-[hsl(var(--muted))] text-[hsl(var(--muted-foreground))]">{sentinelTriggeredBy(run.triggered_by)}</span></td>
        <td className="py-1.5 pr-2"><span className={`inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-medium ${sentinelStatusClass(run.status)}`}>{sentinelStatusLabel(run.status)}</span></td>
        <td className="py-1.5 pr-3 text-right text-[11px] text-[hsl(var(--muted-foreground))]">{sentinelDuration(run.started_at, run.finished_at)}</td>
      </tr>
      {expanded && (
        <tr className="border-b border-[hsl(var(--border))]">
          <td colSpan={4} className="px-3 pb-2 pt-1">
            <pre className="max-h-60 overflow-y-auto rounded bg-[hsl(var(--muted))] p-2.5 text-[10px] leading-relaxed font-mono whitespace-pre-wrap">
              {run.output.length > 0 ? run.output.join("\n") : run.result ? JSON.stringify(run.result, null, 2) : "(无输出)"}
            </pre>
          </td>
        </tr>
      )}
    </>
  );
}

// ── IPSentinelDialog ──────────────────────────────────────────────

function IPSentinelDialog({ nodeId, nodeName, open, onOpenChange }: {
  nodeId: string;
  nodeName: string;
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const handleAuthError = useAuthErrorHandler();

  const [config, setConfig] = useState<NodeIPSentinelConfig>({ region_code: "", region_name: "" });
  const [draftCode, setDraftCode] = useState("");
  const [draftName, setDraftName] = useState("");
  const [detectResult, setDetectResult] = useState<IPDetectResult | null>(null);
  const [runs, setRuns] = useState<IPSentinelRun[]>([]);
  const [runsExpanded, setRunsExpanded] = useState(false);
  const [detectLoading, setDetectLoading] = useState(false);
  const [runLoading, setRunLoading] = useState(false);
  const [configSaving, setConfigSaving] = useState(false);
  const [loaded, setLoaded] = useState(false);

  const pollTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const pollCountRef = useRef(0);

  const fetchRuns = useCallback(async (): Promise<IPSentinelRun[]> => {
    try {
      const data = await api.get<{ runs: IPSentinelRun[] }>(`/nodes/${nodeId}/ip-sentinel/runs`);
      const r = data.runs ?? [];
      const latestDetect = r.find(x => x.task_type === "detect" && x.status === "success" && x.result);
      setRuns(r);
      if (latestDetect?.result) setDetectResult(latestDetect.result as IPDetectResult);
      return r;
    } catch { return []; }
  }, [nodeId]);

  useEffect(() => {
    if (!open) return;
    setLoaded(false);
    Promise.all([
      api.get<NodeIPSentinelConfig>(`/nodes/${nodeId}/ip-sentinel/config`).then(d => {
        setConfig(d); setDraftCode(d.region_code ?? ""); setDraftName(d.region_name ?? "");
      }).catch(() => {}),
      fetchRuns(),
    ]).finally(() => setLoaded(true));
    return () => { if (pollTimerRef.current) clearTimeout(pollTimerRef.current); };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, nodeId]);

  function schedulePoll() {
    if (pollTimerRef.current) clearTimeout(pollTimerRef.current);
    pollTimerRef.current = setTimeout(async () => {
      pollCountRef.current += 1;
      const r = await fetchRuns();
      const latest = r[0];
      if ((latest?.status === "pending" || latest?.status === "running") && pollCountRef.current < 100) schedulePoll();
    }, 3000);
  }

  async function handleDetect() {
    setDetectLoading(true);
    try {
      const result = await api.post<IPDetectResult>(`/nodes/${nodeId}/ip-sentinel/detect`, {});
      setDetectResult(result);
      toast(`检测完成：${result.ip}（${result.country} · ${result.city}）`, "success");
      fetchRuns();
    } catch (err) { if (!handleAuthError(err)) toast("检测失败", "error"); }
    finally { setDetectLoading(false); }
  }

  async function handleRun() {
    setRunLoading(true);
    try {
      await api.post(`/nodes/${nodeId}/ip-sentinel/run`, {});
      toast("任务已提交，轮询结果中…", "success");
      pollCountRef.current = 0;
      schedulePoll();
    } catch (err) { if (!handleAuthError(err)) toast("提交失败", "error"); }
    finally { setRunLoading(false); }
  }

  async function handleSaveConfig() {
    setConfigSaving(true);
    try {
      await api.put(`/nodes/${nodeId}/ip-sentinel/config`, { region_code: draftCode, region_name: draftName });
      setConfig({ region_code: draftCode, region_name: draftName });
      toast("地区设置已保存", "success");
    } catch (err) { if (!handleAuthError(err)) toast("保存失败", "error"); }
    finally { setConfigSaving(false); }
  }

  const isPreset = REGION_PRESETS.some(p => p.code === draftCode);
  const latestRun = runs[0] ?? null;
  const recentRuns = runs.slice(0, 5);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="h-4 w-4 text-[hsl(var(--primary))]"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" /></svg>
            IP Sentinel
          </DialogTitle>
          <DialogDescription>{nodeName}</DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          {/* IP 检测结果 */}
          <div className="flex items-center justify-between rounded-md border border-[hsl(var(--border))] px-3 py-2">
            <div>
              {detectResult ? (
                <>
                  <p className="font-mono text-sm font-medium">{detectResult.ip}</p>
                  <p className="text-[11px] text-[hsl(var(--muted-foreground))]">{detectResult.country_code} · {detectResult.city}</p>
                </>
              ) : (
                <p className="text-[12px] text-[hsl(var(--muted-foreground))]">未检测</p>
              )}
              {latestRun && (
                <span className={`mt-1 inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-medium ${sentinelStatusClass(latestRun.status)}`}>
                  {sentinelStatusLabel(latestRun.status)}
                </span>
              )}
            </div>
            <div className="flex gap-2">
              <Button size="sm" variant="outline" className="h-7 text-xs px-2" disabled={detectLoading || !loaded} onClick={handleDetect}>
                {detectLoading ? <svg className="h-3 w-3 animate-spin" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24"><path d="M21 12a9 9 0 1 1-6.219-8.56" stroke="currentColor" strokeWidth={2} strokeLinecap="round"/></svg> : "检测 IP"}
              </Button>
              <Button size="sm" variant="outline" className="h-7 text-xs px-2" disabled={runLoading || !loaded} onClick={handleRun}>
                {runLoading ? <svg className="h-3 w-3 animate-spin" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24"><path d="M21 12a9 9 0 1 1-6.219-8.56" stroke="currentColor" strokeWidth={2} strokeLinecap="round"/></svg> : "立即执行"}
              </Button>
            </div>
          </div>

          {/* 地区配置 */}
          <div className="space-y-2">
            <Label className="text-xs text-[hsl(var(--muted-foreground))]">地区设置</Label>
            <Select value={isPreset ? draftCode : undefined} onValueChange={v => { const p = REGION_PRESETS.find(x => x.code === v); if (p) { setDraftCode(p.code); setDraftName(p.name); } }}>
              <SelectTrigger className="h-8 text-xs"><SelectValue placeholder="— 选择预设 —" /></SelectTrigger>
              <SelectContent>
                {REGION_PRESETS.map(p => <SelectItem key={p.code} value={p.code} className="text-xs">{p.code} — {p.name}</SelectItem>)}
              </SelectContent>
            </Select>
            <div className="grid grid-cols-2 gap-2">
              <Input placeholder="代码（如 US）" value={draftCode} onChange={e => setDraftCode(e.target.value.toUpperCase())} className="h-8 text-xs" />
              <Input placeholder="名称（如 United States）" value={draftName} onChange={e => setDraftName(e.target.value)} className="h-8 text-xs" />
            </div>
            <div className="flex items-center justify-between">
              <p className="text-[11px] text-[hsl(var(--muted-foreground))]">已保存：{config.region_code || "—"} {config.region_name}</p>
              <Button size="sm" variant="outline" className="h-7 text-xs px-3" disabled={configSaving || !loaded} onClick={handleSaveConfig}>
                {configSaving ? "保存中…" : "保存"}
              </Button>
            </div>
          </div>

          {/* 执行记录 */}
          <div>
            <button className="flex w-full items-center justify-between py-1 text-xs text-[hsl(var(--muted-foreground))] hover:text-[hsl(var(--foreground))] transition-colors" onClick={() => setRunsExpanded(v => !v)}>
              <span className="font-medium">最近执行记录</span>
              <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className={`h-3.5 w-3.5 transition-transform ${runsExpanded ? "rotate-180" : ""}`}><polyline points="6 9 12 15 18 9" /></svg>
            </button>
            {runsExpanded && (
              <div className="mt-1.5 rounded-md border border-[hsl(var(--border))] overflow-hidden">
                {recentRuns.length === 0 ? (
                  <p className="py-3 text-center text-xs text-[hsl(var(--muted-foreground))]">暂无记录</p>
                ) : (
                  <table className="w-full text-sm">
                    <thead><tr className="border-b border-[hsl(var(--border))] bg-[hsl(var(--muted))]">
                      <th className="py-1.5 pl-3 pr-2 text-left text-[10px] font-medium text-[hsl(var(--muted-foreground))]">时间</th>
                      <th className="py-1.5 pr-2 text-left text-[10px] font-medium text-[hsl(var(--muted-foreground))]">触发</th>
                      <th className="py-1.5 pr-2 text-left text-[10px] font-medium text-[hsl(var(--muted-foreground))]">状态</th>
                      <th className="py-1.5 pr-3 text-right text-[10px] font-medium text-[hsl(var(--muted-foreground))]">耗时</th>
                    </tr></thead>
                    <tbody>{recentRuns.map(run => <SentinelRunRow key={run.id} run={run} />)}</tbody>
                  </table>
                )}
              </div>
            )}
          </div>
        </div>

        <DialogFooter>
          <DialogClose asChild><Button variant="outline" size="sm">关闭</Button></DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── SentinelScheduleBar ───────────────────────────────────────────

function SentinelScheduleBar() {
  const handleAuthError = useAuthErrorHandler();
  const [schedule, setSchedule] = useState<SentinelSchedule | null>(null);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [saving, setSaving] = useState(false);
  const [runningAll, setRunningAll] = useState(false);

  useEffect(() => {
    api.get<SentinelSchedule>("/ip-sentinel/schedule")
      .then(setSchedule)
      .catch(() => {});
  }, []);

  async function handleSave() {
    const hours = parseInt(draft, 10);
    if (isNaN(hours) || hours < 1) { toast("请输入有效的小时数（≥1）", "error"); return; }
    setSaving(true);
    try {
      const res = await api.put<{ ok: boolean; interval_hours: number }>("/ip-sentinel/schedule", { interval_hours: hours });
      setSchedule(prev => prev ? { ...prev, interval_hours: res.interval_hours } : null);
      setEditing(false);
      toast(`执行间隔已更新为 ${res.interval_hours} 小时`, "success");
    } catch (err) { if (!handleAuthError(err)) toast("保存失败", "error"); }
    finally { setSaving(false); }
  }

  return (
    <div className="mb-4 flex flex-wrap items-center gap-x-4 gap-y-1.5 rounded-lg border border-[hsl(var(--border))] bg-[hsl(var(--card))] px-4 py-2.5 text-xs">
      <span className="flex items-center gap-1.5 text-[hsl(var(--muted-foreground))]">
        <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="h-3.5 w-3.5"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" /></svg>
        Sentinel 调度
      </span>
      <div className="h-3 w-px bg-[hsl(var(--border))] hidden sm:block" />
      <div className="flex items-center gap-1.5">
        <span className="text-[hsl(var(--muted-foreground))]">间隔</span>
        {editing ? (
          <div className="flex items-center gap-1">
            <Input type="number" min={1} value={draft} onChange={e => setDraft(e.target.value)} onKeyDown={e => { if (e.key === "Enter") handleSave(); if (e.key === "Escape") setEditing(false); }} className="h-6 w-16 text-xs" autoFocus />
            <span className="text-[hsl(var(--muted-foreground))]">h</span>
            <Button size="sm" className="h-6 text-xs px-2" disabled={saving} onClick={handleSave}>{saving ? "…" : "确定"}</Button>
            <Button size="sm" variant="ghost" className="h-6 text-xs px-1" onClick={() => setEditing(false)}>取消</Button>
          </div>
        ) : (
          <button className="flex items-center gap-0.5 font-medium hover:text-[hsl(var(--primary))] transition-colors" onClick={() => { setDraft(String(schedule?.interval_hours ?? "1")); setEditing(true); }}>
            {schedule ? `${schedule.interval_hours}h` : "--"}
            <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="h-3 w-3 opacity-50"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
          </button>
        )}
      </div>
      <span className="text-[hsl(var(--muted-foreground))]">上次：<span className="text-[hsl(var(--foreground))]">{sentinelFormatTime(schedule?.last_run_at)}</span></span>
      <span className="text-[hsl(var(--muted-foreground))]">下次：<span className="text-[hsl(var(--foreground))]">{sentinelFormatTime(schedule?.next_run_at)}</span></span>
      <Button size="sm" variant="outline" className="h-6 text-xs px-2 ml-auto" disabled={runningAll} onClick={async () => {
        setRunningAll(true);
        try { await api.post("/ip-sentinel/run-all", {}); toast("已触发全部节点执行", "success"); }
        catch (err) { if (!handleAuthError(err)) toast("触发失败", "error"); }
        finally { setRunningAll(false); }
      }}>
        {runningAll ? "触发中…" : "全部执行"}
      </Button>
    </div>
  );
}

function speedStyle(cur: number, prev: number): React.CSSProperties {
  if (cur === 0) return { color: "hsl(var(--muted-foreground))" };
  if (cur > prev) return { color: "#10b981" }; // emerald-500
  if (cur < prev) return { color: "#fb923c" }; // orange-400
  return { color: "hsl(var(--foreground))" };
}

function countryFlag(code: string): string {
  if (!code || code.length !== 2) return "";
  return String.fromCodePoint(...[...code.toUpperCase()].map(c => 0x1F1E6 - 65 + c.charCodeAt(0)));
}

function NodeCard({ node, runtime, metrics, onEdit, onDelete, onOpenDetail, onRestart, onSpeedtest, speedtestLoading, speedtestResult, onCheck, checkLoading, checkResult, prevMetrics, onUpdate, updateLoading, onManualUpdate, latestVersion, geoInfo }: NodeCardProps) {
  const [tracerouteOpen, setTracerouteOpen] = useState(false);
  const [sentinelOpen, setSentinelOpen] = useState(false);
  const totalTraffic = node.upload_bytes + node.download_bytes;

  const online = !!node.online;
  const statusBadge = (
    <Badge
      className={online
        ? "bg-green-600 text-white hover:bg-green-600"
        : ""}
      variant={online ? undefined : "destructive"}
      title="基于 gRPC 长连接判定"
    >
      {online ? "在线" : "离线"}
    </Badge>
  );

  return (
    <>
    <Card className={`flex flex-col${node.disabled ? " opacity-60" : ""}`}>
      <CardHeader className="flex flex-row items-start justify-between space-y-0 pb-2 pt-3">
        <div className="min-w-0 flex-1">
          <CardTitle className="truncate text-base font-semibold">
            <Link
              to="/panel/nodes/$nodeId"
              params={{ nodeId: node.id }}
              className="hover:underline"
            >
              {node.name}
            </Link>
          </CardTitle>
          <CardDescription className="mt-1 truncate font-mono text-xs">
            {node.base_url}
          </CardDescription>
          {geoInfo && (
            <p className="mt-0.5 truncate text-xs text-[hsl(var(--muted-foreground))]">
              {countryFlag(geoInfo.country_code)} {[geoInfo.country_name, geoInfo.city].filter(Boolean).join(" · ")}
              {geoInfo.asn_org && <span className="ml-1 opacity-70">/ {geoInfo.asn_org}</span>}
            </p>
          )}
        </div>
        <div className="ml-2 flex shrink-0 items-center gap-2">
          {node.disabled
            ? <Badge variant="secondary">已禁用</Badge>
            : statusBadge}
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="sm" className="h-8 w-8 p-0">
                <IconMore className="h-4 w-4" />
                <span className="sr-only">操作菜单</span>
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onClick={() => onEdit(node)}>
                编辑
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => setTracerouteOpen(true)}>
                路由追踪
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => setSentinelOpen(true)}>
                IP Sentinel
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => onUpdate(node)} disabled={updateLoading}>
                {updateLoading ? "更新中…" : "更新节点"}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => onManualUpdate(node)}>
                手动更新
              </DropdownMenuItem>
              <DropdownMenuItem
                className="text-[hsl(var(--destructive))]"
                onClick={() => onDelete(node)}
              >
                删除
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </CardHeader>

      <CardContent className="flex-1 pt-0">
        {/* ── 累计流量（系统网卡流量，不含倍率）── */}
        <div className="flex items-center gap-3 py-2 text-xs font-mono" title="系统网卡实际流量，不含计费倍率">
          <span className="flex items-center gap-1 text-[hsl(var(--muted-foreground))]">
            <IconUpload className="h-3 w-3" />
            {formatBytes(node.upload_bytes)}
          </span>
          <span className="flex items-center gap-1 text-[hsl(var(--muted-foreground))]">
            <IconDownload className="h-3 w-3" />
            {formatBytes(node.download_bytes)}
          </span>
          <span className="ml-auto font-medium text-[hsl(var(--foreground))]">
            {formatBytes(totalTraffic)}
          </span>
        </div>

        {/* ── 实时网速 ── */}
        {metrics?.running && (
          <div className="flex items-center gap-3 border-t border-[hsl(var(--border))] py-2 text-xs font-mono">
            <span className="flex items-center gap-1" style={speedStyle(metrics.upload_speed, prevMetrics?.upload_speed ?? metrics.upload_speed)}>
              <IconUpload className="h-3 w-3" />
              {formatSpeed(metrics.upload_speed)}
            </span>
            <span className="flex items-center gap-1" style={speedStyle(metrics.download_speed, prevMetrics?.download_speed ?? metrics.download_speed)}>
              <IconDownload className="h-3 w-3" />
              {formatSpeed(metrics.download_speed)}
            </span>
            {metrics.connections > 0 && (
              <span className="ml-auto flex items-center gap-1 text-[hsl(var(--muted-foreground))]">
                <span className="h-1.5 w-1.5 rounded-full bg-green-500" />
                {metrics.connections} 连接
              </span>
            )}
          </div>
        )}

        {/* ── 到期 / 面板 / 备注 ── */}
        {(node.expire_at || node.panel_url || node.remark) && (
          <div className="flex items-center justify-between gap-2 border-t border-[hsl(var(--border))] py-1.5 text-xs">
            {node.expire_at && (() => {
              const d = new Date(node.expire_at);
              const daysLeft = Math.ceil((d.getTime() - Date.now()) / 86400000);
              const color =
                daysLeft <= 0  ? "text-red-500" :
                daysLeft <= 3  ? "text-orange-500" :
                daysLeft <= 7  ? "text-amber-500" :
                daysLeft <= 15 ? "text-yellow-500" :
                daysLeft <= 30 ? "text-yellow-400/70" :
                "text-[hsl(var(--muted-foreground))]";
              return (
                <span className={`font-medium ${color}`}>
                  {d.toLocaleDateString("zh-CN")}{daysLeft > 0 ? `（${daysLeft}天）` : "（已到期）"}
                </span>
              );
            })()}
            {node.panel_url && (
              <a href={node.panel_url} target="_blank" rel="noopener noreferrer"
                className="min-w-0 truncate text-[hsl(var(--muted-foreground))] hover:text-[hsl(var(--foreground))] hover:underline">
                {node.panel_url.replace(/^https?:\/\//, "")}
              </a>
            )}
            {node.remark && (
              <span className="min-w-0 truncate text-[hsl(var(--muted-foreground))]">{node.remark}</span>
            )}
          </div>
        )}

        {/* ── 版本信息 ── */}
        {(runtime?.version || runtime?.node_version) && (
          <div className="flex items-center justify-between border-t border-[hsl(var(--border))] py-1.5 text-[10px] font-mono text-[hsl(var(--muted-foreground))]">
            <div className="flex items-center gap-2">
              {runtime.node_version && <span>node <span className="text-[hsl(var(--foreground))]">{runtime.node_version}</span></span>}
              {runtime.version && <span>xray <span className="text-[hsl(var(--foreground))]">{runtime.version}</span></span>}
            </div>
            {latestVersion && runtime.node_version && latestVersion !== runtime.node_version && (
              <span className="text-amber-500">→ {latestVersion}</span>
            )}
          </div>
        )}

        <div className="border-t border-[hsl(var(--border))] pt-2" />

        <div className="flex flex-wrap gap-1">
          <Button variant="ghost" size="sm" className="h-8 px-2 text-xs" onClick={() => onOpenDetail(node, "config")} title="配置">
            配置
          </Button>
          <Button variant="ghost" size="sm" className="h-8 px-2 text-xs" onClick={() => onOpenDetail(node, "logs")} title="日志">
            日志
          </Button>
          <Button variant="ghost" size="sm" className="h-8 px-2 text-xs" onClick={() => onRestart(node)} title="重启">
            重启
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="h-8 px-2 text-xs"
            onClick={() => onSpeedtest(node)}
            disabled={speedtestLoading}
            title="测速"
          >
            {speedtestLoading ? (
              <>
                <svg className="mr-1 h-3 w-3 animate-spin" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
                  <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                  <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                </svg>
                测速中…
              </>
            ) : "测速"}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="h-8 px-2 text-xs"
            onClick={() => onCheck(node)}
            disabled={checkLoading}
            title="解锁检测"
          >
            {checkLoading ? (
              <>
                <svg className="mr-1 h-3 w-3 animate-spin" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
                  <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                  <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                </svg>
                检测中…
              </>
            ) : "解锁检测"}
          </Button>
        </div>

        {speedtestResult && (
          <div className="mt-2 rounded-md bg-[hsl(var(--muted))] px-3 py-2">
            <div className="flex items-center gap-3 text-xs font-mono">
              <span className="text-[hsl(var(--foreground))]">↓ {(speedtestResult.down_bps / 1_000_000).toFixed(1)} Mbps</span>
              <span className="text-[hsl(var(--foreground))]">↑ {(speedtestResult.up_bps / 1_000_000).toFixed(1)} Mbps</span>
            </div>
            <p className="mt-1 text-[10px] text-[hsl(var(--muted-foreground))]">
              测试于 {new Date(speedtestResult.tested_at).toLocaleString("zh-CN")}
            </p>
          </div>
        )}

        {checkResult && (
          <div className="mt-2 rounded-md bg-[hsl(var(--muted))] px-3 py-2">
            <div className="flex flex-wrap gap-1.5">
              {(checkResult.proxied?.length ? checkResult.proxied : checkResult.direct).map((item) => (
                <span
                  key={item.service}
                  className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-medium ${
                    item.unlocked
                      ? "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400"
                      : "bg-red-500/15 text-red-600 dark:text-red-400"
                  }`}
                  title={item.note || undefined}
                >
                  <span className={`h-1.5 w-1.5 rounded-full ${item.unlocked ? "bg-emerald-500" : "bg-red-500"}`} />
                  {item.service}{item.region ? ` (${item.region})` : ""}
                </span>
              ))}
            </div>
          </div>
        )}
      </CardContent>
    </Card>

    <TracerouteDialog node={node} open={tracerouteOpen} onOpenChange={setTracerouteOpen} />
    <IPSentinelDialog nodeId={node.id} nodeName={node.name} open={sentinelOpen} onOpenChange={setSentinelOpen} />
    </>
  );
}

// ── Main Page ────────────────────────────────────────────────────

export default function NodesPage() {

  const [nodes, setNodes] = useState<Node[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Form dialog state
  const [formOpen, setFormOpen] = useState(false);
  const [editingNode, setEditingNode] = useState<Node | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // Install command dialog (post-create)
  const [installNode, setInstallNode] = useState<Node | null>(null);
  const [installOpen, setInstallOpen] = useState(false);

  // Manual update dialog
  const [manualUpdateNode, setManualUpdateNode] = useState<Node | null>(null);
  const [manualUpdateOpen, setManualUpdateOpen] = useState(false);

  // Delete dialog state
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deletingNode, setDeletingNode] = useState<Node | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [restartNode, setRestartNode] = useState<Node | null>(null);

  // Node runtime info (versions)
  const [nodeRuntimes, setNodeRuntimes] = useState<Map<string, RuntimeInfo>>(new Map());

  // Detail dialog state
  const [detailOpen, setDetailOpen] = useState(false);
  const [detailNode, setDetailNode] = useState<Node | null>(null);
  const [detailMode, setDetailMode] = useState<DetailMode>(null);

  // Speedtest state
  const [speedtestLoading, setSpeedtestLoading] = useState<Set<string>>(new Set());
  const [speedtestResults, setSpeedtestResults] = useState<Map<string, SpeedtestResult>>(new Map());

  // Check state
  const [checkLoading, setCheckLoading] = useState<Set<string>>(new Set());
  const [checkResults, setCheckResults] = useState<Map<string, CheckResult>>(new Map());

  // Update state
  const [updateLoading, setUpdateLoading] = useState<Set<string>>(new Set());
  const [latestVersion, setLatestVersion] = useState<string | null>(null);

  // GeoIP state
  const [geoInfoMap, setGeoInfoMap] = useState<Map<string, GeoInfo>>(new Map());

  // Realtime metrics
  const [nodeMetrics, setNodeMetrics] = useState<Map<string, NodeMetrics>>(new Map());
  const [prevNodeMetrics, setPrevNodeMetrics] = useState<Map<string, NodeMetrics>>(new Map());

  // ── Auth error handler ───────────────────────────────────────
  const handleAuthError = useAuthErrorHandler();

  // ── Fetch nodes ──────────────────────────────────────────────
  const fetchNodes = useCallback((silent = false) => {
    if (!silent) {
      setLoading(true);
      setError(null);
    }
    return api
      .get<NodesResponse>("/nodes")
      .then((res) => setNodes(res.nodes ?? []))
      .catch((err) => {
        if (!silent && !handleAuthError(err)) {
          setError(err instanceof Error ? err.message : "加载失败");
        }
      })
      .finally(() => { if (!silent) setLoading(false); });
  }, [handleAuthError]);

  useEffect(() => {
    fetchNodes();
  }, [fetchNodes]);

  // 每 5 秒静默刷新节点列表，更新在线状态（来自 server hub）。
  useEffect(() => {
    const id = setInterval(() => { fetchNodes(true); }, 5000);
    return () => clearInterval(id);
  }, [fetchNodes]);

  // ── Fetch latest version for update badge ────────────────────
  useEffect(() => {
    api.get<{ latest: string }>("/system/update/check")
      .then((r) => setLatestVersion(r.latest))
      .catch(() => {}); // 静默失败
  }, []);

  // ── Fetch GeoIP info (静默，数据库未就绪时忽略) ───────────────
  useEffect(() => {
    if (nodes.length === 0) return;
    api.get<{ results: { node_id: string; info?: GeoInfo }[] }>("/nodes/geoip")
      .then((r) => {
        const map = new Map<string, GeoInfo>();
        r.results.forEach(({ node_id, info }) => { if (info) map.set(node_id, info); });
        setGeoInfoMap(map);
      })
      .catch(() => {}); // 数据库未就绪时静默跳过
  }, [nodes]);

  // ── Fetch runtime info (versions) after nodes load ───────────
  useEffect(() => {
    if (nodes.length === 0) return;
    nodes.forEach((node) => {
      api.get<RuntimeInfo>(`/nodes/${node.id}/runtime`).then((rt) => {
        setNodeRuntimes((prev) => {
          const next = new Map(prev);
          next.set(node.id, rt);
          return next;
        });
      }).catch(() => {}); // 节点离线时忽略错误
    });
  }, [nodes]);

  // ── Realtime metrics via SSE ──────────────────────────────────
  useEffect(() => {
    if (nodes.length === 0) return;
    const token = getToken();
    if (!token) return;

    let cancelled = false;

    (async () => {
      try {
        const res = await fetch("/v1/nodes/metrics/stream", {
          headers: { Authorization: `Bearer ${token}` },
        });
        if (!res.ok || !res.body) return;

        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buf = "";

        while (!cancelled) {
          const { done, value } = await reader.read();
          if (done) break;
          buf += decoder.decode(value, { stream: true });

          const events = buf.split("\n\n");
          buf = events.pop() ?? "";
          for (const event of events) {
            const line = event.trim();
            if (!line.startsWith("data: ")) continue;
            try {
              const items: NodeMetrics[] = JSON.parse(line.slice(6));
              setNodeMetrics((prev) => {
                setPrevNodeMetrics(new Map(prev));
                const next = new Map(prev);
                for (const item of items) next.set(item.node_id, item);
                return next;
              });
            } catch {}
          }
        }
      } catch {}
    })();

    return () => { cancelled = true; };
  }, [nodes]);

  // ── Create / Update ──────────────────────────────────────────
  const handleSubmit = async (data: CreateNodeRequest) => {
    setSubmitting(true);
    try {
      if (editingNode) {
        await api.put<Node>(`/nodes/${editingNode.id}`, data);
        setFormOpen(false);
        setEditingNode(null);
        fetchNodes();
      } else {
        const created = await api.post<Node>("/nodes", data);
        setFormOpen(false);
        setInstallNode(created);
        setInstallOpen(true);
        fetchNodes();
      }
    } catch (err) {
      if (!handleAuthError(err)) {
        throw err;
      }
    } finally {
      setSubmitting(false);
    }
  };

  // ── Delete ───────────────────────────────────────────────────
  const handleDelete = async () => {
    if (!deletingNode) return;
    setDeleting(true);
    try {
      await api.del(`/nodes/${deletingNode.id}`);
      setDeleteOpen(false);
      setDeletingNode(null);
      fetchNodes();
    } catch (err) {
      if (!handleAuthError(err)) {
        // keep dialog open on error so user can retry
      }
    } finally {
      setDeleting(false);
    }
  };

  // ── Open edit ────────────────────────────────────────────────
  const openEdit = (node: Node) => {
    setEditingNode(node);
    setFormOpen(true);
  };

  // ── Open delete ──────────────────────────────────────────────
  const openDelete = (node: Node) => {
    setDeletingNode(node);
    setDeleteOpen(true);
  };

  // ── Open create ──────────────────────────────────────────────
  const openCreate = () => {
    setEditingNode(null);
    setFormOpen(true);
  };

  // ── Open detail dialog ──────────────────────────────────────
  const openDetail = (node: Node, mode: DetailMode) => {
    setDetailNode(node);
    setDetailMode(mode);
    setDetailOpen(true);
  };

  // ── Restart node ────────────────────────────────────────────
  const handleRestart = (node: Node) => {
    setRestartNode(node);
  };

  const doRestart = async () => {
    const node = restartNode;
    setRestartNode(null);
    if (!node) return;
    try {
      await api.post<any>(`/nodes/${node.id}/runtime/restart`, { config: "" });
      toast(`节点 ${node.name} 重启成功`, "success");
      fetchNodes(true);
    } catch (err) {
      if (!handleAuthError(err)) {
        toast(`重启失败：${err instanceof Error ? err.message : "未知错误"}`, "error");
      }
    }
  };

  // ── Update node ───────────────────────────────────────────────
  const handleUpdate = async (node: Node) => {
    setUpdateLoading((prev) => { const next = new Set(prev); next.add(node.id); return next; });
    try {
      await api.post(`/nodes/${node.id}/update`, {});
      toast(`节点 ${node.name} 更新已开始，节点将自动重启`, "success");
    } catch (err) {
      if (!handleAuthError(err)) {
        toast(`更新失败：${err instanceof Error ? err.message : "未知错误"}`, "error");
      }
    } finally {
      setUpdateLoading((prev) => { const next = new Set(prev); next.delete(node.id); return next; });
    }
  };

  // ── Speedtest node ────────────────────────────────────────────
  const handleSpeedtest = async (node: Node) => {
    setSpeedtestLoading((prev) => {
      const next = new Set(prev);
      next.add(node.id);
      return next;
    });
    try {
      const result = await api.post<SpeedtestResult>(`/nodes/${node.id}/runtime/speedtest`, {});
      setSpeedtestResults((prev) => {
        const next = new Map(prev);
        next.set(node.id, result);
        return next;
      });
    } catch (err) {
      if (!handleAuthError(err)) {
        toast(`测速失败：${err instanceof Error ? err.message : "未知错误"}`, "error");
      }
    } finally {
      setSpeedtestLoading((prev) => {
        const next = new Set(prev);
        next.delete(node.id);
        return next;
      });
    }
  };

  // ── Check node ────────────────────────────────────────────────
  const handleCheck = async (node: Node) => {
    setCheckLoading((prev) => { const next = new Set(prev); next.add(node.id); return next; });
    try {
      const result = await api.post<CheckResult>(`/nodes/${node.id}/runtime/check`, {});
      setCheckResults((prev) => { const next = new Map(prev); next.set(node.id, result); return next; });
    } catch (err) {
      if (!handleAuthError(err)) {
        toast(`解锁检测失败：${err instanceof Error ? err.message : "未知错误"}`, "error");
      }
    } finally {
      setCheckLoading((prev) => { const next = new Set(prev); next.delete(node.id); return next; });
    }
  };

  // ── Error state (full-page) ──────────────────────────────────
  if (error && !nodes.length) {
    return (
      <div className="flex h-full items-center justify-center p-8">
        <Card className="w-full max-w-md">
          <CardContent className="pt-6 text-center">
            <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-[hsl(var(--destructive))]/10 text-[hsl(var(--destructive))]">
              <IconAlert className="h-6 w-6" />
            </div>
            <p className="mb-1 font-semibold text-[hsl(var(--foreground))]">加载失败</p>
            <p className="mb-4 text-sm text-[hsl(var(--muted-foreground))]">{error}</p>
            <Button onClick={() => { fetchNodes(); }} variant="outline">
              <IconRefresh className="mr-2 h-4 w-4" />
              重试
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="p-4 md:p-6 lg:p-8">
      {/* ── Header ──────────────────────────────────────────────── */}
      <div className="mb-6 flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold text-[hsl(var(--foreground))]">节点</h1>
          <p className="mt-1 text-sm text-[hsl(var(--muted-foreground))]">
            管理代理节点及其连接配置。
          </p>
        </div>
        <Button onClick={openCreate}>
          <IconPlus className="mr-2 h-4 w-4" />
          添加节点
        </Button>
      </div>

      <SentinelScheduleBar />

      {/* ── Loading skeleton ────────────────────────────────────── */}
      {loading && (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <SkeletonCard key={i} />
          ))}
        </div>
      )}

      {/* ── Empty state ─────────────────────────────────────────── */}
      {!loading && nodes.length === 0 && (
        <Card>
          <CardContent className="py-16 text-center">
            <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-[hsl(var(--muted))]">
              <IconServer className="h-6 w-6 text-[hsl(var(--muted-foreground))]" />
            </div>
            <p className="mb-1 font-semibold text-[hsl(var(--foreground))]">暂无节点</p>
            <p className="mb-4 text-sm text-[hsl(var(--muted-foreground))]">
              添加第一个节点开始使用。
            </p>
            <Button onClick={openCreate}>
              <IconPlus className="mr-2 h-4 w-4" />
              添加节点
            </Button>
          </CardContent>
        </Card>
      )}

      {/* ── Node grid ───────────────────────────────────────────── */}
      {!loading && nodes.length > 0 && (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {nodes.map((node) => (
            <NodeCard
              key={node.id}
              node={node}
              runtime={nodeRuntimes.get(node.id) ?? null}
              metrics={nodeMetrics.get(node.id) ?? null}
              prevMetrics={prevNodeMetrics.get(node.id) ?? null}
              onEdit={openEdit}
              onDelete={openDelete}
              onOpenDetail={openDetail}
              onRestart={handleRestart}
              onSpeedtest={handleSpeedtest}
              speedtestLoading={speedtestLoading.has(node.id)}
              speedtestResult={speedtestResults.get(node.id) ?? null}
              onCheck={handleCheck}
              checkLoading={checkLoading.has(node.id)}
              checkResult={checkResults.get(node.id) ?? null}
              onUpdate={handleUpdate}
              updateLoading={updateLoading.has(node.id)}
              onManualUpdate={(n) => { setManualUpdateNode(n); setManualUpdateOpen(true); }}
              latestVersion={latestVersion}
              geoInfo={geoInfoMap.get(node.id) ?? null}
            />
          ))}
        </div>
      )}

      {/* ── Create / Edit dialog ────────────────────────────────── */}
      <NodeFormDialog
        open={formOpen}
        onOpenChange={(open) => {
          setFormOpen(open);
          if (!open) setEditingNode(null);
        }}
        node={editingNode}
        onSubmit={handleSubmit}
        submitting={submitting}
      />

      {/* ── Install command dialog (post-create) ───────────────── */}
      <InstallCmdDialog
        node={installNode}
        open={installOpen}
        onClose={() => { setInstallOpen(false); setInstallNode(null); fetchNodes(); }}
      />

      {/* ── Manual update dialog ────────────────────────────────── */}
      <ManualUpdateDialog
        node={manualUpdateNode}
        open={manualUpdateOpen}
        onClose={() => { setManualUpdateOpen(false); setManualUpdateNode(null); }}
      />

      {/* ── Delete confirmation dialog ──────────────────────────── */}
      <DeleteDialog
        open={deleteOpen}
        onOpenChange={(open) => {
          setDeleteOpen(open);
          if (!open) setDeletingNode(null);
        }}
        node={deletingNode}
        onConfirm={handleDelete}
        deleting={deleting}
      />

      {/* ── Node detail dialog (status / config / logs) ─────────── */}
      <NodeDetailDialog
        open={detailOpen}
        onOpenChange={(open) => {
          setDetailOpen(open);
          if (!open) {
            setDetailNode(null);
            setDetailMode(null);
          }
        }}
        node={detailNode}
        mode={detailMode}
        handleAuthError={handleAuthError}
      />

      <ConfirmDialog
        open={restartNode !== null}
        onOpenChange={(open) => { if (!open) setRestartNode(null); }}
        title="确认重启"
        description={
          <>
            确定要重启节点{" "}
            <span className="font-medium text-[hsl(var(--foreground))]">
              {restartNode?.name}
            </span>{" "}
            吗？
          </>
        }
        confirmLabel="重启"
        variant="default"
        onConfirm={doRestart}
      />
    </div>
  );
}
