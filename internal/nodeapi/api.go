// Package nodeapi 提供节点侧 RPC method 的业务实现。
//
// 节点端不监听 HTTP，所有 method 通过 nodehub gRPC 双向流由控制面下发，
// dispatch 层调用本包的 Do* 函数执行。本文件包含：
//   - API 结构体 + 构造器 + Manager 切换逻辑
//   - 所有 Do* 业务方法（Runtime / Status / Usage / Config / Logs / Start /
//     Stop / Restart / AddUser / RemoveUser / Update / EnsureCert）
//   - LogsChannel（流式日志数据源）
package nodeapi

import (
"context"
"errors"
"log"
"sync/atomic"

"pulse/internal/buildinfo"
"pulse/internal/coremanager"
"pulse/internal/selfupdate"
"pulse/internal/sniproxy"
)

type API struct {
xrManager  coremanager.Manager // xray Manager
sniManager *sniproxy.Manager   // 可选：若节点启用了内置 SNI 代理则非空
}

func New(xrManager coremanager.Manager) *API {
return &API{xrManager: xrManager}
}

// WithSNIManager 挂入 SNI 代理 Manager，使节点支持 SNIProxy* method。
// 传 nil 则相关方法返回 ErrSNIProxyNotConfigured。
func (a *API) WithSNIManager(m *sniproxy.Manager) *API {
a.sniManager = m
return a
}

// managerFor 返回 xray Manager（当前唯一支持的核心）。
func (a *API) managerFor(_ string) coremanager.Manager {
return a.xrManager
}

// activeManager 返回 xray Manager。
func (a *API) activeManager() coremanager.Manager {
return a.xrManager
}

var nodeUpdating atomic.Bool

// DoUpdate 触发异步 self-update，返回提示信息。
func (a *API) DoUpdate() map[string]any {
if !nodeUpdating.CompareAndSwap(false, true) {
return map[string]any{"ok": false, "message": "更新已在进行中"}
}
go func() {
defer nodeUpdating.Store(false)
if err := selfupdate.Apply(context.Background(), "node"); err != nil {
log.Printf("node update: 更新失败: %v", err)
}
}()
return map[string]any{"ok": true, "message": "节点更新已开始，将在数秒后重启"}
}

// DoRuntime 返回核心运行时信息。
func (a *API) DoRuntime(ctx context.Context) map[string]any {
info := a.activeManager().RuntimeInfo(ctx)
return map[string]any{
"available":    info.Available,
"module":       info.Module,
"version":      info.Version,
"last_error":   info.LastError,
"node_version": buildinfo.Version,
}
}

// DoStatus 返回核心运行状态。
func (a *API) DoStatus() coremanager.Status { return a.activeManager().Status() }

// DoUsage 取一次 usage 快照。reset=true 时会重置 xray 的累计计数器。
func (a *API) DoUsage(reset bool) coremanager.UsageStats {
return a.activeManager().Usage(reset)
}

// DoConfig 返回当前生效的 xray 配置 JSON 字符串（包装在 {"config":...}）。
func (a *API) DoConfig() map[string]any {
return map[string]any{"config": a.activeManager().Config()}
}

// DoLogs 返回缓冲区内的历史日志行。
func (a *API) DoLogs() map[string]any {
return map[string]any{"logs": a.activeManager().Logs()}
}

// DoAccessLogs 取出并清空 access log 缓冲。若核心不实现 drainer，返回空 entries。
func (a *API) DoAccessLogs() map[string]any {
drainer, ok := a.activeManager().(coremanager.AccessLogDrainer)
if !ok {
return map[string]any{"entries": []any{}}
}
entries := drainer.DrainAccessLogs()
if entries == nil {
entries = []coremanager.AccessLogEntry{}
}
return map[string]any{"entries": entries}
}

// LogsChannel 返回一个把 xray 历史 + 实时日志统一推出的只读通道。
// ctx 取消时通道会被关闭。底层基于 coremanager.Manager 的 Subscribe/Unsubscribe。
//
// 返回的 chan 会先发送当前缓冲区的全部历史行，然后转入实时订阅模式。
func (a *API) LogsChannel(ctx context.Context) <-chan string {
out := make(chan string, 64)
mgr := a.activeManager()
go func() {
defer close(out)
for _, line := range mgr.Logs() {
select {
case out <- line:
case <-ctx.Done():
return
}
}
id, ch := mgr.Subscribe()
defer mgr.Unsubscribe(id)
for {
select {
case <-ctx.Done():
return
case line, ok := <-ch:
if !ok {
return
}
select {
case out <- line:
case <-ctx.Done():
return
}
}
}
}()
return out
}

// DoStart 启动核心，返回最新 Status。
func (a *API) DoStart(config, core string) (coremanager.Status, error) {
mgr := a.managerFor(core)
if err := mgr.Start(config); err != nil {
return coremanager.Status{}, err
}
return mgr.Status(), nil
}

// DoStop 停止 xray 核心；如果未运行视为成功。
func (a *API) DoStop() (coremanager.Status, error) {
if err := a.xrManager.Stop(); err != nil && !errors.Is(err, coremanager.ErrNotRunning) {
return coremanager.Status{}, err
}
return a.xrManager.Status(), nil
}

// DoRestart 用新配置重启核心。
func (a *API) DoRestart(config, core string) (coremanager.Status, error) {
mgr := a.managerFor(core)
if err := mgr.Restart(config); err != nil {
return coremanager.Status{}, err
}
return mgr.Status(), nil
}

// DoAddUser 校验并热增用户。
func (a *API) DoAddUser(ctx context.Context, cfg coremanager.UserConfig) error {
if cfg.InboundTag == "" || cfg.Email == "" {
return errors.New("inbound_tag and email are required")
}
return a.activeManager().AddUser(ctx, cfg)
}

// DoRemoveUser 校验并热删用户。
func (a *API) DoRemoveUser(ctx context.Context, inboundTag, email string) error {
if inboundTag == "" || email == "" {
return errors.New("inbound_tag and email are required")
}
return a.activeManager().RemoveUser(ctx, inboundTag, email)
}

// DoKickUsers 强制断开这些用户的全部存量连接，返回被断开的连接总数。
// 与 DoRemoveUser 配合完成超限止血：热删拦新连接，踢连接止住存量流量。
//
// 单个用户失败不影响其余用户，最后合并返回错误——对账兜底会一次传入多个
// email，不应因其中一个出错就整批放弃。
func (a *API) DoKickUsers(ctx context.Context, emails []string) (int, error) {
if len(emails) == 0 {
return 0, errors.New("emails is required")
}
mgr := a.activeManager()
total := 0
var firstErr error
for _, email := range emails {
if email == "" {
continue
}
n, err := mgr.KickUser(ctx, email)
if err != nil && firstErr == nil {
firstErr = err
continue
}
total += n
}
return total, firstErr
}

// DoEnsureCert 当前实现仅返回 ok（实际 ACME 流程在 certmgr 包，节点侧无操作）。
func (a *API) DoEnsureCert(domain, cfToken string) map[string]any {
_ = domain
_ = cfToken
return map[string]any{"ok": true}
}
