import { useEffect, useState, useCallback, useMemo } from "react";
import {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
  Button,
  Input,
  Label,
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
  Switch,
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
  toast,
} from "@/components/ui";
import { api } from "@/lib/api";
import { copyText } from "@/lib/clipboard";
import { useAuthErrorHandler } from "@/hooks/useAuthErrorHandler";

// ── 类型定义 ─────────────────────────────────────────────────────

interface AuditEntry {
  id: number;
  node_id: string;
  username: string;
  source_ip: string;
  source_port: string;
  remote_ip: string;
  protocol: string;
  inbound_tag: string;
  destination: string;
  route_tag: string;
  created_at: string;
}

interface AuditLogsResponse {
  entries: AuditEntry[];
}

type RuleType = "domain_keyword" | "port" | "ip";

interface Rule {
  id: string;
  type: RuleType;
  value: string;
  enabled: boolean;
  created_at: string;
}

interface RulesResponse {
  rules: Rule[];
}

interface UsersResponse {
  users: Array<{ username: string }>;
}

interface NodesResponse {
  nodes: Array<{ id: string; name: string }>;
}

// ── 时间范围快捷选项 ──────────────────────────────────────────────

const TIME_RANGE_OPTIONS = [
  { label: "最近 1 小时", hours: 1 },
  { label: "最近 6 小时", hours: 6 },
  { label: "最近 12 小时", hours: 12 },
  { label: "最近 24 小时", hours: 24 },
] as const;

// 规则类型映射
const RULE_TYPE_LABELS: Record<RuleType, string> = {
  domain_keyword: "域名关键词",
  port: "端口",
  ip: "IP",
};

const RULE_TYPE_PLACEHOLDERS: Record<RuleType, string> = {
  domain_keyword: "如 torrent",
  port: "如 22",
  ip: "如 1.2.3.4",
};

// ── 类型：用户分析 ────────────────────────────────────────────────

interface UserAnalysis {
  username: string;
  connections: number;
  distinct_ips: number;
  total_bytes: number;
  last_seen: string;
}

// ── 工具函数 ─────────────────────────────────────────────────────

function formatBytes(n: number): string {
  if (n >= 1024 ** 3) return (n / 1024 ** 3).toFixed(1) + " GB";
  if (n >= 1024 ** 2) return (n / 1024 ** 2).toFixed(1) + " MB";
  if (n >= 1024) return (n / 1024).toFixed(1) + " KB";
  return n + " B";
}

