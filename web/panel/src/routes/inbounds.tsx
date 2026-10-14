import { useEffect, useState, useCallback, useMemo } from "react";
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
  Separator,
  Checkbox,
  MultiSelect,
  SingleSelect,
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  Tooltip,
  TooltipTrigger,
  TooltipContent,
  TooltipProvider,
  toast,
} from "@/components/ui";
import { ScrollArea } from "@/components/ui/scroll-area";
import { api, cfApi, nodeDomainApi } from "@/lib/api";
import { hostSubName } from "@/lib/format";
import { useAuthErrorHandler } from "@/hooks/useAuthErrorHandler";
import type {
  Inbound,
  InboundsResponse,
  InboundProtocol,
  CreateInboundRequest,
  Node,
  NodesResponse,
  Outbound,
  OutboundsResponse,
  Host,
  HostsResponse,
  User,
  UsersResponse,
  SSOutboundOption,
  SSOutboundOptionsResponse,
  CFDomain,
  NodeDomain,
  UserGroup,
  UserGroupsResponse,
} from "@/lib/types";

// ── Icons ────────────────────────────────────────────────────────

function IconPlus({ className }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <line x1="12" y1="5" x2="12" y2="19" />
      <line x1="5" y1="12" x2="19" y2="12" />
    </svg>
  );
}

function IconDice({ className }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <rect x="2" y="2" width="20" height="20" rx="2" />
      <circle cx="8" cy="8" r="1.5" fill="currentColor" />
      <circle cx="16" cy="8" r="1.5" fill="currentColor" />
      <circle cx="8" cy="16" r="1.5" fill="currentColor" />
      <circle cx="16" cy="16" r="1.5" fill="currentColor" />
    </svg>
  );
}

function IconKey({ className }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4" />
    </svg>
  );
}

function IconInbox({ className }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <polyline points="22 12 16 12 14 15 10 15 8 12 2 12" />
      <path d="M5.45 5.11L2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z" />
    </svg>
  );
}

function IconEdit({ className }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7" />
      <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z" />
    </svg>
  );
}

function IconTrash({ className }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <polyline points="3 6 5 6 21 6" />
      <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
    </svg>
  );
}

function IconMore({ className }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <circle cx="12" cy="5" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="12" cy="19" r="1"/>
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

function IconLoader({ className }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <line x1="12" y1="2" x2="12" y2="6" />
      <line x1="12" y1="18" x2="12" y2="22" />
      <line x1="4.93" y1="4.93" x2="7.76" y2="7.76" />
      <line x1="16.24" y1="16.24" x2="19.07" y2="19.07" />
      <line x1="2" y1="12" x2="6" y2="12" />
      <line x1="18" y1="12" x2="22" y2="12" />
      <line x1="4.93" y1="19.07" x2="7.76" y2="16.24" />
      <line x1="16.24" y1="7.76" x2="19.07" y2="4.93" />
    </svg>
  );
}

// ── Constants ────────────────────────────────────────────────────

const PROTOCOLS: InboundProtocol[] = ["anytls", "vless", "shadowsocks", "trojan", "hy2"];

const PROTOCOL_BADGE_VARIANT: Record<InboundProtocol, "default" | "secondary" | "outline"> = {
  vless: "default",
  trojan: "secondary",
  shadowsocks: "outline",
  anytls: "outline",
  hy2: "secondary",
};


const SS_METHODS = [
  "2022-blake3-aes-128-gcm",
  "2022-blake3-aes-256-gcm",
  "2022-blake3-chacha20-poly1305",
] as const;

// ── Helpers ──────────────────────────────────────────────────────

function randomPort(): number {
  return Math.floor(Math.random() * (65535 - 10000) + 10000);
}

function generateBase64Key(length: number): string {
  const bytes = new Uint8Array(length);
  crypto.getRandomValues(bytes);
  let binary = "";
  bytes.forEach((b) => (binary += String.fromCharCode(b)));
  return btoa(binary);
}

// ── Skeleton rows ────────────────────────────────────────────────

function SkeletonRows() {
  return (
    <>
      {Array.from({ length: 5 }).map((_, i) => (
        <TableRow key={i}>
          {Array.from({ length: 8 }).map((_, j) => (
            <TableCell key={j} className="px-4 py-3">
              <div className="h-4 w-full max-w-[100px] animate-pulse rounded bg-[hsl(var(--muted))]" />
            </TableCell>
          ))}
        </TableRow>
      ))}
    </>
  );
}

// ── Form state type ──────────────────────────────────────────────

interface InboundFormState {
  node_id: string;
  protocol: InboundProtocol;
  port: string;
  traffic_rate: string;
  outbound_id: string;
  security: string;
  reality_private_key: string;
  reality_public_key: string;
  reality_handshake_addr: string;
  reality_short_id: string;
  method: string;
  password: string;
  domain: string;
  // hy2 专属（保存时序列化进 Inbound.extra JSON）
  hy2_sni: string;
  hy2_obfs_enabled: boolean;
  hy2_obfs_password: string;
  hy2_masquerade_url: string;
  hy2_udp_idle_timeout_sec: string;
}

const EMPTY_FORM: InboundFormState = {
  node_id: "",
  protocol: "anytls",
  port: "",
  traffic_rate: "1",
  outbound_id: "",
  security: "tls",
  reality_private_key: "",
  reality_public_key: "",
  reality_handshake_addr: "www.microsoft.com",
  reality_short_id: "",
  method: "2022-blake3-aes-128-gcm",
  password: "",
  domain: "",
  hy2_sni: "",
  hy2_obfs_enabled: false,
  hy2_obfs_password: "",
  hy2_masquerade_url: "https://www.bing.com",
  hy2_udp_idle_timeout_sec: "60",
};

// ── Inbound Form Dialog ──────────────────────────────────────────

interface InboundFormDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  inbound?: Inbound | null;
  nodes: Node[];
  inbounds: Inbound[];
  outbounds: Outbound[];
  ssOutboundOptions: SSOutboundOption[];
  onSubmit: (data: CreateInboundRequest) => Promise<Inbound>;
  submitting: boolean;
  handleAuthError: (err: unknown) => boolean;
}

