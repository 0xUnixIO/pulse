package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"

	"pulse/internal/nodes"
)

// fakeHub 是单测用的 hub 适配器：把 nodes.Client 的 RPC 方法名映射到
// 内联 handler，返回预置 JSON 响应。生产路径必须经由 nodehub.Hub。
type fakeHub struct {
	handlers map[string]func(nodeID string, body any) (json.RawMessage, error)
}

func (f *fakeHub) Call(_ context.Context, nodeID string, method string, body any) (json.RawMessage, error) {
	if h, ok := f.handlers[method]; ok {
		return h(nodeID, body)
	}
	return json.RawMessage(`{}`), nil
}

// hubDial 把一个 fakeHub 包装成 NodeDialer。
func hubDial(hub *fakeHub) NodeDialer {
	return func(nodeID string) (*nodes.Client, error) {
		return nodes.NewClientWithHub(nodeID, hub), nil
	}
}

// methodPaths 把 nodes.Client RPC 方法名映射到旧 HTTP 路径，
// 让基于 path-style 的老测试 handler（testDial）继续工作。
var methodPaths = map[string]string{
	"Runtime":     "/v1/node/runtime",
	"Status":      "/v1/node/runtime/status",
	"Logs":        "/v1/node/runtime/logs",
	"AccessLogs":  "/v1/node/runtime/accesslogs",
	"Config":      "/v1/node/runtime/config",
	"Usage":       "/v1/node/runtime/usage",
	"Start":       "/v1/node/runtime/start",
	"Stop":        "/v1/node/runtime/stop",
	"Restart":     "/v1/node/runtime/restart",
	"AddUser":     "/v1/node/runtime/users/add",
	"RemoveUser":  "/v1/node/runtime/users/remove",
	"KickUser":    "/v1/node/runtime/users/kick",
	"SpeedTest":   "/v1/node/speedtest",
	"CheckUnlock": "/v1/node/check",
}

// pathHub 把 path-style 的 http handler 适配为 hub.Call。请求 body 用 JSON
// 编码原 RPC body，response 用 ResponseRecorder 捕获并返回 body 字节。
// 仅供测试使用。
func pathHub(handler func(path string, w http.ResponseWriter, r *http.Request)) *fakeHub {
	hub := &fakeHub{handlers: map[string]func(string, any) (json.RawMessage, error){}}
	for method, path := range methodPaths {
		method, path := method, path
		hub.handlers[method] = func(_ string, body any) (json.RawMessage, error) {
			var bodyReader io.Reader = http.NoBody
			if body != nil {
				buf, err := json.Marshal(body)
				if err != nil {
					return nil, err
				}
				bodyReader = bytes.NewReader(buf)
			}
			req := httptest.NewRequest(http.MethodPost, path, bodyReader)
			rec := httptest.NewRecorder()
			handler(path, rec, req)
			if rec.Code >= 400 {
				return nil, &httpStatusErr{code: rec.Code, body: rec.Body.String()}
			}
			return rec.Body.Bytes(), nil
		}
	}
	return hub
}

type httpStatusErr struct {
	code int
	body string
}

func (e *httpStatusErr) Error() string { return e.body }