function formatTime(iso: string): string {
  try {
    return new Date(iso).toLocaleString("zh-CN", {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
  } catch {
    return iso;
  }
}

function buildQuery(params: Record<string, string>): string {
  const qs = Object.entries(params)
    .filter(([, v]) => v.trim() !== "")
    .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v)}`)
    .join("&");
  return qs ? `?${qs}` : "";
}

// ── 历史日志子组件 ────────────────────────────────────────────────

function AuditLogsTab() {
  const handleAuthError = useAuthErrorHandler();

  // 筛选状态
  const [selectedHours, setSelectedHours] = useState<number>(1);
  const [username, setUsername] = useState("");
  const [nodeId, setNodeId] = useState("");

  // 数据状态
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [loading, setLoading] = useState(false);

  // DB 总条数
  const [totalCount, setTotalCount] = useState<number | null>(null);

  // 用户下拉列表状态：null 表示加载失败，降级为 Input
  const [userList, setUserList] = useState<string[] | null>(null);
  // 节点下拉列表状态：null 表示加载失败，降级为 Input
  const [nodeList, setNodeList] = useState<Array<{ id: string; name: string }> | null>(null);
  const nodeNames = useMemo(
    () => new Map((nodeList ?? []).map((node) => [node.id, node.name])),
    [nodeList]
  );

  // 挂载时加载用户列表、节点列表和 DB 总条数
  useEffect(() => {
    api.get<{ users: string[] }>("/audit/users")
      .then((data) => setUserList(data.users ?? []))
      .catch(() => setUserList(null));

    api.get<NodesResponse>("/nodes")
      .then((data) => setNodeList(data.nodes ?? []))
      .catch(() => setNodeList(null));

    api.get<{ count: number }>("/audit/count")
      .then((data) => setTotalCount(data.count))
      .catch(() => setTotalCount(null));
  }, []);

  const fetchLogs = useCallback(async () => {
    setLoading(true);
    try {
      const since = new Date(Date.now() - selectedHours * 3600 * 1000).toISOString();
      const until = new Date().toISOString();

      const params: Record<string, string> = { since, until };
      const effectiveUsername = username === "__all__" ? "" : username.trim();
      const effectiveNodeId = nodeId === "__all__" ? "" : nodeId.trim();
      if (effectiveUsername) params.username = effectiveUsername;
      if (effectiveNodeId) params.node_id = effectiveNodeId;

      const query = buildQuery(params);
      const data = await api.get<AuditLogsResponse>(`/audit/logs${query}`);
      // 按时间降序排列
      const sorted = [...(data.entries ?? [])].sort(
        (a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime()
      );
      setEntries(sorted);
    } catch (err) {
      if (handleAuthError(err)) return;
      toast((err as Error).message || "加载失败", "error");
    } finally {
      setLoading(false);
    }
  }, [selectedHours, username, nodeId, handleAuthError]);

  // 首次加载
  useEffect(() => {
    fetchLogs();
  }, [fetchLogs]);

  const copyAsText = useCallback(async () => {
    if (entries.length === 0) return;
    const lines = entries.map((e) =>
      [
        formatTime(e.created_at),
        e.username || "-",
        e.source_ip || "-",
        e.source_port || "-",
        e.protocol || "-",
        e.destination || "-",
        nodeNames.get(e.node_id) || e.node_id || "-",
        e.route_tag || "-",
      ].join("\t")
    );
    const text = ["时间\t用户\t源IP\t源端口\t协议\t目标地址\t节点\t路由出口", ...lines].join("\n");
    try {
      await copyText(text);
      toast(`${entries.length} 条记录已复制到剪贴板`, "success");
    } catch {
      toast("复制失败，请手动选中内容复制", "error");
    }
  }, [entries, nodeNames]);

  return (
    <div className="space-y-4">
      {/* 筛选栏 */}
      <Card>
        <CardHeader>
          <CardTitle>
            流量审计日志
            {totalCount !== null && (
              <span className="ml-2 text-sm font-normal text-muted-foreground">
                数据库共 {totalCount} 条
              </span>
            )}
          </CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex flex-wrap gap-4 items-end">
            {/* 时间范围快捷选项 */}
            <div className="space-y-1">
              <Label>时间范围</Label>
              <div className="flex gap-2">
                {TIME_RANGE_OPTIONS.map((opt) => (
                  <Button
                    key={opt.hours}
                    variant={selectedHours === opt.hours ? "default" : "outline"}
                    size="sm"
                    onClick={() => setSelectedHours(opt.hours)}
                  >
                    {opt.label}
                  </Button>
                ))}
              </div>
            </div>

            {/* 用户名：有列表时用下拉，否则降级为 Input */}
            <div className="space-y-1">
              <Label htmlFor="audit-username">用户名</Label>
              {userList !== null ? (
                <Select value={username} onValueChange={setUsername}>
                  <SelectTrigger id="audit-username" className="w-40">
                    <SelectValue placeholder="全部" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="__all__">全部</SelectItem>
                    {userList.map((u) => (
                      <SelectItem key={u} value={u}>
                        {u}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : (
                <Input
                  id="audit-username"
                  placeholder="可选"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  className="w-40"
                  onKeyDown={(e) => e.key === "Enter" && fetchLogs()}
                />
              )}
            </div>

            {/* 节点：有列表时用下拉，否则降级为 Input */}
            <div className="space-y-1">
              <Label htmlFor="audit-node-id">节点</Label>
              {nodeList !== null ? (
                <Select value={nodeId} onValueChange={setNodeId}>
                  <SelectTrigger id="audit-node-id" className="w-40">
                    <SelectValue placeholder="全部" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="__all__">全部</SelectItem>
                    {nodeList.map((n) => (
                      <SelectItem key={n.id} value={n.id}>
                        {n.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : (
                <Input
                  id="audit-node-id"
                  placeholder="可选"
                  value={nodeId}
                  onChange={(e) => setNodeId(e.target.value)}
                  className="w-40"
                  onKeyDown={(e) => e.key === "Enter" && fetchLogs()}
                />
              )}
            </div>

            {/* 查询按钮 */}
            <Button onClick={fetchLogs} disabled={loading}>
              {loading ? "查询中..." : "查询"}
            </Button>
          </div>
        </CardContent>
      </Card>

      {/* 结果表格 */}
      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle>
            查询结果
            {!loading && (
              <span className="ml-2 text-sm font-normal text-muted-foreground">
                共 {entries.length} 条
              </span>
            )}
          </CardTitle>
          {entries.length > 0 && (
            <Button variant="outline" size="sm" onClick={copyAsText}>
              复制为文本
            </Button>
          )}
        </CardHeader>
        <CardContent className="p-0">
          {entries.length === 0 && !loading ? (
            <div className="flex items-center justify-center py-16 text-muted-foreground text-sm">
              暂无数据
            </div>
          ) : (
            <Table containerClassName="max-h-[600px]">
              <TableHeader className="sticky top-0 z-10 bg-[hsl(var(--card))]">
                <TableRow>
                  <TableHead>时间</TableHead>
                  <TableHead>用户</TableHead>
                  <TableHead>源 IP</TableHead>
                  <TableHead>源端口</TableHead>
                  <TableHead>协议</TableHead>
                  <TableHead>目标地址</TableHead>
                  <TableHead>节点</TableHead>
                  <TableHead>路由出口</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {entries.map((entry) => (
                  <TableRow key={entry.id}>
                    <TableCell className="whitespace-nowrap text-sm">
                      {formatTime(entry.created_at)}
                    </TableCell>
                    <TableCell className="text-sm">{entry.username || "-"}</TableCell>
                    <TableCell className="font-mono text-sm">{entry.source_ip || "-"}</TableCell>
                    <TableCell className="font-mono text-sm">{entry.source_port || "-"}</TableCell>
                    <TableCell className="text-sm">{entry.protocol || "-"}</TableCell>
                    <TableCell className="font-mono text-sm min-w-64">{entry.destination || "-"}</TableCell>
                    <TableCell className="whitespace-nowrap text-sm">
                      {nodeNames.get(entry.node_id) || entry.node_id || "-"}
                    </TableCell>
                    <TableCell className="text-sm">{entry.route_tag || "-"}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

// ── 告警规则子组件 ────────────────────────────────────────────────

function AlertRulesTab() {

  // 规则列表状态
  const [rules, setRules] = useState<Rule[]>([]);
  const [loading, setLoading] = useState(false);

  // 新增表单状态
  const [newType, setNewType] = useState<RuleType>("domain_keyword");
  const [newValue, setNewValue] = useState("");
  const [adding, setAdding] = useState(false);

  // 正在切换启用状态的规则 ID 集合
  const [togglingIds, setTogglingIds] = useState<Set<string>>(new Set());
  // 正在删除的规则 ID 集合
  const [deletingIds, setDeletingIds] = useState<Set<string>>(new Set());

  const handleAuthError = useAuthErrorHandler();

  const fetchRules = useCallback(async () => {
    setLoading(true);
    try {
      const data = await api.get<RulesResponse>("/audit/rules");
      setRules(data.rules ?? []);
    } catch (err) {
      if (handleAuthError(err)) return;
      toast((err as Error).message || "加载规则失败", "error");
    } finally {
      setLoading(false);
    }
  }, [handleAuthError]);

  useEffect(() => {
    fetchRules();
  }, [fetchRules]);

  // 新增规则
  const handleAdd = useCallback(async () => {
    const trimmed = newValue.trim();
    if (!trimmed) {
      toast("请填写规则值", "error");
      return;
    }
    setAdding(true);
    try {
      const created = await api.post<Rule>("/audit/rules", {
        type: newType,
        value: trimmed,
      });
      setRules((prev) => [created, ...prev]);
      setNewValue("");
      toast("规则已添加", "success");
    } catch (err) {
      if (handleAuthError(err)) return;
      toast((err as Error).message || "添加失败", "error");
    } finally {
      setAdding(false);
    }
  }, [newType, newValue, handleAuthError]);

  // 切换启用/禁用
  const handleToggle = useCallback(
    async (rule: Rule) => {
      setTogglingIds((prev) => new Set(prev).add(rule.id));
      try {
        await api.post<Rule>(`/audit/rules/${rule.id}`, { enabled: !rule.enabled });
        setRules((prev) =>
          prev.map((r) => (r.id === rule.id ? { ...r, enabled: !r.enabled } : r))
        );
      } catch (err) {
        if (handleAuthError(err)) return;
        toast((err as Error).message || "操作失败", "error");
      } finally {
        setTogglingIds((prev) => {
          const next = new Set(prev);
          next.delete(rule.id);
          return next;
        });
      }
    },
    [handleAuthError]
  );

  // 删除规则
  const handleDelete = useCallback(
    async (id: string) => {
      setDeletingIds((prev) => new Set(prev).add(id));
      try {
        await api.del(`/audit/rules/${id}`);
        setRules((prev) => prev.filter((r) => r.id !== id));
        toast("规则已删除", "success");
      } catch (err) {
        if (handleAuthError(err)) return;
        toast((err as Error).message || "删除失败", "error");
      } finally {
        setDeletingIds((prev) => {
          const next = new Set(prev);
          next.delete(id);
          return next;
        });
      }
    },
    [handleAuthError]
  );

  return (
    <div className="space-y-4">
      {/* 新增规则表单 */}
      <Card>
        <CardHeader>
          <CardTitle>新增规则</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex flex-wrap gap-4 items-end">
            {/* 类型选择 */}
            <div className="space-y-1">
              <Label htmlFor="rule-type">类型</Label>
              <Select
                value={newType}
                onValueChange={(v) => setNewType(v as RuleType)}
              >
                <SelectTrigger id="rule-type" className="w-36">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="domain_keyword">域名关键词</SelectItem>
                  <SelectItem value="port">端口</SelectItem>
                  <SelectItem value="ip">IP</SelectItem>
                </SelectContent>
              </Select>
            </div>

            {/* 值输入框 */}
            <div className="space-y-1">
              <Label htmlFor="rule-value">值</Label>
              <Input
                id="rule-value"
                placeholder={RULE_TYPE_PLACEHOLDERS[newType]}
                value={newValue}
                onChange={(e) => setNewValue(e.target.value)}
                className="w-52"
                onKeyDown={(e) => e.key === "Enter" && handleAdd()}
              />
            </div>

            {/* 添加按钮 */}
            <Button onClick={handleAdd} disabled={adding}>
              {adding ? "添加中..." : "添加"}
            </Button>
          </div>
        </CardContent>
      </Card>

      {/* 规则列表 */}
      <Card>
        <CardHeader>
          <CardTitle>
            规则列表
            {!loading && (
              <span className="ml-2 text-sm font-normal text-muted-foreground">
                共 {rules.length} 条
              </span>
            )}
          </CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {rules.length === 0 && !loading ? (
            <div className="flex items-center justify-center py-16 text-muted-foreground text-sm">
              暂无规则
            </div>
          ) : (
            <Table containerClassName="max-h-[600px]">
              <TableHeader className="sticky top-0 z-10 bg-[hsl(var(--card))]">
                <TableRow>
                  <TableHead>类型</TableHead>
                  <TableHead>值</TableHead>
                  <TableHead>创建时间</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rules.map((rule) => (
                  <TableRow key={rule.id}>
                    <TableCell className="text-sm">
                      {RULE_TYPE_LABELS[rule.type] ?? rule.type}
                    </TableCell>
                    <TableCell className="font-mono text-sm">{rule.value}</TableCell>
                    <TableCell className="whitespace-nowrap text-sm text-muted-foreground">
                      {formatTime(rule.created_at)}
                    </TableCell>
                    <TableCell>
                      <Switch
                        checked={rule.enabled}
                        disabled={togglingIds.has(rule.id)}
                        onCheckedChange={() => handleToggle(rule)}
                        aria-label={rule.enabled ? "禁用规则" : "启用规则"}
                      />
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={deletingIds.has(rule.id)}
                        onClick={() => handleDelete(rule.id)}
                      >
                        {deletingIds.has(rule.id) ? "删除中..." : "删除"}
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

// ── 用户分析子组件 ────────────────────────────────────────────────

function AnalysisTab() {
  const handleAuthError = useAuthErrorHandler();
  const [selectedHours, setSelectedHours] = useState<number>(24);
  const [users, setUsers] = useState<UserAnalysis[]>([]);
  const [loading, setLoading] = useState(false);

  const fetchAnalysis = useCallback(async () => {
    setLoading(true);
    try {
      const since = new Date(Date.now() - selectedHours * 3600 * 1000).toISOString();
      const until = new Date().toISOString();
      const data = await api.get<{ users: UserAnalysis[] }>(
        `/audit/analysis?since=${encodeURIComponent(since)}&until=${encodeURIComponent(until)}`
      );
      setUsers(data.users ?? []);
    } catch (err) {
      if (handleAuthError(err)) return;
      toast((err as Error).message || "加载失败", "error");
    } finally {
      setLoading(false);
    }
  }, [selectedHours, handleAuthError]);

  useEffect(() => { fetchAnalysis(); }, [fetchAnalysis]);

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader><CardTitle>用户行为分析</CardTitle></CardHeader>
        <CardContent>
          <div className="flex flex-wrap gap-4 items-end">
            <div className="space-y-1">
              <Label>时间范围</Label>
              <div className="flex gap-2">
                {TIME_RANGE_OPTIONS.map((opt) => (
                  <Button
                    key={opt.hours}
                    variant={selectedHours === opt.hours ? "default" : "outline"}
                    size="sm"
                    onClick={() => setSelectedHours(opt.hours)}
                  >
                    {opt.label}
                  </Button>
                ))}
              </div>
            </div>
            <Button onClick={fetchAnalysis} disabled={loading}>
              {loading ? "分析中..." : "分析"}
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>
            用户汇总
            {!loading && (
              <span className="ml-2 text-sm font-normal text-muted-foreground">
                共 {users.length} 个用户
              </span>
            )}
          </CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <p className="px-4 pt-3 pb-1 text-xs text-muted-foreground">
            独立 IP 数 &gt; 3 可能存在账号共享，建议核查
          </p>
          {users.length === 0 && !loading ? (
            <div className="flex items-center justify-center py-16 text-muted-foreground text-sm">
              暂无数据
            </div>
          ) : (
            <Table containerClassName="max-h-[600px]">
                <TableHeader className="sticky top-0 z-10 bg-[hsl(var(--card))]">
                  <TableRow>
                    <TableHead>用户</TableHead>
                    <TableHead>独立 IP 数</TableHead>
                    <TableHead>连接数</TableHead>
                    <TableHead>时间段流量</TableHead>
                    <TableHead>最近活跃</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {users.map((u) => (
                    <TableRow key={u.username}>
                      <TableCell className="text-sm font-medium">{u.username}</TableCell>
                      <TableCell className={`text-sm font-mono font-semibold ${u.distinct_ips > 3 ? "text-orange-500" : ""}`}>
                        {u.distinct_ips}
                      </TableCell>
                      <TableCell className="text-sm font-mono">{u.connections}</TableCell>
                      <TableCell className="text-sm font-mono">
                        {u.total_bytes > 0 ? formatBytes(u.total_bytes) : "-"}
                      </TableCell>
                      <TableCell className="text-sm whitespace-nowrap">
                        {formatTime(u.last_seen)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

// ── 页面组件 ─────────────────────────────────────────────────────

export default function AuditPage() {
  return (
    <div className="p-6 space-y-4">
      <Tabs defaultValue="logs">
        <TabsList>
          <TabsTrigger value="logs">历史日志</TabsTrigger>
          <TabsTrigger value="rules">告警规则</TabsTrigger>
          <TabsTrigger value="analysis">用户分析</TabsTrigger>
        </TabsList>

        <TabsContent value="logs">
          <AuditLogsTab />
        </TabsContent>

        <TabsContent value="rules">
          <AlertRulesTab />
        </TabsContent>

        <TabsContent value="analysis">
          <AnalysisTab />
        </TabsContent>
      </Tabs>
    </div>
  );
}