function InboundFormDialog({
  open,
  onOpenChange,
  inbound,
  nodes,
  inbounds,
  outbounds,
  ssOutboundOptions,
  onSubmit,
  submitting,
  handleAuthError,
}: InboundFormDialogProps) {
  const [form, setForm] = useState<InboundFormState>(EMPTY_FORM);
  const [generatingKeys, setGeneratingKeys] = useState(false);
  const [selectedNodeIds, setSelectedNodeIds] = useState<string[]>([]);
  const [userGroups, setUserGroups] = useState<UserGroup[]>([]);
  const [selectedGroupIds, setSelectedGroupIds] = useState<string[]>([]);

  const isEdit = !!inbound;

  // 加载用户组，组件 unmount 或 open 变化时取消飞行中的请求
  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    api.get<UserGroupsResponse>("/user-groups")
      .then((res) => { if (!cancelled) setUserGroups(res.user_groups ?? []); })
      .catch(() => { if (!cancelled) setUserGroups([]); });
    return () => { cancelled = true; };
  }, [open]);

  // 编辑模式：open/inbound/userGroups 任一变化时重新计算已选组
  // 创建模式（!inbound）始终清空
  useEffect(() => {
    if (!open) return;
    if (!inbound) {
      setSelectedGroupIds([]);
      return;
    }
    const ids = userGroups
      .filter((g) => g.inbound_ids?.split(",").filter(Boolean).includes(inbound.id))
      .map((g) => g.id);
    setSelectedGroupIds(ids);
  }, [open, inbound, userGroups]);

  // 重置表单
  useEffect(() => {
    if (!open) return;
    if (inbound) {
      // 解析 hy2 私有 Extra（容错：非法 JSON 视为空）
      let hy2: { sni?: string; masquerade_url?: string; udp_idle_timeout_sec?: number } = {};
      if (inbound.protocol === "hy2" && inbound.extra) {
        try { hy2 = JSON.parse(inbound.extra) ?? {}; } catch { hy2 = {}; }
      }
      setForm({
        node_id: inbound.node_id,
        protocol: inbound.protocol,
        port: String(inbound.port),
        traffic_rate: String(inbound.traffic_rate || 1),
        outbound_id: inbound.outbound_id || "",
        security: inbound.security || "none",
        reality_private_key: inbound.reality_private_key || "",
        reality_public_key: inbound.reality_public_key || "",
        reality_handshake_addr: inbound.reality_handshake_addr || "",
        reality_short_id: inbound.reality_short_id || "",
        method: inbound.method || "2022-blake3-aes-128-gcm",
        password: inbound.password || "",
        domain: inbound.domain || "",
        hy2_sni: hy2.sni || "",
        hy2_obfs_enabled: inbound.protocol === "hy2" && inbound.method === "salamander",
        hy2_obfs_password: inbound.protocol === "hy2" ? (inbound.password || "") : "",
        hy2_masquerade_url: hy2.masquerade_url || "https://www.bing.com",
        hy2_udp_idle_timeout_sec: hy2.udp_idle_timeout_sec ? String(hy2.udp_idle_timeout_sec) : "60",
      });
      setSelectedNodeIds([]);
    } else {
      const port = randomPort();
      setForm({
        ...EMPTY_FORM,
        port: String(port),
        node_id: nodes[0]?.id ?? "",
      });
      setSelectedNodeIds([]);
    }
  }, [open, inbound, nodes]);

  const updateField = <K extends keyof InboundFormState>(key: K, value: InboundFormState[K]) => {
    setForm((prev) => ({ ...prev, [key]: value }));
  };

  // 自动生成 Reality 密钥对
  const generateRealityKeys = async () => {
    setGeneratingKeys(true);
    try {
      const res = await api.get<{ private_key: string; public_key: string; short_id: string }>(
        "/tools/reality-keypair",
      );
      setForm((prev) => ({
        ...prev,
        reality_private_key: res.private_key,
        reality_public_key: res.public_key,
        reality_short_id: res.short_id,
      }));
    } catch (err) {
      if (!handleAuthError(err)) {
        // 静默失败，用户可手动填写
      }
    } finally {
      setGeneratingKeys(false);
    }
  };

  // 自动生成 Shadowsocks 密码
  const generateSSPassword = () => {
    const len = form.method.includes("128") ? 16 : 32;
    updateField("password", generateBase64Key(len));
  };

  // 随机端口
  const randomizePort = () => updateField("port", String(randomPort()));

  // 表单验证
  const canSubmit =
    (isEdit ? form.node_id !== "" : selectedNodeIds.length > 0) &&
    form.port !== "" &&
    Number(form.port) > 0 &&
    Number(form.port) <= 65535;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canSubmit || submitting) return;

    const baseData: CreateInboundRequest = {
      node_id: form.node_id,
      protocol: form.protocol,
      port: Number(form.port),
      traffic_rate: Number(form.traffic_rate) || 1,
      security: form.security,
      outbound_id: form.outbound_id || "",
      domain: "", // 显式清空废弃字段，防止编辑时携带旧值
    };

    if (form.security === "reality") {
      baseData.reality_private_key = form.reality_private_key;
      baseData.reality_public_key = form.reality_public_key;
      baseData.reality_handshake_addr = form.reality_handshake_addr;
      baseData.reality_short_id = form.reality_short_id;
    }

    if (form.protocol === "shadowsocks") {
      baseData.method = form.method;
      baseData.password = form.password;
    }

    if (form.protocol === "hy2") {
      // Plan A 字段复用：method=obfs 类型，password=obfs 密码
      baseData.method = form.hy2_obfs_enabled ? "salamander" : "";
      baseData.password = form.hy2_obfs_enabled ? form.hy2_obfs_password : "";
      const extra: Record<string, unknown> = {};
      if (form.hy2_sni.trim()) extra.sni = form.hy2_sni.trim();
      if (form.hy2_masquerade_url.trim()) extra.masquerade_url = form.hy2_masquerade_url.trim();
      const idle = Number(form.hy2_udp_idle_timeout_sec);
      if (idle > 0) extra.udp_idle_timeout_sec = idle;
      baseData.extra = JSON.stringify(extra);
    }

    if (isEdit) {
      baseData.node_id = form.node_id;
      await onSubmit(baseData);
      // 同步用户组关联：对比原始状态做 diff 更新
      if (inbound && userGroups.length > 0) {
        const originalIds = new Set(
          userGroups
            .filter((g) => g.inbound_ids?.split(",").filter(Boolean).includes(inbound.id))
            .map((g) => g.id)
        );
        const nextIds = new Set(selectedGroupIds);
        const toAdd = [...nextIds].filter((id) => !originalIds.has(id));
        const toRemove = [...originalIds].filter((id) => !nextIds.has(id));
        const failed: string[] = [];
        for (const gid of toAdd) {
          const group = userGroups.find((g) => g.id === gid);
          if (!group) continue;
          const existing = group.inbound_ids ? group.inbound_ids.split(",").filter(Boolean) : [];
          const merged = [...new Set([...existing, inbound.id])];
          try { await api.put(`/user-groups/${gid}`, { ...group, inbound_ids: merged.join(",") }); }
          catch { failed.push(group.name); }
        }
        for (const gid of toRemove) {
          const group = userGroups.find((g) => g.id === gid);
          if (!group) continue;
          const filtered = (group.inbound_ids ?? "").split(",").filter((id) => id && id !== inbound.id);
          try { await api.put(`/user-groups/${gid}`, { ...group, inbound_ids: filtered.join(",") }); }
          catch { failed.push(group.name); }
        }
        if (failed.length > 0) {
          toast(`入站已更新，但用户组同步失败：${failed.join("、")}`, "error");
        }
      }
    } else {
      const createdIds: string[] = [];
      const createErrors: string[] = [];
      for (const nodeId of selectedNodeIds) {
        try {
          const created = await onSubmit({ ...baseData, node_id: nodeId });
          createdIds.push(created.id);
        } catch (err) {
          createErrors.push(nodes.find((n) => n.id === nodeId)?.name ?? nodeId);
        }
      }
      if (createErrors.length > 0) {
        toast(`以下节点创建失败：${createErrors.join("、")}`, "error");
      }
      if (createdIds.length === 0) {
        return;
      }
      // 把成功创建的 inbound ID 追加到选中的用户组
      if (selectedGroupIds.length > 0) {
        const failed: string[] = [];
        for (const gid of selectedGroupIds) {
          const group = userGroups.find((g) => g.id === gid);
          if (!group) continue;
          const existing = group.inbound_ids ? group.inbound_ids.split(",").filter(Boolean) : [];
          const merged = [...new Set([...existing, ...createdIds])];
          try {
            await api.put(`/user-groups/${gid}`, { ...group, inbound_ids: merged.join(",") });
          } catch {
            failed.push(group.name);
          }
        }
        if (failed.length > 0) {
          toast(`已创建入站，但加入用户组失败：${failed.join("、")}`, "error");
        }
      }
    }
    onOpenChange(false);
  };

  const showReality = form.security === "reality";
  const showSS = form.protocol === "shadowsocks";
  const showHy2 = form.protocol === "hy2";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-lg">
        <form onSubmit={handleSubmit}>
          <DialogHeader>
            <DialogTitle>{isEdit ? "编辑入站" : "添加入站"}</DialogTitle>
            <DialogDescription>
              {isEdit ? "修改入站配置参数。" : "配置新的入站连接。"}
            </DialogDescription>
          </DialogHeader>

          <div className="mt-4 space-y-4">
            {/* ── Node selector ──────────────────────────────── */}
            {!isEdit ? (
              <div className="space-y-2">
                <Label>节点（可多选）</Label>
                <MultiSelect
                  value={selectedNodeIds}
                  onChange={setSelectedNodeIds}
                  options={nodes.map((n) => ({ value: n.id, label: n.name }))}
                  placeholder="选择节点..."
                  countLabel="已选 {n} 个节点"
                />
              </div>
            ) : (
              <div className="space-y-2">
                <Label>节点</Label>
                <Select value={form.node_id} onValueChange={(v) => updateField("node_id", v)}>
                  <SelectTrigger>
                    <SelectValue placeholder="选择节点" />
                  </SelectTrigger>
                  <SelectContent>
                    {nodes.map((n) => (
                      <SelectItem key={n.id} value={n.id}>
                        {n.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            )}

            {/* ── User group selector ───────────────────────── */}
            {userGroups.length > 0 && (
              <div className="space-y-2">
                <Label>用户组</Label>
                <MultiSelect
                  value={selectedGroupIds}
                  onChange={setSelectedGroupIds}
                  options={userGroups.map((g) => ({
                    value: g.id,
                    label: g.remark
                      ? `${g.name} · ${g.remark}`
                      : g.name,
                  }))}
                  placeholder="不加入用户组"
                  countLabel="已选 {n} 个用户组"
                />
              </div>
            )}

            {/* ── Protocol selector ─────────────────────────── */}
            <div className="space-y-2">
              <Label>协议</Label>
              <Select
                value={form.protocol}
                onValueChange={(v) => {
                  const protocol = v as InboundProtocol;
                  updateField("protocol", protocol);
                  if (protocol === "vless") {
                    updateField("security", "reality");
                    generateRealityKeys();
                  } else if (protocol === "trojan" || protocol === "anytls") {
                    updateField("security", "tls");
                  } else if (protocol === "hy2") {
                    updateField("security", "tls");
                  } else {
                    updateField("security", "none");
                  }
                  if (protocol === "shadowsocks") {
                    const len = form.method.includes("128") ? 16 : 32;
                    updateField("password", generateBase64Key(len));
                  }
                }}
                disabled={isEdit}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {PROTOCOLS.map((p) => (
                    <SelectItem key={p} value={p}>
                      {p}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            {/* ── Tag ───────────────────────────────────────── */}
            {/* ── Port + Random button ──────────────────────── */}
            <div className="space-y-2">
              <Label>端口</Label>
              <div className="flex gap-2">
                <Input
                  type="number"
                  min={1}
                  max={65535}
                  value={form.port}
                  onChange={(e) => updateField("port", e.target.value)}
                  className="flex-1"
                />
                <Button type="button" variant="outline" size="icon" onClick={randomizePort} title="随机端口">
                  <IconDice className="h-4 w-4" />
                </Button>
              </div>
            </div>

            {/* ── Traffic rate ──────────────────────────────── */}
            <div className="space-y-2">
              <Label>流量倍率</Label>
              <Input
                type="number"
                min={0.1}
                max={100}
                step={0.1}
                value={form.traffic_rate}
                onChange={(e) => updateField("traffic_rate", e.target.value)}
              />
            </div>

            {/* ── Outbound selector ─────────────────────────── */}
            <div className="space-y-2">
              <Label>出口</Label>
              <SingleSelect
                value={form.outbound_id || "__direct__"}
                onChange={(v) => updateField("outbound_id", v === "__direct__" ? "" : v)}
                options={[
                  { value: "__direct__", label: "direct (默认)" },
                  ...outbounds.map((ob) => ({
                    value: ob.id,
                    label: `${ob.name} · ${ob.server} (${ob.protocol})`,
                  })),
                  ...ssOutboundOptions.map((opt) => ({
                    value: opt.id,
                    label: opt.label,
                  })),
                ]}
                placeholder="direct (默认)"
              />
            </div>

            <Separator />

            {/* ── Security ──────────────────────────────────── */}
            <div className="space-y-2">
              <Label>安全</Label>
              <div className="flex h-9 items-center rounded-md border border-[hsl(var(--border))] bg-[hsl(var(--muted))] px-3 text-sm text-[hsl(var(--muted-foreground))]">
                {form.security}
              </div>
            </div>

            {/* ── Reality fields ─────────────────────────────── */}
            {showReality && (
              <div className="space-y-4 rounded-lg border border-[hsl(var(--border))] p-4">
                <div className="flex items-center justify-between">
                  <p className="text-sm font-medium text-[hsl(var(--foreground))]">Reality 配置</p>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={generateRealityKeys}
                    disabled={generatingKeys}
                  >
                    {generatingKeys ? (
                      <IconLoader className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                    ) : (
                      <IconKey className="mr-1.5 h-3.5 w-3.5" />
                    )}
                    生成
                  </Button>
                </div>
                <div className="space-y-2">
                  <Label>Private Key</Label>
                  <Input
                    value={form.reality_private_key}
                    onChange={(e) => updateField("reality_private_key", e.target.value)}
                    placeholder="私钥"
                    className="font-mono text-xs"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Public Key{isEdit ? "（只读，用于订阅链接）" : ""}</Label>
                  <Input
                    value={form.reality_public_key}
                    onChange={(e) => updateField("reality_public_key", e.target.value)}
                    placeholder="公钥"
                    className="font-mono text-xs"
                    readOnly={isEdit}
                  />
                </div>
                <div className="space-y-2">
                  <Label>Handshake 地址</Label>
                  <Input
                    value={form.reality_handshake_addr}
                    onChange={(e) => updateField("reality_handshake_addr", e.target.value)}
                    placeholder="www.microsoft.com"
                  />
                  <p className="text-xs text-[hsl(var(--muted-foreground))]">
                    建议用{" "}
                    <a
                      href="https://github.com/XTLS/RealiTLScanner"
                      target="_blank"
                      rel="noopener noreferrer"
                      className="underline underline-offset-2 hover:text-[hsl(var(--foreground))]"
                    >
                      RealiTLScanner
                    </a>{" "}
                    扫描节点 IP 获取最佳目标站点。
                  </p>
                </div>
                <div className="space-y-2">
                  <Label>Short ID</Label>
                  <Input
                    value={form.reality_short_id}
                    onChange={(e) => updateField("reality_short_id", e.target.value)}
                    placeholder="短 ID"
                    className="font-mono text-xs"
                  />
                </div>
              </div>
            )}

            {/* ── TLS note ───────────────────────────────────── */}
            {form.security === "tls" && (
              <div className="rounded-lg border border-[hsl(var(--border))] p-4">
                <p className="text-xs text-[hsl(var(--muted-foreground))]">Trojan TLS 由 NodeGate 负责终止，无需在此配置证书。</p>
              </div>
            )}

            {/* ── Shadowsocks fields ────────────────────────── */}
            {showSS && (
              <div className="space-y-4 rounded-lg border border-[hsl(var(--border))] p-4">
                <p className="text-sm font-medium text-[hsl(var(--foreground))]">Shadowsocks 配置</p>
                <div className="space-y-2">
                  <Label>加密方式</Label>
                  <Select value={form.method} onValueChange={(v) => {
                    updateField("method", v);
                    const len = v.includes("128") ? 16 : 32;
                    updateField("password", generateBase64Key(len));
                  }}>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {SS_METHODS.map((m) => (
                        <SelectItem key={m} value={m}>
                          {m}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-2">
                  <Label>密码</Label>
                  <div className="flex gap-2">
                    <Input
                      value={form.password}
                      onChange={(e) => updateField("password", e.target.value)}
                      placeholder="密码"
                      className="flex-1 font-mono text-xs"
                    />
                    <Button type="button" variant="outline" size="sm" onClick={generateSSPassword}>
                      生成
                    </Button>
                  </div>
                </div>
              </div>
            )}

            {showHy2 && (
              <div className="space-y-4 rounded-lg border border-[hsl(var(--border))] p-4">
                <p className="text-sm font-medium text-[hsl(var(--foreground))]">Hysteria2 配置</p>
                <p className="text-xs text-[hsl(var(--muted-foreground))]">
                  hy2 走 UDP/QUIC，必须由节点 Xray 加载 TLS 证书。请确保 SNI 域名已在节点 certmgr 中托管。
                </p>
                <div className="space-y-2">
                  <Label>SNI / 证书域名</Label>
                  <Input
                    value={form.hy2_sni}
                    onChange={(e) => updateField("hy2_sni", e.target.value)}
                    placeholder="hy.example.com"
                  />
                </div>
                <div className="space-y-2">
                  <Label>伪装 URL（masquerade）</Label>
                  <Input
                    value={form.hy2_masquerade_url}
                    onChange={(e) => updateField("hy2_masquerade_url", e.target.value)}
                    placeholder="https://www.bing.com"
                  />
                </div>
                <div className="space-y-2">
                  <Label>UDP 空闲超时（秒）</Label>
                  <Input
                    type="number"
                    min={10}
                    max={3600}
                    value={form.hy2_udp_idle_timeout_sec}
                    onChange={(e) => updateField("hy2_udp_idle_timeout_sec", e.target.value)}
                  />
                </div>
                <div className="flex items-center gap-2">
                  <input
                    id="hy2_obfs_enabled"
                    type="checkbox"
                    checked={form.hy2_obfs_enabled}
                    onChange={(e) => updateField("hy2_obfs_enabled", e.target.checked)}
                  />
                  <Label htmlFor="hy2_obfs_enabled" className="cursor-pointer">启用 salamander 混淆</Label>
                </div>
                {form.hy2_obfs_enabled && (
                  <div className="space-y-2">
                    <Label>混淆密码（obfs-password）</Label>
                    <div className="flex gap-2">
                      <Input
                        value={form.hy2_obfs_password}
                        onChange={(e) => updateField("hy2_obfs_password", e.target.value)}
                        placeholder="混淆密码"
                        className="flex-1 font-mono text-xs"
                      />
                      <Button type="button" variant="outline" size="sm" onClick={() => updateField("hy2_obfs_password", generateBase64Key(16))}>
                        生成
                      </Button>
                    </div>
                  </div>
                )}
              </div>
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

// ── Delete Confirmation Dialog ───────────────────────────────────

interface DeleteDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  inbound: Inbound | null;
  onConfirm: () => Promise<void>;
  deleting: boolean;
}

function DeleteDialog({ open, onOpenChange, inbound, onConfirm, deleting }: DeleteDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>确认删除</DialogTitle>
          <DialogDescription>
            确定要删除入站{" "}
            <span className="font-semibold text-[hsl(var(--foreground))]">
              {inbound ? `${inbound.protocol}:${inbound.port}` : ""}
            </span>{" "}
            吗？此操作不可撤销。
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

// ── Icons ────────────────────────────────────────────────────────

function IconWand({ className }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <path d="M15 4V2" /><path d="M15 16v-2" /><path d="M8 9h2" /><path d="M20 9h2" />
      <path d="M17.8 11.8 19 13" /><path d="M15 9h.01" /><path d="M17.8 6.2 19 5" />
      <path d="m3 21 9-9" /><path d="M12.2 6.2 11 5" />
    </svg>
  );
}

// ── CF Domain Picker Icon ────────────────────────────────────────

function IconCloudPick({ className }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <path d="M18 10h-1.26A8 8 0 1 0 9 20h9a5 5 0 0 0 0-10z" />
    </svg>
  );
}

// ── CF Domain Picker Dialog ─────────────────────────────────────

interface CFDomainPickerProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSelect: (domain: string) => void;
  handleAuthError: (err: unknown) => boolean;
  nodeId?: string;
}

function CFDomainPickerDialog({
  open,
  onOpenChange,
  onSelect,
  handleAuthError,
  nodeId,
}: CFDomainPickerProps) {
  const [nodeDomains, setNodeDomains] = useState<NodeDomain[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!open || !nodeId) return;
    setLoading(true);
    nodeDomainApi
      .list()
      .then((res) =>
        setNodeDomains((res.node_domains ?? []).filter((d) => d.node_id === nodeId))
      )
      .catch((err) => {
        if (!handleAuthError(err)) toast.error("加载域名列表失败");
      })
      .finally(() => setLoading(false));
  }, [open, handleAuthError, nodeId]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[80vh] overflow-y-auto sm:max-w-md">
        <DialogHeader>
          <DialogTitle>从 CF 域名选择</DialogTitle>
          <DialogDescription>选择已解析到此节点的域名，自动填充地址。</DialogDescription>
        </DialogHeader>

        {loading ? (
          <div className="flex items-center justify-center py-8">
            <IconLoader className="h-5 w-5 animate-spin text-[hsl(var(--muted-foreground))]" />
          </div>
        ) : nodeDomains.length === 0 ? (
          <p className="py-6 text-center text-sm text-[hsl(var(--muted-foreground))]">
            暂无已解析到此节点的域名。
          </p>
        ) : (
          <div className="space-y-1">
            {nodeDomains.map((nd) => (
              <button
                key={nd.id}
                type="button"
                className="flex w-full items-center justify-between rounded-md border border-[hsl(var(--border))] px-3 py-2 text-left text-sm hover:bg-[hsl(var(--accent))]"
                onClick={() => {
                  onSelect(nd.fqdn);
                  onOpenChange(false);
                }}
              >
                <span className="font-mono">{nd.fqdn}</span>
                <span className="ml-2 shrink-0 text-xs text-[hsl(var(--muted-foreground))]">
                  {nd.content}
                  {nd.proxied && " ☁"}
                </span>
              </button>
            ))}
          </div>
        )}

        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant="outline">取消</Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── Host Form Dialog ─────────────────────────────────────────────


function slugPart(s: string): string {
  return s
    .toLowerCase()
    .replace(/\s+/g, "-")
    .replace(/[^a-z0-9-]/g, "")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "");
}

interface HostFormState {
  address: string;
  port: string;
  sni: string;
  country: string;
  region: string;
  network: string;
  entry: string;
  tags: string;
  relay_node_id: string;
  https_port: string;
}

const EMPTY_HOST_FORM: HostFormState = {
  address: "",
  port: "0",
  sni: "",
  country: "",
  region: "",
  network: "",
  entry: "",
  tags: "",
  relay_node_id: "",
  https_port: "",
};

interface HostFormDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  host: Host | null;
  inboundId: string;
  inboundTag?: string;
  inboundProtocol?: string;
  inboundPort?: number;
  nodeIp?: string;
  nodeId?: string;
  nodes: Node[];
  allHosts?: Host[]; // 用于检测前置节点端口冲突
  nodeIpMap?: Map<string, string>; // 用于自动填入前置节点地址
  outboundName?: string; // 出口名称，用于自动生成 CF 记录 comment
  onSaved: () => void;
  handleAuthError: (err: unknown) => boolean;
}

function HostFormDialog({
  open,
  onOpenChange,
  host,
  inboundId,
  inboundTag,
  inboundProtocol,
  inboundPort,
  nodeIp = "",
  nodeId,
  nodes,
  allHosts = [],
  nodeIpMap,
  outboundName = "",
  onSaved,
  handleAuthError,
}: HostFormDialogProps) {
  const [form, setForm] = useState<HostFormState>(EMPTY_HOST_FORM);
  const [submitting, setSubmitting] = useState(false);
  const [showRelay, setShowRelay] = useState(false);
  const [autoGen, setAutoGen] = useState<{ zone: CFDomain; zones: CFDomain[]; subdomain: string; remark: string } | null>(null);
  const [autoGenApplying, setAutoGenApplying] = useState(false);
  const [geoLooking, setGeoLooking] = useState(false);
  const [pickerOpen, setPickerOpen] = useState(false);
  // 前置节点实时 sniproxy 启用状态：null=未查询/已清除，true=enabled，false=未启用
  const [relaySniproxyEnabled, setRelaySniproxyEnabled] = useState<boolean | null>(null);

  const isEdit = !!host;

  useEffect(() => {
    if (!open) return;
    if (host) {
      setForm({
        address: host.address,
        port: String(host.port),
        sni: host.sni || "",
        country: host.country || "",
        region: host.region || "",
        network: host.network || "",
        entry: host.entry || "",
        tags: host.tags || "",
        relay_node_id: host.relay_node_id || "",
        https_port: host.https_port ? String(host.https_port) : "",
      });
      setShowRelay(!!host.relay_node_id);
    } else {
      const defaultPort = (inboundProtocol === "anytls" || inboundProtocol === "trojan") ? "443" : String(inboundPort ?? "");
      setForm({ ...EMPTY_HOST_FORM, address: nodeIp || "", port: defaultPort });
      setShowRelay(false);
    }
  }, [open, host, inboundProtocol, inboundPort, nodeIp]);

  // 前置节点变化时实时查询 NodeGate (sniproxy) 运行状态
  useEffect(() => {
    if (!form.relay_node_id) {
      setRelaySniproxyEnabled(null);
      return;
    }
    let cancelled = false;
    api.get<{ enabled: boolean }>(`/nodes/${form.relay_node_id}/sniproxy/status`)
      .then((s) => { if (!cancelled) setRelaySniproxyEnabled(s.enabled); })
      .catch(() => { if (!cancelled) setRelaySniproxyEnabled(null); });
    return () => { cancelled = true; };
  }, [form.relay_node_id]);

  const updateField = <K extends keyof HostFormState>(key: K, value: HostFormState[K]) => {
    setForm((prev) => ({ ...prev, [key]: value }));
  };

  const handleAutoGen = async () => {
    try {
      const res = await cfApi.listDomains();
      const zones = res.domains ?? [];
      const defaultZone = zones[0];
      if (!defaultZone) { toast.error("暂无已管理的 CF 域名"); return; }
      // 优先选与当前地址后缀匹配的 zone（取最长匹配），否则沿用第一个
      const addr = form.address.trim().toLowerCase();
      const zone =
        zones
          .filter((z) => addr === z.zone_name.toLowerCase() || addr.endsWith(`.${z.zone_name.toLowerCase()}`))
          .sort((a, b) => b.zone_name.length - a.zone_name.length)[0] ?? defaultZone;
      const parts: string[] = ["cdn"];
      const country = slugPart(form.country);
      if (country) parts.push(country);
      const region = slugPart(form.region);
      if (region) parts.push(region);
      const network = slugPart(form.network);
      if (network) parts.push(network);
      parts.push(Math.random().toString(16).slice(2, 6));
      const subdomain = parts.join("-");
      const nodeName = nodes.find((n) => n.id === nodeId)?.name ?? "";
      const displayPort = form.port && form.port !== "0" ? form.port : (inboundTag?.split(":")[1] ?? "");
      const remark = [nodeName, inboundProtocol, displayPort, outboundName].filter(Boolean).join(" ");
      setAutoGen({ zone, zones, subdomain, remark });
    } catch (err) {
      if (!handleAuthError(err)) toast.error("获取域名失败");
    }
  };

  const handleAutoGenConfirm = async () => {
    if (!autoGen) return;
    setAutoGenApplying(true);
    try {
      await cfApi.createRecord(autoGen.zone.id, {
        type: "A",
        name: autoGen.subdomain,
        content: nodeIp,
        ttl: 1,
        proxied: false,
        comment: autoGen.remark,
      });
      const fqdn = `${autoGen.subdomain}.${autoGen.zone.zone_name}`;
      updateField("address", fqdn);
      updateField("sni", fqdn);
      toast.success(`已创建 ${fqdn}`);
      setAutoGen(null);
    } catch (err) {
      if (!handleAuthError(err)) toast.error("创建 DNS 记录失败");
    } finally {
      setAutoGenApplying(false);
    }
  };

  const relayValid = !form.relay_node_id || (form.port !== "" && Number(form.port) > 0 && Number(form.port) <= 65535);
  const relayPortConflict = !!form.relay_node_id && form.port !== "" &&
    allHosts.some((h) => h.relay_node_id === form.relay_node_id && h.port === Number(form.port) && h.id !== host?.id);
  const canSubmit = form.address.trim() !== "" && relayValid && !relayPortConflict;

  const handleGeoLookup = async () => {
    if (!nodeIp || geoLooking) return;
    setGeoLooking(true);
    try {
      const info = await api.get<{
        country_code: string; country_name: string; city: string; asn_org: string;
      }>(`/system/geoip/lookup?host=${encodeURIComponent(nodeIp)}`);
      if (info.country_code) {
        updateField("country", info.country_code.toUpperCase());
      }
      updateField("region", info.city || info.country_name || "");
      updateField("network", info.asn_org || "");
    } catch {
      // 静默失败，用户手动填
    } finally {
      setGeoLooking(false);
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canSubmit || submitting) return;
    setSubmitting(true);
    try {
      const body: Record<string, unknown> = {
        inbound_id: inboundId,
        address: form.address.trim(),
        port: Number(form.port) || 0,
        sni: form.sni,
        country: form.country,
        region: form.region,
        network: form.network,
        entry: form.entry,
        tags: form.tags,
      };

      // 显式提交 relay 字段，含清空场景（relay_node_id="" → relay 关闭）
      body.relay_node_id = form.relay_node_id || "";
      body.https_port = (form.relay_node_id && form.https_port) ? Number(form.https_port) : 0;

      if (isEdit) {
        await api.put<Host>(`/hosts/${host.id}`, body);
      } else {
        await api.post<Host>("/hosts", body);
      }
      onOpenChange(false);
      onSaved();
    } catch (err) {
      if (!handleAuthError(err)) {
        toast(err instanceof Error ? err.message : "保存失败", "error");
      }
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <>
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <form onSubmit={handleSubmit}>
          <DialogHeader>
            <DialogTitle>{isEdit ? "编辑连接地址" : "添加连接地址"}</DialogTitle>
            <DialogDescription>
              {isEdit ? "修改连接地址配置。" : "为入站添加新的连接地址。"}
            </DialogDescription>
          </DialogHeader>

          <div className="mt-4 space-y-4">
            {/* ── 两列主体：左=地址/端口，右=节点命名 ──── */}
            <div className="grid grid-cols-2 gap-4 items-start">
              {/* 左列：地址 + 端口 */}
              <div className="space-y-4">
                {/* Address (required) */}
                <div className="space-y-2">
                  <Label>地址 *</Label>
                  <div className="flex items-center gap-2">
                    <Input
                      value={form.address}
                      onChange={(e) => updateField("address", e.target.value)}
                      placeholder="域名或 IP"
                      required
                      className="flex-1"
                    />
                    <Button
                      type="button"
                      variant="outline"
                      size="icon"
                      className="shrink-0"
                      title="从 CF 域名选择"
                      onClick={() => setPickerOpen(true)}
                    >
                      <IconCloudPick className="h-4 w-4" />
                    </Button>
                    <Button
                      type="button"
                      variant="outline"
                      size="icon"
                      className="shrink-0"
                      title="自动生成域名"
                      onClick={handleAutoGen}
                    >
                      <IconWand className="h-4 w-4" />
                    </Button>
                  </div>

                  {/* 自动生成确认区 */}
                  {autoGen && (
                    <div className="rounded-md border border-[hsl(var(--border))] bg-[hsl(var(--muted)/0.4)] p-3 space-y-3">
                      <p className="text-xs text-[hsl(var(--muted-foreground))]">将创建 DNS A 记录</p>
                      <div className="flex items-center gap-2">
                        <Input
                          value={autoGen.subdomain}
                          onChange={(e) =>
                            setAutoGen((prev) => prev ? { ...prev, subdomain: e.target.value } : null)
                          }
                          className="flex-1 font-mono text-sm"
                          placeholder="子域名"
                        />
                        {autoGen.zones.length > 1 ? (
                          <Select
                            value={autoGen.zone.id}
                            onValueChange={(id) =>
                              setAutoGen((prev) => {
                                const zone = prev?.zones.find((z) => z.id === id);
                                return prev && zone ? { ...prev, zone } : prev;
                              })
                            }
                          >
                            <SelectTrigger className="h-9 w-auto shrink-0 font-mono text-sm">
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {autoGen.zones.map((z) => (
                                <SelectItem key={z.id} value={z.id} className="font-mono">
                                  .{z.zone_name}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        ) : (
                          <span className="shrink-0 text-sm text-[hsl(var(--muted-foreground))]">
                            .{autoGen.zone.zone_name}
                          </span>
                        )}
                        <span className="shrink-0 text-xs text-[hsl(var(--muted-foreground))]">
                          → {nodeIp || "?"}
                        </span>
                      </div>
                      <div className="space-y-1">
                        <Label className="text-xs">订阅名（同步到 CF 记录 comment）</Label>
                        <Input
                          value={autoGen.remark}
                          onChange={(e) =>
                            setAutoGen((prev) => prev ? { ...prev, remark: e.target.value } : null)
                          }
                          placeholder="订阅显示名 / CF comment"
                        />
                      </div>
                      <div className="flex justify-end gap-2">
                        <Button type="button" variant="outline" size="sm" onClick={() => setAutoGen(null)}>
                          取消
                        </Button>
                        <Button
                          type="button"
                          size="sm"
                          disabled={autoGenApplying || !autoGen.subdomain.trim() || !nodeIp}
                          onClick={handleAutoGenConfirm}
                        >
                          {autoGenApplying ? "创建中…" : "确认"}
                        </Button>
                      </div>
                    </div>
                  )}
                </div>

                {/* Port */}
                <div className="space-y-2">
                  <Label>端口</Label>
                  <Input
                    type="number"
                    min={1}
                    max={65535}
                    value={form.port}
                    onChange={(e) => updateField("port", e.target.value)}
                    placeholder="端口"
                  />
                </div>
              </div>

              {/* 右列：节点命名 */}
              <div className="space-y-3 rounded-md border border-[hsl(var(--border))] p-3">
                <div className="flex items-center justify-between">
                  <p className="text-xs font-medium text-[hsl(var(--muted-foreground))]">
                    节点命名（留空则回退到订阅名/标签）
                  </p>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="h-6 px-2 text-xs"
                    disabled={!nodeIp || geoLooking}
                    onClick={handleGeoLookup}
                    title="根据连接地址自动填入国家、地区、网络（需在设置中配置 MaxMind License Key）"
                  >
                    {geoLooking ? "查询中…" : "IP 自动填入"}
                  </Button>
                </div>
                <div className="grid grid-cols-2 gap-2">
                  <div className="space-y-1">
                    <Label className="text-xs">国家</Label>
                    <Input
                      value={form.country}
                      onChange={(e) => updateField("country", e.target.value)}
                      placeholder="HK"
                      className="h-8 text-sm"
                    />
                  </div>
                  <div className="space-y-1">
                    <Label className="text-xs">地区</Label>
                    <Input
                      value={form.region}
                      onChange={(e) => updateField("region", e.target.value)}
                      placeholder="香港"
                      className="h-8 text-sm"
                    />
                  </div>
                  <div className="space-y-1">
                    <Label className="text-xs">网络</Label>
                    <Input
                      value={form.network}
                      onChange={(e) => updateField("network", e.target.value)}
                      placeholder="GIA"
                      className="h-8 text-sm"
                    />
                  </div>
                  <div className="space-y-1">
                    <Label className="text-xs">入口</Label>
                    <Input
                      value={form.entry}
                      onChange={(e) => updateField("entry", e.target.value)}
                      placeholder="深圳（选填）"
                      className="h-8 text-sm"
                    />
                  </div>
                </div>
                <div className="space-y-1">
                  <Label className="text-xs">业务标签</Label>
                  <Input
                    value={form.tags}
                    onChange={(e) => updateField("tags", e.target.value)}
                    placeholder="NF·GPT（选填，多个用 · 分隔）"
                    className="h-8 text-sm"
                  />
                </div>
                <p className="text-xs text-[hsl(var(--muted-foreground))]">
                  编号自动生成，倍率来自入站配置，均无需手动填写。
                </p>
              </div>
            </div>

            {/* ── 前置中转 toggle ───────────────────────────── */}
            <button
              type="button"
              className="flex items-center gap-1 text-sm font-medium text-[hsl(var(--primary))] hover:underline"
              onClick={() => setShowRelay((v) => !v)}
            >
              {showRelay ? "▾ 前置中转" : "▸ 前置中转"}
            </button>

            {showRelay && (() => {
              const nodeGateDisabled = form.relay_node_id && relaySniproxyEnabled === false;
              const takenPorts = new Set(
                allHosts
                  .filter((h) => h.relay_node_id === form.relay_node_id && h.id !== host?.id)
                  .map((h) => h.port)
              );
              const portConflict =
                form.relay_node_id !== "" && form.port !== "" &&
                takenPorts.has(Number(form.port));

              return (
                <div className="rounded-lg border border-[hsl(var(--border))] p-4 space-y-3">
                  <div className="space-y-1">
                    <Label>前置节点（可选）</Label>
                    <Select
                      value={form.relay_node_id || "__none__"}
                      onValueChange={(v) => {
                        const id = v === "__none__" ? "" : v;
                        updateField("relay_node_id", id);
                        if (id && nodeIpMap) {
                          const ip = nodeIpMap.get(id);
                          if (ip) updateField("address", ip);
                        }
                      }}
                    >
                      <SelectTrigger>
                        <SelectValue placeholder="不使用前置节点" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="__none__">不使用前置节点</SelectItem>
                        {nodes.map((n) => (
                          <SelectItem key={n.id} value={n.id}>{n.name}</SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    {nodeGateDisabled && (
                      <p className="text-xs text-[hsl(var(--muted-foreground))]">
                        ℹ 该节点 NodeGate 暂无路由（未监听）。保存后将自动下发并启用；如仍未生效，请到 NodeGate 页面查看节点错误。
                      </p>
                    )}
                  </div>

                  {form.relay_node_id && (
                    <>
                      {portConflict && (
                        <p className="text-xs text-[hsl(var(--destructive))]">
                          该端口已被同一前置节点的其他连接地址占用
                        </p>
                      )}
                      <div className="grid gap-3 grid-cols-2">
                        <div className="space-y-1">
                          <Label>NodeGate HTTPS 端口</Label>
                          <Input
                            type="number"
                            min={0}
                            max={65535}
                            value={form.https_port}
                            onChange={(e) => updateField("https_port", e.target.value)}
                            placeholder="443"
                            className="font-mono text-xs"
                          />
                          <p className="text-xs text-[hsl(var(--muted-foreground))]">
                            落地节点 NodeGate 监听端口，0 = 节点默认
                          </p>
                        </div>
                        <div className="space-y-1">
                          <Label>SNI / 落地域名</Label>
                          <Input
                            value={form.sni}
                            onChange={(e) => updateField("sni", e.target.value)}
                            placeholder="落地节点的域名"
                            className="font-mono text-xs"
                          />
                          <p className="text-xs text-[hsl(var(--muted-foreground))]">
                            客户端 TLS SNI，同时用于落地节点 NodeGate 路由和证书申请
                          </p>
                        </div>
                      </div>
                    </>
                  )}
                </div>
              );
            })()}
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

    {/* ── CF 域名选择器 ──────────────────────────────── */}
    <CFDomainPickerDialog
      open={pickerOpen}
      onOpenChange={setPickerOpen}
      onSelect={(domain) => {
        updateField("address", domain);
        updateField("sni", domain);
      }}
      handleAuthError={handleAuthError}
      nodeId={nodeId}
    />
    </>
  );
}

// ── Hosts Dialog ─────────────────────────────────────────────────

interface HostsDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  inbound: Inbound | null;
  nodeIp?: string;
  nodeId?: string;
  nodes: Node[];
  allHosts?: Host[];
  nodeIpMap?: Map<string, string>;
  outboundName?: string;
  handleAuthError: (err: unknown) => boolean;
  onChanged?: () => void;
}

function HostsDialog({ open, onOpenChange, inbound, nodeIp = "", nodeId, nodes, allHosts = [], nodeIpMap, outboundName = "", handleAuthError, onChanged }: HostsDialogProps) {
  const [hosts, setHosts] = useState<Host[]>([]);
  const [loading, setLoading] = useState(false);
  const [editingHost, setEditingHost] = useState<Host | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [deleting, setDeleting] = useState<string | null>(null);

  const fetchHosts = useCallback(async () => {
    if (!inbound) return;
    setLoading(true);
    try {
      const res = await api.get<HostsResponse>(`/hosts?inbound_id=${inbound.id}`);
      setHosts(res.hosts ?? []);
    } catch (err) {
      if (!handleAuthError(err)) {
        // 静默失败
      }
    } finally {
      setLoading(false);
    }
  }, [inbound, handleAuthError]);

  useEffect(() => {
    if (open && inbound) {
      fetchHosts();
    }
    if (!open) {
      setHosts([]);
      setEditingHost(null);
      setFormOpen(false);
      setDeleting(null);
    }
  }, [open, inbound, fetchHosts]);

  const handleDeleteHost = async (id: string) => {
    setDeleting(id);
    try {
      await api.del(`/hosts/${id}`);
      await fetchHosts();
      onChanged?.();
    } catch (err) {
      if (!handleAuthError(err)) {
        // keep going
      }
    } finally {
      setDeleting(null);
    }
  };

  const openAddHost = () => {
    setEditingHost(null);
    setFormOpen(true);
  };

  const openEditHost = (h: Host) => {
    setEditingHost(h);
    setFormOpen(true);
  };

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>
              连接地址 — {inbound ? `${inbound.protocol}:${inbound.port}` : ""}
            </DialogTitle>
            <DialogDescription>
              每条连接地址对应用户订阅中的一个节点记录。
            </DialogDescription>
          </DialogHeader>

          <div className="mt-4">
            <div className="mb-4 flex items-center justify-between">
              <p className="text-sm text-[hsl(var(--muted-foreground))]">
                共 {hosts.length} 条记录
              </p>
              <Button size="sm" onClick={openAddHost}>
                <IconPlus className="mr-1.5 h-3.5 w-3.5" />
                添加连接地址
              </Button>
            </div>

            {loading ? (
              <div className="flex justify-center py-8">
                <IconLoader className="h-5 w-5 animate-spin text-[hsl(var(--muted-foreground))]" />
              </div>
            ) : hosts.length === 0 ? (
              <div className="py-8 text-center">
                <p className="mb-1 text-sm font-medium text-[hsl(var(--foreground))]">暂无连接地址</p>
                <p className="text-xs text-[hsl(var(--muted-foreground))]">
                  点击上方按钮添加第一个连接地址。
                </p>
              </div>
            ) : (
              <div className="rounded-md border border-[hsl(var(--border))]">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead className="px-3">订阅名</TableHead>
                      <TableHead className="px-3">地址</TableHead>
                      <TableHead className="px-3">端口</TableHead>
                      <TableHead className="px-3 text-right">操作</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {hosts.map((h) => (
                      <TableRow key={h.id}>
                        <TableCell className="px-3 text-[hsl(var(--foreground))]">
                          {hostSubName(h, inbound?.traffic_rate) || "—"}
                        </TableCell>
                        <TableCell className="px-3 font-mono text-sm text-[hsl(var(--muted-foreground))]">
                          {h.address}
                        </TableCell>
                        <TableCell className="px-3 font-mono text-sm text-[hsl(var(--muted-foreground))]">
                          {h.port}
                        </TableCell>
                        <TableCell className="px-3 text-right">
                          <div className="flex justify-end gap-1">
                            <Button
                              variant="ghost"
                              size="sm"
                              onClick={() => openEditHost(h)}
                              className="h-8 w-8 p-0"
                              title="编辑"
                            >
                              <IconEdit className="h-4 w-4" />
                            </Button>
                            <Button
                              variant="ghost"
                              size="sm"
                              onClick={() => handleDeleteHost(h.id)}
                              disabled={deleting === h.id}
                              className="h-8 w-8 p-0 text-[hsl(var(--destructive))] hover:text-[hsl(var(--destructive))]"
                              title="删除"
                            >
                              {deleting === h.id ? (
                                <IconLoader className="h-4 w-4 animate-spin" />
                              ) : (
                                <IconTrash className="h-4 w-4" />
                              )}
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </div>

          <DialogFooter className="mt-4">
            <DialogClose asChild>
              <Button variant="outline">关闭</Button>
            </DialogClose>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {inbound && (
        <HostFormDialog
          open={formOpen}
          onOpenChange={(o) => {
            setFormOpen(o);
            if (!o) setEditingHost(null);
          }}
          host={editingHost}
          inboundId={inbound.id}
          inboundTag={`${inbound.protocol}:${inbound.port}`}
          inboundProtocol={inbound.protocol}
          inboundPort={inbound.port}
          nodeIp={nodeIp}
          nodeId={nodeId}
          nodes={nodes}
          allHosts={allHosts}
          nodeIpMap={nodeIpMap}
          outboundName={outboundName}
          onSaved={() => { fetchHosts(); onChanged?.(); }}
          handleAuthError={handleAuthError}
        />
      )}
    </>
  );
}

// ── User List Dialog（只读，展示当前入站关联的用户）────────────────────

interface UserListDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  inbound: Inbound | null;
  handleAuthError: (err: unknown) => boolean;
}

function UserListDialog({ open, onOpenChange, inbound, handleAuthError }: UserListDialogProps) {
  const [users, setUsers] = useState<User[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!open || !inbound) return;
    setLoading(true);
    Promise.all([
      api.get<{ user_ids: string[] }>(`/inbounds/${inbound.id}/users`),
      api.get<UsersResponse>("/users"),
    ])
      .then(([assignedRes, usersRes]) => {
        const idSet = new Set(assignedRes.user_ids ?? []);
        setUsers((usersRes.users ?? []).filter((u) => idSet.has(u.id)));
      })
      .catch((err) => handleAuthError(err))
      .finally(() => setLoading(false));
  }, [open, inbound, handleAuthError]);

  useEffect(() => {
    if (!open) setUsers([]);
  }, [open]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>关联用户</DialogTitle>
          <DialogDescription>
            入站 <span className="font-medium text-[hsl(var(--foreground))]">{inbound ? `${inbound.protocol}:${inbound.port}` : ""}</span> 当前关联的用户
          </DialogDescription>
        </DialogHeader>
        <ScrollArea className="max-h-[60vh]">
          {loading ? (
            <p className="py-6 text-center text-sm text-[hsl(var(--muted-foreground))]">加载中…</p>
          ) : users.length === 0 ? (
            <p className="py-6 text-center text-sm text-[hsl(var(--muted-foreground))]">暂无关联用户</p>
          ) : (
            <div className="divide-y divide-[hsl(var(--border))]">
              {users.map((u) => (
                <div key={u.id} className="flex items-center justify-between py-2.5 px-1">
                  <span className="text-sm font-medium">{u.username}</span>
                  <Badge variant={u.status === "active" ? "default" : "secondary"} className="text-xs">
                    {u.status}
                  </Badge>
                </div>
              ))}
            </div>
          )}
        </ScrollArea>
      </DialogContent>
    </Dialog>
  );
}

// ── User Allocation Dialog ────────────────────────────────────────

interface UserAllocDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  inbound: Inbound | null;
  handleAuthError: (err: unknown) => boolean;
  onSaved?: () => void;
}

function UserAllocDialog({ open, onOpenChange, inbound, handleAuthError, onSaved }: UserAllocDialogProps) {
  const [allUsers, setAllUsers] = useState<User[]>([]);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [searchFilter, setSearchFilter] = useState("");
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);

  // 打开时拉取用户列表和已分配 ID
  useEffect(() => {
    if (!open || !inbound) return;
    setLoading(true);
    setSearchFilter("");
    Promise.all([
      api.get<UsersResponse>("/users"),
      api.get<{ user_ids: string[] }>(`/inbounds/${inbound.id}/users`),
    ])
      .then(([usersRes, assignedRes]) => {
        setAllUsers(usersRes.users ?? []);
        setSelectedIds(new Set(assignedRes.user_ids ?? []));
      })
      .catch((err) => {
        if (!handleAuthError(err)) {
          // 静默失败
        }
      })
      .finally(() => setLoading(false));
  }, [open, inbound, handleAuthError]);

  // 关闭时重置
  useEffect(() => {
    if (!open) {
      setAllUsers([]);
      setSelectedIds(new Set());
      setSearchFilter("");
    }
  }, [open]);

  const filteredUsers = useMemo(() => {
    if (!searchFilter.trim()) return allUsers;
    const q = searchFilter.toLowerCase();
    return allUsers.filter((u) => u.username.toLowerCase().includes(q));
  }, [allUsers, searchFilter]);

  const toggleUser = (id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const selectAll = () => {
    setSelectedIds(new Set(allUsers.map((u) => u.id)));
  };

  const selectNone = () => {
    setSelectedIds(new Set());
  };

  const handleSave = async () => {
    if (!inbound || saving) return;
    setSaving(true);
    try {
      await api.put(`/inbounds/${inbound.id}/users`, {
        user_ids: Array.from(selectedIds),
      });
      onSaved?.();
      onOpenChange(false);
    } catch (err) {
      if (!handleAuthError(err)) {
        toast(err instanceof Error ? err.message : "分配失败", "error");
      }
    } finally {
      setSaving(false);
    }
  };

  const statusVariant = (status: string): "default" | "secondary" | "outline" => {
    switch (status) {
      case "active":
        return "default";
      case "disabled":
      case "limited":
      case "expired":
        return "secondary";
      default:
        return "outline";
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>分配用户 — {inbound ? `${inbound.protocol}:${inbound.port}` : ""}</DialogTitle>
          <DialogDescription>
            为入站 <span className="font-semibold">{inbound ? `${inbound.protocol}:${inbound.port}` : ""}</span> 分配可使用的用户。
          </DialogDescription>
        </DialogHeader>

        {loading ? (
          <div className="flex justify-center py-8">
            <IconLoader className="h-5 w-5 animate-spin text-[hsl(var(--muted-foreground))]" />
          </div>
        ) : (
          <div className="mt-4 space-y-3">
            {/* 搜索 */}
            <Input
              placeholder="搜索用户名…"
              value={searchFilter}
              onChange={(e) => setSearchFilter(e.target.value)}
            />

            {/* 工具栏：全选 / 全不选 + 计数 */}
            <div className="flex items-center justify-between">
              <div className="flex gap-2">
                <Button type="button" variant="outline" size="sm" onClick={selectAll}>
                  全选
                </Button>
                <Button type="button" variant="outline" size="sm" onClick={selectNone}>
                  全不选
                </Button>
              </div>
              <span className="text-sm text-[hsl(var(--muted-foreground))]">
                已选 {selectedIds.size}/{allUsers.length} 用户
              </span>
            </div>

            {/* 用户列表 */}
            <ScrollArea className="max-h-[50vh] rounded-md border border-[hsl(var(--border))] p-2">
              {filteredUsers.length === 0 ? (
                <p className="py-4 text-center text-sm text-[hsl(var(--muted-foreground))]">
                  {allUsers.length === 0 ? "暂无用户" : "无匹配用户"}
                </p>
              ) : (
                filteredUsers.map((u) => (
                  <label
                    key={u.id}
                    className="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 hover:bg-[hsl(var(--muted))]"
                  >
                    <Checkbox
                      checked={selectedIds.has(u.id)}
                      onCheckedChange={() => toggleUser(u.id)}
                    />
                    <span className="flex-1 text-sm text-[hsl(var(--foreground))]">{u.username}</span>
                    <Badge variant={statusVariant(u.status)} className="text-[10px]">
                      {u.status}
                    </Badge>
                  </label>
                ))
              )}
            </ScrollArea>
          </div>
        )}

        <DialogFooter className="mt-4">
          <DialogClose asChild>
            <Button type="button" variant="outline" disabled={saving}>
              取消
            </Button>
          </DialogClose>
          <Button onClick={handleSave} disabled={loading || saving}>
            {saving ? "保存中…" : "保存"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── Main Page ────────────────────────────────────────────────────

export default function InboundsPage() {

  const [inbounds, setInbounds] = useState<Inbound[]>([]);
  const [userCounts, setUserCounts] = useState<Record<string, number>>({});
  const [nodes, setNodes] = useState<Node[]>([]);
  const [outbounds, setOutbounds] = useState<Outbound[]>([]);
  const [ssOutboundOptions, setSSOutboundOptions] = useState<SSOutboundOption[]>([]);
  const [allHosts, setAllHosts] = useState<Host[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState("");

  // Form dialog
  const [formOpen, setFormOpen] = useState(false);
  const [editingInbound, setEditingInbound] = useState<Inbound | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // Delete dialog
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deletingInbound, setDeletingInbound] = useState<Inbound | null>(null);
  const [deleting, setDeleting] = useState(false);


  // Hosts dialog
  const [hostsOpen, setHostsOpen] = useState(false);
  const [hostsInbound, setHostsInbound] = useState<Inbound | null>(null);

  // User list dialog（只读）
  const [userListOpen, setUserListOpen] = useState(false);
  const [userListInbound, setUserListInbound] = useState<Inbound | null>(null);

  // User allocation dialog
  const [userAllocOpen, setUserAllocOpen] = useState(false);
  const [userAllocInbound, setUserAllocInbound] = useState<Inbound | null>(null);

  // ── Auth error handler ───────────────────────────────────────
  const handleAuthError = useAuthErrorHandler();

  // ── Node name lookup ─────────────────────────────────────────
  const nodeNameMap = useMemo(() => {
    const map = new Map<string, string>();
    nodes.forEach((n) => map.set(n.id, n.name));
    return map;
  }, [nodes]);

  const nodeIpMap = useMemo(() => {
    const map = new Map<string, string>();
    nodes.forEach((n) => {
      if (n.ip_override) {
        map.set(n.id, n.ip_override);
      } else {
        try {
          map.set(n.id, new URL(n.base_url).hostname);
        } catch {
          // ignore
        }
      }
    });
    return map;
  }, [nodes]);

  // ── Filtered inbounds ───────────────────────────────────────
  const filteredInbounds = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return inbounds;
    return inbounds.filter((ib) =>
      ib.protocol.toLowerCase().includes(q) ||
      (nodeNameMap.get(ib.node_id) ?? ib.node_id).toLowerCase().includes(q) ||
      String(ib.port).includes(q)
    );
  }, [inbounds, search, nodeNameMap]);

  // ── 订阅名预览（使用公共函数 hostSubName）────────────────
  const previewName = (host: Host, trafficRate: number): string =>
    hostSubName(host, trafficRate);

  // ── Outbound name lookup ────────────────────────────────────
  const outboundNameMap = useMemo(() => {
    const map = new Map<string, string>();
    outbounds.forEach((ob) => map.set(ob.id, `${ob.name} (${ob.protocol})`));
    ssOutboundOptions.forEach((opt) => map.set(opt.id, opt.label));
    return map;
  }, [outbounds, ssOutboundOptions]);

  // ── Fetch data ───────────────────────────────────────────────
  const fetchData = useCallback(() => {
    setLoading(true);
    setError(null);
    Promise.all([
      api.get<InboundsResponse>("/inbounds"),
      api.get<NodesResponse>("/nodes"),
      api.get<OutboundsResponse>("/outbounds"),
      api.get<SSOutboundOptionsResponse>("/inbounds/ss-outbound-options"),
      api.get<HostsResponse>("/hosts").catch(() => ({ hosts: [] } as HostsResponse)),
    ])
      .then(([ibRes, nodesRes, obRes, ssRes, hostsRes]) => {
        setInbounds(ibRes.inbounds ?? []);
        setUserCounts(ibRes.user_counts ?? {});
        setNodes(nodesRes.nodes ?? []);
        setOutbounds(obRes.outbounds ?? []);
        setSSOutboundOptions(ssRes.options ?? []);
        setAllHosts(hostsRes.hosts ?? []);
      })
      .catch((err) => {
        if (!handleAuthError(err)) {
          setError(err instanceof Error ? err.message : "加载失败");
        }
      })
      .finally(() => setLoading(false));
  }, [handleAuthError]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  // ── Create / Update ──────────────────────────────────────────
  const handleSubmit = async (data: CreateInboundRequest): Promise<Inbound> => {
    setSubmitting(true);
    try {
      let result: Inbound;
      if (editingInbound) {
        result = await api.put<Inbound>(`/inbounds/${editingInbound.id}`, data);
      } else {
        result = await api.post<Inbound>("/inbounds", data);
      }
      return result;
    } catch (err) {
      if (!handleAuthError(err)) {
        toast(err instanceof Error ? err.message : "保存失败", "error");
      }
      throw err;
    } finally {
      setSubmitting(false);
    }
  };

  // ── Delete ───────────────────────────────────────────────────
  const handleDelete = async () => {
    if (!deletingInbound) return;
    setDeleting(true);
    try {
      await api.del(`/inbounds/${deletingInbound.id}`);
      setDeleteOpen(false);
      setDeletingInbound(null);
      fetchData();
    } catch (err) {
      if (!handleAuthError(err)) {
        toast(err instanceof Error ? err.message : "删除失败", "error");
      }
    } finally {
      setDeleting(false);
    }
  };

  // ── Dialog openers ───────────────────────────────────────────
  const openCreate = () => {
    setEditingInbound(null);
    setFormOpen(true);
  };

  const openEdit = (ib: Inbound) => {
    setEditingInbound(ib);
    setFormOpen(true);
  };

  const openDelete = (ib: Inbound) => {
    setDeletingInbound(ib);
    setDeleteOpen(true);
  };

  const openHosts = (ib: Inbound) => {
    setHostsInbound(ib);
    setHostsOpen(true);
  };

  const openUserList = (ib: Inbound) => {
    setUserListInbound(ib);
    setUserListOpen(true);
  };

  const openUserAlloc = (ib: Inbound) => {
    setUserAllocInbound(ib);
    setUserAllocOpen(true);
  };

  // ── Error state (full-page) ──────────────────────────────────
  if (error && !inbounds.length) {
    return (
      <div className="flex h-full items-center justify-center p-8">
        <Card className="w-full max-w-md">
          <CardContent className="pt-6 text-center">
            <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-[hsl(var(--destructive))]/10 text-[hsl(var(--destructive))]">
              <IconAlert className="h-6 w-6" />
            </div>
            <p className="mb-1 font-semibold text-[hsl(var(--foreground))]">加载失败</p>
            <p className="mb-4 text-sm text-[hsl(var(--muted-foreground))]">{error}</p>
            <Button onClick={fetchData} variant="outline">
              <IconRefresh className="mr-2 h-4 w-4" />
              重试
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="flex h-full flex-col p-4 sm:p-6 lg:p-8">
      {/* ── Header ──────────────────────────────────────────────── */}
      <div className="mb-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-bold text-[hsl(var(--foreground))]">入站</h1>
          <p className="mt-1 text-sm text-[hsl(var(--muted-foreground))]">
            管理入站连接及协议配置。
          </p>
        </div>
        <Button onClick={openCreate} className="self-start sm:self-auto">
          <IconPlus className="mr-2 h-4 w-4" />
          添加入站
        </Button>
      </div>

      {/* ── Search ──────────────────────────────────────────────── */}
      <div className="mb-3">
        <Input
          placeholder="搜索 tag、协议、节点、端口…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="max-w-sm"
        />
      </div>

      {/* ── Empty state ─────────────────────────────────────────── */}
      {!loading && inbounds.length === 0 && (
        <Card>
          <CardContent className="py-16 text-center">
            <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-[hsl(var(--muted))]">
              <IconInbox className="h-6 w-6 text-[hsl(var(--muted-foreground))]" />
            </div>
            <p className="mb-1 font-semibold text-[hsl(var(--foreground))]">暂无入站</p>
            <p className="mb-4 text-sm text-[hsl(var(--muted-foreground))]">
              添加第一个入站开始使用。
            </p>
            <Button onClick={openCreate}>
              <IconPlus className="mr-2 h-4 w-4" />
              添加入站
            </Button>
          </CardContent>
        </Card>
      )}

      {/* ── Table ───────────────────────────────────────────────── */}
      {(loading || inbounds.length > 0) && (
        <Card className="flex min-h-0 flex-1 flex-col overflow-hidden">
          <Table containerClassName="flex-1 overflow-auto">
            <TableHeader className="sticky top-0 z-10 bg-[hsl(var(--card))]">
              <TableRow>
                <TableHead className="px-4 w-px whitespace-nowrap">协议</TableHead>
                <TableHead className="px-4 w-px whitespace-nowrap text-right">用户</TableHead>
                <TableHead className="px-4">节点</TableHead>
                <TableHead className="hidden px-4 w-[120px] max-w-[120px] lg:table-cell">出口</TableHead>
                <TableHead className="hidden px-4 lg:table-cell">订阅名</TableHead>
                <TableHead className="sticky right-0 bg-[hsl(var(--card))] px-4 w-px whitespace-nowrap text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {loading ? (
                <SkeletonRows />
              ) : filteredInbounds.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={6} className="h-24 text-center text-[hsl(var(--muted-foreground))]">
                    没有匹配的入站
                  </TableCell>
                </TableRow>
              ) : (
                filteredInbounds.map((ib) => (
                  <TableRow key={ib.id}>
                    <TableCell className="px-4 whitespace-nowrap">
                      <div className="flex items-center gap-1.5">
                        <Badge variant={PROTOCOL_BADGE_VARIANT[ib.protocol] ?? "outline"}>
                          {ib.protocol}
                        </Badge>
                        {ib.traffic_rate && ib.traffic_rate !== 1 && (
                          <span className="text-xs font-mono text-[hsl(var(--muted-foreground))]">×{ib.traffic_rate}</span>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="px-4 text-right">
                      <button
                        type="button"
                        className="cursor-pointer text-xs text-[hsl(var(--muted-foreground))] hover:text-[hsl(var(--foreground))] hover:underline transition-colors"
                        onClick={() => openUserList(ib)}
                      >
                        {userCounts[ib.id] ?? 0}
                      </button>
                    </TableCell>
                    <TableCell className="px-4">
                      <TooltipProvider delayDuration={200}>
                        <Tooltip>
                          <TooltipTrigger asChild>
                            <span className="cursor-default text-sm text-[hsl(var(--foreground))]">
                              {nodeNameMap.get(ib.node_id) || ib.node_id}
                            </span>
                          </TooltipTrigger>
                          <TooltipContent side="top">
                            <span className="font-mono text-xs select-all">
                              {nodeIpMap.get(ib.node_id) ?? "无 IP 信息"}
                            </span>
                          </TooltipContent>
                        </Tooltip>
                      </TooltipProvider>
                      <span className="ml-1.5 font-mono text-xs text-[hsl(var(--muted-foreground))]">
                        :{ib.port}
                      </span>
                    </TableCell>
                    <TableCell className="hidden px-4 w-[120px] max-w-[120px] lg:table-cell">
                      {(() => {
                        const name = ib.outbound_id ? (outboundNameMap.get(ib.outbound_id) ?? "direct") : "direct";
                        return (
                          <TooltipProvider delayDuration={300}>
                            <Tooltip>
                              <TooltipTrigger asChild>
                                <div className="max-w-[120px] truncate text-sm text-[hsl(var(--muted-foreground))] cursor-default">
                                  {name}
                                </div>
                              </TooltipTrigger>
                              <TooltipContent side="top">{name}</TooltipContent>
                            </Tooltip>
                          </TooltipProvider>
                        );
                      })()}
                    </TableCell>
                    {/* ── 订阅名预览 ──────────────────────────── */}
                    <TableCell className="hidden px-4 lg:table-cell">
                      {(() => {
                        const hosts = allHosts.filter((h) => h.inbound_id === ib.id);
                        if (hosts.length === 0) {
                          return <span className="text-xs text-[hsl(var(--muted-foreground))]">无连接地址</span>;
                        }
                        const first = hosts[0]!;
                        const name = previewName(first, ib.traffic_rate);
                        const allNames = hosts.map((h) => previewName(h, ib.traffic_rate) || "(未填命名字段)");
                        return (
                          <div className="flex items-center gap-1.5 min-w-0">
                            <span className={`truncate text-sm ${name ? "text-[hsl(var(--foreground))]" : "text-[hsl(var(--muted-foreground))] italic"}`}>
                              {name || "(未填命名字段)"}
                            </span>
                            {hosts.length > 1 && (
                              <TooltipProvider delayDuration={200}>
                                <Tooltip>
                                  <TooltipTrigger asChild>
                                    <span className="shrink-0 rounded-full bg-[hsl(var(--muted))] px-1.5 py-0.5 text-[10px] text-[hsl(var(--muted-foreground))] cursor-default">
                                      +{hosts.length - 1}
                                    </span>
                                  </TooltipTrigger>
                                  <TooltipContent side="top" className="max-w-xs">
                                    <ul className="space-y-0.5">
                                      {allNames.map((n, i) => (
                                        <li key={i} className="text-xs">{n}</li>
                                      ))}
                                    </ul>
                                  </TooltipContent>
                                </Tooltip>
                              </TooltipProvider>
                            )}
                          </div>
                        );
                      })()}
                    </TableCell>
                    <TableCell className="sticky right-0 bg-[hsl(var(--card))] px-4 whitespace-nowrap text-right">
                      <div className="flex justify-end">
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button variant="ghost" size="sm" className="h-8 w-8 p-0">
                              <span className="sr-only">更多操作</span>
                              <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="h-4 w-4"><circle cx="12" cy="5" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="12" cy="19" r="1"/></svg>
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem onClick={() => openHosts(ib)}>连接地址</DropdownMenuItem>
                            <DropdownMenuItem onClick={() => openUserAlloc(ib)}>分配用户</DropdownMenuItem>
                            <DropdownMenuItem onClick={() => openEdit(ib)}>编辑</DropdownMenuItem>
                            <DropdownMenuSeparator />
                            <DropdownMenuItem
                              onClick={() => openDelete(ib)}
                              className="text-[hsl(var(--destructive))]"
                            >
                              删除
                            </DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </div>
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </Card>

      )}

      {/* ── Create / Edit dialog ────────────────────────────────── */}
      <InboundFormDialog
        open={formOpen}
        onOpenChange={(open) => {
          setFormOpen(open);
          if (!open) {
            setEditingInbound(null);
            fetchData();
          }
        }}
        inbound={editingInbound}
        nodes={nodes}
        inbounds={inbounds}
        outbounds={outbounds}
        ssOutboundOptions={ssOutboundOptions}
        onSubmit={handleSubmit}
        submitting={submitting}
        handleAuthError={handleAuthError}
      />

      {/* ── Delete confirmation dialog ──────────────────────────── */}
      <DeleteDialog
        open={deleteOpen}
        onOpenChange={(open) => {
          setDeleteOpen(open);
          if (!open) setDeletingInbound(null);
        }}
        inbound={deletingInbound}
        onConfirm={handleDelete}
        deleting={deleting}
      />

      {/* ── Hosts dialog ────────────────────────────────────────── */}
      <HostsDialog
        open={hostsOpen}
        onOpenChange={(open) => {
          setHostsOpen(open);
          if (!open) setHostsInbound(null);
        }}
        inbound={hostsInbound}
        nodeIp={hostsInbound ? (nodeIpMap.get(hostsInbound.node_id) ?? "") : ""}
        nodeId={hostsInbound?.node_id}
        nodes={nodes}
        allHosts={allHosts}
        nodeIpMap={nodeIpMap}
        outboundName={hostsInbound?.outbound_id ? (outboundNameMap.get(hostsInbound.outbound_id) ?? "") : ""}
        handleAuthError={handleAuthError}
        onChanged={fetchData}
      />

      {/* ── User list dialog（只读）────────────────────────────────── */}
      <UserListDialog
        open={userListOpen}
        onOpenChange={(open) => {
          setUserListOpen(open);
          if (!open) setUserListInbound(null);
        }}
        inbound={userListInbound}
        handleAuthError={handleAuthError}
      />

      {/* ── User allocation dialog ──────────────────────────────── */}
      <UserAllocDialog
        open={userAllocOpen}
        onOpenChange={(open) => {
          setUserAllocOpen(open);
          if (!open) setUserAllocInbound(null);
        }}
        inbound={userAllocInbound}
        handleAuthError={handleAuthError}
        onSaved={fetchData}
      />
    </div>
  );
}
