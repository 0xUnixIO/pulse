import { useEffect, useState, useCallback, useMemo, type FormEvent } from "react";
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
  Textarea,
  MultiSelect,
  type MultiSelectOption,
  SingleSelect,
} from "@/components/ui";
import { api } from "@/lib/api";
import { useAuthErrorHandler } from "@/hooks/useAuthErrorHandler";
import { hostSubName } from "@/lib/format";
import type {
  RouteRule,
  RouteRuleType,
  RouteRulesResponse,
  Outbound,
  OutboundsResponse,
  SSOutboundOption,
  SSOutboundOptionsResponse,
  Inbound,
  InboundsResponse,
  Node,
  NodesResponse,
  Host,
  HostsResponse,
} from "@/lib/types";

// ── Icons ────────────────────────────────────────────────────────

function RouteIcon(props: React.SVGProps<SVGSVGElement>) {
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
      <circle cx="6" cy="19" r="3" />
      <path d="M9 19h8.5a3.5 3.5 0 0 0 0-7h-11a3.5 3.5 0 0 1 0-7H15" />
      <circle cx="18" cy="5" r="3" />
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

const RULE_TYPES: RouteRuleType[] = [
  "domain_suffix",
  "domain_keyword",
  "domain",
  "ip_cidr",
];

const RULE_TYPE_LABELS: Record<RouteRuleType, string> = {
  domain_suffix: "Domain Suffix",
  domain_keyword: "Domain Keyword",
  domain: "Domain",
  ip_cidr: "IP CIDR",
};

const RULE_TYPE_BADGE_VARIANT: Record<
  RouteRuleType,
  "default" | "secondary" | "outline"
> = {
  domain_suffix: "default",
  domain_keyword: "secondary",
  domain: "outline",
  ip_cidr: "secondary",
};

// ── Empty form state ─────────────────────────────────────────────

interface RouteRuleForm {
  name: string;
  rule_type: RouteRuleType;
  patterns: string;
  outbound_id: string;
  priority: number;
  inbound_ids: string[];
}

const EMPTY_FORM: RouteRuleForm = {
  name: "",
  rule_type: "domain_suffix",
  patterns: "",
  outbound_id: "",
  priority: 100,
  inbound_ids: [],
};

// ── Skeleton rows ────────────────────────────────────────────────

function SkeletonRow() {
  return (
    <TableRow>
      {Array.from({ length: 6 }).map((_, i) => (
        <TableCell key={i} className="px-4">
          <div className="h-4 w-24 animate-pulse rounded bg-[hsl(var(--muted))]" />
        </TableCell>
      ))}
    </TableRow>
  );
}

// ── Main page ────────────────────────────────────────────────────

export default function RouteRulesPage() {
  const handleAuthError = useAuthErrorHandler();

  const [rules, setRules] = useState<RouteRule[]>([]);
  const [outbounds, setOutbounds] = useState<Outbound[]>([]);
  const [ssOutboundOptions, setSSOutboundOptions] = useState<SSOutboundOption[]>([]);
  const [allInbounds, setAllInbounds] = useState<Inbound[]>([]);
  const [allNodes, setAllNodes] = useState<Node[]>([]);
  const [allHosts, setAllHosts] = useState<Host[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Dialog states
  const [createOpen, setCreateOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  // Form data
  const [form, setForm] = useState<RouteRuleForm>(EMPTY_FORM);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [deletingRule, setDeletingRule] = useState<RouteRule | null>(null);

  const patchForm = useCallback(
    (patch: Partial<RouteRuleForm>) =>
      setForm((prev) => ({ ...prev, ...patch })),
    [],
  );

  // ── Node name map ─────────────────────────────────────────────

  const nodeMap = new Map<string, string>();
  for (const n of allNodes) {
    nodeMap.set(n.id, n.name);
  }

  const inboundOptions: MultiSelectOption[] = useMemo(() =>
    allInbounds.map((ib) => {
      const nodeName = nodeMap.get(ib.node_id) ?? ib.node_id;
      const primaryHost = allHosts.find((h) => h.inbound_id === ib.id);
      const subName = primaryHost ? hostSubName(primaryHost, ib.traffic_rate) : "";
      const displayName = subName || ib.tag || `${ib.protocol}:${ib.port}`;
      return {
        value: ib.id,
        triggerLabel: `${nodeName} · ${displayName}`,
        label: (
          <span>
            <span className="text-[hsl(var(--muted-foreground))]">{nodeName} · </span>
            <span className="font-medium">{displayName}</span>
          </span>
        ),
      };
    }), [allInbounds, allNodes, allHosts]); // eslint-disable-line react-hooks/exhaustive-deps

  // ── Outbound name map ─────────────────────────────────────────

  const outboundMap = new Map<string, string>();
  for (const ob of outbounds) {
    outboundMap.set(ob.id, `${ob.name} (${ob.protocol})`);
  }
  for (const opt of ssOutboundOptions) {
    outboundMap.set(opt.id, opt.label);
  }

  // ── Fetch ───────────────────────────────────────────────────────

  const fetchData = useCallback(() => {
    setLoading(true);
    setError(null);
    Promise.all([
      api.get<RouteRulesResponse>("/routerules"),
      api.get<OutboundsResponse>("/outbounds"),
      api.get<SSOutboundOptionsResponse>("/inbounds/ss-outbound-options"),
      api.get<InboundsResponse>("/inbounds"),
      api.get<NodesResponse>("/nodes"),
      api.get<HostsResponse>("/hosts").catch(() => ({ hosts: [] }) as HostsResponse),
    ])
      .then(([rulesRes, outboundsRes, ssRes, inboundsRes, nodesRes, hostsRes]) => {
        setRules(rulesRes.rules ?? []);
        setOutbounds(outboundsRes.outbounds ?? []);
        setSSOutboundOptions(ssRes.options ?? []);
        setAllInbounds(inboundsRes.inbounds ?? []);
        setAllNodes(nodesRes.nodes ?? []);
        setAllHosts(hostsRes.hosts ?? []);
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

  function buildBody(): Omit<RouteRule, "id"> {
    return {
      name: form.name.trim(),
      rule_type: form.rule_type,
      patterns: form.patterns.trim(),
      outbound_id: form.outbound_id,
      priority: Number(form.priority) || 100,
      inbound_ids: form.inbound_ids.join(","),
    };
  }

  // ── Create ──────────────────────────────────────────────────────

  async function handleCreate(e: FormEvent) {
    e.preventDefault();
    setFormError(null);
    setSubmitting(true);
    try {
      await api.post<RouteRule>("/routerules", buildBody());
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

  function openEdit(rule: RouteRule) {
    setEditingId(rule.id);
    setForm({
      name: rule.name,
      rule_type: rule.rule_type,
      patterns: rule.patterns,
      outbound_id: rule.outbound_id,
      priority: rule.priority,
      inbound_ids: rule.inbound_ids ? rule.inbound_ids.split(",").filter(Boolean) : [],
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
      await api.put<RouteRule>(`/routerules/${editingId}`, buildBody());
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

  function openDelete(rule: RouteRule) {
    setDeletingRule(rule);
    setFormError(null);
    setDeleteOpen(true);
  }

  async function handleDelete() {
    if (!deletingRule) return;
    setFormError(null);
    setSubmitting(true);
    try {
      await api.del(`/routerules/${deletingRule.id}`);
      setDeleteOpen(false);
      setDeletingRule(null);
      fetchData();
    } catch (err) {
      if (handleAuthError(err)) return;
      setFormError(err instanceof Error ? err.message : "删除失败");
    } finally {
      setSubmitting(false);
    }
  }

  // ── Error state ─────────────────────────────────────────────────

  if (error && !rules.length) {
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
          <Label htmlFor="rr-name">名称 *</Label>
          <Input
            id="rr-name"
            required
            value={form.name}
            onChange={(e) => patchForm({ name: e.target.value })}
            placeholder="my-route-rule"
          />
        </div>

        <div className="space-y-2">
          <Label htmlFor="rr-type">类型</Label>
          <Select
            value={form.rule_type}
            onValueChange={(v) =>
              patchForm({ rule_type: v as RouteRuleType })
            }
          >
            <SelectTrigger id="rr-type">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {RULE_TYPES.map((t) => (
                <SelectItem key={t} value={t}>
                  {RULE_TYPE_LABELS[t]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="space-y-2">
          <Label htmlFor="rr-patterns">匹配模式 *</Label>
          <Textarea
            id="rr-patterns"
            required
            value={form.patterns}
            onChange={(e) => patchForm({ patterns: e.target.value })}
            placeholder="每行一个或逗号分隔"
            rows={4}
          />
        </div>

        <div className="space-y-2">
          <Label>入口</Label>
          <MultiSelect
            value={form.inbound_ids}
            onChange={(ids) => patchForm({ inbound_ids: ids })}
            options={inboundOptions}
            placeholder="所有入口（默认）"
            countLabel="已选 {n} 个入口"
          />
        </div>

        <div className="space-y-2">
          <Label>出口</Label>
          <SingleSelect
            value={form.outbound_id || "__direct__"}
            onChange={(v) => patchForm({ outbound_id: v === "__direct__" ? "" : v })}
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

        <div className="space-y-2">
          <Label htmlFor="rr-priority">优先级</Label>
          <Input
            id="rr-priority"
            type="number"
            min={0}
            max={9999}
            value={form.priority}
            onChange={(e) =>
              patchForm({ priority: Number(e.target.value) || 0 })
            }
          />
        </div>

      </div>
    );
  }

  // ── Helper: truncate pattern text ───────────────────────────────

  function truncate(text: string, max: number) {
    return text.length > max ? text.slice(0, max) + "…" : text;
  }

  // ── Render ──────────────────────────────────────────────────────

  return (
    <div className="flex h-full flex-col p-4 sm:p-6 lg:p-8">
      <div className="mb-6 flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold text-[hsl(var(--foreground))]">
            路由规则
          </h1>
          <p className="mt-1 text-sm text-[hsl(var(--muted-foreground))]">
            管理代理路由规则，控制流量出口。
          </p>
        </div>

        {/* ── Create dialog ───────────────────────────────────── */}
        <Dialog
          open={createOpen}
          onOpenChange={(open) => {
            setCreateOpen(open);
            if (!open) {
              setForm(EMPTY_FORM);
              setFormError(null);
            }
          }}
        >
          <DialogTrigger asChild>
            <Button>+ 添加规则</Button>
          </DialogTrigger>
          <DialogContent className="sm:max-w-lg">
            <form onSubmit={handleCreate}>
              <DialogHeader>
                <DialogTitle>添加规则</DialogTitle>
                <DialogDescription>
                  配置新的路由规则。
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
              <TableHead className="px-4">优先级</TableHead>
              <TableHead className="px-4">名称</TableHead>
              <TableHead className="px-4">类型</TableHead>
              <TableHead className="px-4">出口</TableHead>
              <TableHead className="px-4">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              Array.from({ length: 3 }).map((_, i) => (
                <SkeletonRow key={i} />
              ))
            ) : rules.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={5}
                  className="h-32 text-center text-[hsl(var(--muted-foreground))]"
                >
                  <div className="flex flex-col items-center gap-3">
                    <RouteIcon className="h-10 w-10 opacity-40" />
                    <p>暂无路由规则</p>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => setCreateOpen(true)}
                    >
                      + 添加规则
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              rules.map((rule) => (
                <TableRow key={rule.id}>
                  <TableCell className="px-4 font-mono text-sm text-[hsl(var(--muted-foreground))]">
                    {rule.priority}
                  </TableCell>
                  <TableCell className="px-4 font-medium text-[hsl(var(--foreground))]">
                    {rule.name}
                  </TableCell>
                  <TableCell className="px-4">
                    <Badge
                      variant={
                        RULE_TYPE_BADGE_VARIANT[rule.rule_type] ?? "outline"
                      }
                    >
                      {RULE_TYPE_LABELS[rule.rule_type] ?? rule.rule_type}
                    </Badge>
                  </TableCell>
                  <TableCell className="px-4 text-sm text-[hsl(var(--muted-foreground))]">
                    {rule.outbound_id
                      ? outboundMap.get(rule.outbound_id) ?? rule.outbound_id
                      : "direct"}
                  </TableCell>
                  <TableCell className="px-4">
                    <div className="flex gap-1">
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => openEdit(rule)}
                      >
                        <EditIcon className="h-4 w-4" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-[hsl(var(--destructive))] hover:text-[hsl(var(--destructive))]"
                        onClick={() => openDelete(rule)}
                      >
                        <TrashIcon className="h-4 w-4" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Card>

      {/* ── Edit dialog ─────────────────────────────────────────── */}
      <Dialog
        open={editOpen}
        onOpenChange={(open) => {
          setEditOpen(open);
          if (!open) {
            setForm(EMPTY_FORM);
            setEditingId(null);
            setFormError(null);
          }
        }}
      >
        <DialogContent className="sm:max-w-lg">
          <form onSubmit={handleEdit}>
            <DialogHeader>
              <DialogTitle>编辑规则</DialogTitle>
              <DialogDescription>
                修改路由规则配置。
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
            setDeletingRule(null);
            setFormError(null);
          }
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>确认删除</DialogTitle>
            <DialogDescription>
              确定要删除规则{" "}
              <span className="font-semibold text-[hsl(var(--foreground))]">
                {deletingRule?.name}
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
    </div>
  );
}
